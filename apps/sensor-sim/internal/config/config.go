// Package config loads and validates the sensor-sim configuration.
//
// Values come from built-in defaults, an optional YAML file (--config) and
// command-line flags, in increasing order of precedence. Two connection values
// also read the environment: the token (SENSOR_SIM_TOKEN, then
// SENTINEL_INGEST_TOKEN) and the address (SENSOR_SIM_ADDR), which sit between
// the flags and the YAML. A flag overrides only when it was set explicitly on
// the command line, so --rate 1 beats a YAML rate of 5 even though 1 is the
// default.
//
// One process simulates one sensor, a radar or an EO/IR camera (--kind). The
// scenario, the targets they observe, is YAML only and is meant to be shared
// by the radar and camera processes through one file.
//
// The token is a credential. It is never included in an error, in String(),
// or in any fmt verb applied to a Config or Connection.
package config

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sentinel/sensor-sim/internal/scenario"
)

// Environment variables read by Load.
const (
	EnvToken         = "SENSOR_SIM_TOKEN"
	EnvFallbackToken = "SENTINEL_INGEST_TOKEN"
	EnvAddr          = "SENSOR_SIM_ADDR"
)

// Sensor kinds.
const (
	KindRadar = "radar"
	KindEOIR  = "eoir"
)

// EO/IR bands, named after the proto EoIrBand enum.
var bands = []string{"VISIBLE", "SWIR", "MWIR", "LWIR"}

// DefaultTerrainURL is where terrain tiles come from when none is given.
const DefaultTerrainURL = "https://copernicus-dem-30m.s3.amazonaws.com"

const (
	// MaxRate is the largest --rate, in Hz.
	MaxRate = 100
	// DefaultHeartbeatInterval is how often the emplacement heartbeats by default.
	DefaultHeartbeatInterval = 5 * time.Second
	// MaxHeartbeatInterval is the longest --heartbeat-interval (the server's limit).
	MaxHeartbeatInterval = 300 * time.Second
	// MaxHeightAboveGroundM is the tallest mounting height the server accepts.
	MaxHeightAboveGroundM = 1000

	// DefaultCameraElevMinDeg and DefaultCameraElevMaxDeg are the elevation
	// window of a camera whose configuration sets none: it looks a little
	// below the horizon and well up into the sky, a 90 degree vertical field.
	// A radar's default window is unlimited, -90 to +90.
	DefaultCameraElevMinDeg = -15.0
	DefaultCameraElevMaxDeg = 75.0
)

// Config is everything the simulator needs to run. Load returns it normalised
// (enum strings canonicalised, defaults filled) and validated.
type Config struct {
	Connection Connection `yaml:"connection"`
	Sensor     Sensor     `yaml:"sensor"`
	// Scenario is the shared truth the sensor observes. ScenarioFile names a
	// YAML file holding it instead (its keys are the scenario's: epoch and
	// targets), resolved relative to the configuration file, so a radar and a
	// camera configuration can share one scenario. At most one may be set.
	Scenario     scenario.Scenario `yaml:"scenario"`
	ScenarioFile string            `yaml:"scenarioFile"`
	// Seed seeds every random draw. SeedGenerated is true when it was chosen
	// from the clock because none was given, so the caller can log it.
	Seed          int64         `yaml:"-"`
	SeedGenerated bool          `yaml:"-"`
	Duration      time.Duration `yaml:"duration"` // 0 runs until signalled
	// Verbose logs every report as it is sent, and each ack.
	Verbose bool          `yaml:"verbose"`
	Terrain TerrainConfig `yaml:"terrain"`
	// ConfigFile is the --config path, or empty.
	ConfigFile string `yaml:"-"`
}

// Connection describes how to reach and authenticate to the ingest service.
type Connection struct {
	Addr     string `yaml:"addr"`  // host:port
	Token    string `yaml:"token"` // si_... ingest token; never log it
	Insecure bool   `yaml:"insecure"`
	TLS      bool   `yaml:"tls"`
	CA       string `yaml:"ca"`   // extra root CA file (TLS only)
	Cert     string `yaml:"cert"` // client certificate file (TLS only)
	Key      string `yaml:"key"`  // client key file (TLS only)
}

// Sensor is the sensor site and its detection model.
type Sensor struct {
	// Kind is KindRadar or KindEOIR.
	Kind string  `yaml:"kind"`
	Name string  `yaml:"name"`
	Lat  float64 `yaml:"lat"`
	Lon  float64 `yaml:"lon"`
	// Alt is the site altitude in metres above the WGS84 ellipsoid.
	// HeightAboveGroundM places the site that far above the terrain instead
	// (it needs terrain). An EO/IR camera needs one of them; at most one may be set.
	Alt                *float64 `yaml:"alt"`
	HeightAboveGroundM *float64 `yaml:"heightAboveGroundM"`

	Boresight  float64 `yaml:"boresight"`  // degrees, [0,360)
	Width      float64 `yaml:"width"`      // azimuth width, degrees; 360 is omni
	RangeKm    float64 `yaml:"rangeKm"`    // maximum range
	MinRangeKm float64 `yaml:"minRangeKm"` // blind zone
	// ElevationMinDeg and ElevationMaxDeg limit the elevation coverage; nil
	// leaves the kind's default (radar unlimited, camera a built-in window).
	ElevationMinDeg *float64 `yaml:"elevationMinDeg"`
	ElevationMaxDeg *float64 `yaml:"elevationMaxDeg"`

	Rate float64 `yaml:"rate"` // scans per second
	Pd   float64 `yaml:"pd"`   // probability of detection, (0,1]
	// Measurement noise, 1-sigma. SigmaRangeM is unused by a camera.
	SigmaAzDeg  float64 `yaml:"sigmaAzDeg"`
	SigmaElDeg  float64 `yaml:"sigmaElDeg"`
	SigmaRangeM float64 `yaml:"sigmaRangeM"`
	// HeartbeatInterval is how often the emplacement sends its EmplacementState
	// heartbeat, and the interval it declares: whole seconds, 1s to 300s.
	HeartbeatInterval time.Duration `yaml:"heartbeatInterval"`
	// Band is the EO/IR band: VISIBLE, SWIR, MWIR or LWIR.
	Band string `yaml:"band"`
}

// TerrainConfig says where terrain comes from. An empty CacheDir turns
// terrain off.
type TerrainConfig struct {
	CacheDir  string `yaml:"cacheDir"`
	SourceURL string `yaml:"sourceUrl"`
}

// ElevationWindow is the elevation coverage in degrees once the kind's
// defaults fill what the configuration leaves unset: a camera's are
// DefaultCameraElevMinDeg and DefaultCameraElevMaxDeg, a radar's -90 and +90.
func (s Sensor) ElevationWindow() (lo, hi float64) {
	lo, hi = -90, 90
	if strings.EqualFold(s.Kind, KindEOIR) {
		lo, hi = DefaultCameraElevMinDeg, DefaultCameraElevMaxDeg
	}
	if s.ElevationMinDeg != nil {
		lo = *s.ElevationMinDeg
	}
	if s.ElevationMaxDeg != nil {
		hi = *s.ElevationMaxDeg
	}
	return lo, hi
}

// HeartbeatEvery is the heartbeat interval, the default when none is set.
func (s Sensor) HeartbeatEvery() time.Duration {
	if s.HeartbeatInterval <= 0 {
		return DefaultHeartbeatInterval
	}
	return s.HeartbeatInterval
}

// HeartbeatSeconds is HeartbeatEvery in whole seconds, as the heartbeat declares it.
func (s Sensor) HeartbeatSeconds() uint32 { return uint32(s.HeartbeatEvery() / time.Second) }

// String describes the connection with the token redacted.
func (c Connection) String() string {
	return fmt.Sprintf("{addr=%s token=%s insecure=%t tls=%t ca=%q cert=%q key=%q}",
		c.Addr, c.RedactedToken(), c.Insecure, c.TLS, c.CA, c.Cert, c.Key)
}

// GoString keeps %#v from printing the token.
func (c Connection) GoString() string { return "config.Connection" + c.String() }

// RedactedToken returns a form of the token that is safe to log: a prefix of
// at most 8 characters and never more than half of the token.
func (c Connection) RedactedToken() string {
	if c.Token == "" {
		return "<unset>"
	}
	n := len(c.Token) / 2
	if n > 8 {
		n = 8
	}
	return c.Token[:n] + "..."
}

func defaults() Config {
	return Config{
		Sensor: Sensor{
			Name: "sensor-sim", Width: 90, RangeKm: 10,
			Rate: 1, Pd: 0.9, SigmaAzDeg: 0.5, SigmaElDeg: 0.5, SigmaRangeM: 10,
			HeartbeatInterval: DefaultHeartbeatInterval, Band: "VISIBLE",
		},
		Terrain: TerrainConfig{SourceURL: DefaultTerrainURL},
	}
}

// flagValues receives the parsed flags. Which of them the user actually set
// is read from FlagSet.Visit, never inferred from the values.
type flagValues struct {
	configFile                 string
	addr, token                string
	insecure, tls              bool
	ca, cert, key              string
	kind, name                 string
	lat, lon, alt, height      float64
	boresight, width           float64
	rangeKm, minRangeKm        float64
	elevMin, elevMax           float64
	rate, pd                   float64
	sigmaAz, sigmaEl, sigmaRng float64
	band                       string
	seed                       int64
	duration                   time.Duration
	heartbeatInterval          time.Duration
	verbose                    bool
	terrainCache, terrainURL   string
}

func newFlagSet(fv *flagValues, d Config) *flag.FlagSet {
	fs := flag.NewFlagSet("sensor-sim", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.StringVar(&fv.configFile, "config", "", "YAML `file` with the connection, sensor, scenario and terrain; flags override it")

	fs.StringVar(&fv.addr, "addr", "", "ingest service `host:port` (env "+EnvAddr+")")
	fs.StringVar(&fv.token, "token", "", "ingest token (env "+EnvToken+" or "+EnvFallbackToken+")")
	fs.BoolVar(&fv.insecure, "insecure", false, "plaintext h2c; the token travels in clear (exactly one of --insecure and --tls)")
	fs.BoolVar(&fv.tls, "tls", false, "use TLS (exactly one of --insecure and --tls)")
	fs.StringVar(&fv.ca, "ca", "", "extra root CA `file` (requires --tls)")
	fs.StringVar(&fv.cert, "cert", "", "client certificate `file` (requires --tls and --key)")
	fs.StringVar(&fv.key, "key", "", "client key `file` (requires --tls and --cert)")

	s := d.Sensor
	fs.StringVar(&fv.kind, "kind", "", "sensor kind: radar or eoir (required)")
	fs.StringVar(&fv.name, "name", s.Name, "sensor name (the emplacement name)")
	fs.Float64Var(&fv.lat, "lat", 0, "sensor latitude, degrees")
	fs.Float64Var(&fv.lon, "lon", 0, "sensor longitude, degrees")
	fs.Float64Var(&fv.alt, "alt", 0, "sensor altitude, metres above the WGS84 ellipsoid (an EO/IR camera needs --alt or --height-above-ground)")
	fs.Float64Var(&fv.height, "height-above-ground", 0, "sensor height above the terrain, metres (needs --terrain-cache; instead of --alt)")
	fs.Float64Var(&fv.boresight, "boresight", s.Boresight, "boresight azimuth, degrees")
	fs.Float64Var(&fv.width, "width", s.Width, "azimuth coverage width, degrees (360 is omni)")
	fs.Float64Var(&fv.rangeKm, "range-km", s.RangeKm, "maximum range, km")
	fs.Float64Var(&fv.minRangeKm, "min-range-km", s.MinRangeKm, "minimum range, km")
	fs.Float64Var(&fv.elevMin, "elev-min", 0, "lowest elevation covered, degrees")
	fs.Float64Var(&fv.elevMax, "elev-max", 0, "highest elevation covered, degrees")

	fs.Float64Var(&fv.rate, "rate", s.Rate, "scans per second")
	fs.Float64Var(&fv.pd, "pd", s.Pd, "probability of detection, (0,1]")
	fs.Float64Var(&fv.sigmaAz, "sigma-az", s.SigmaAzDeg, "azimuth noise 1-sigma, degrees")
	fs.Float64Var(&fv.sigmaEl, "sigma-el", s.SigmaElDeg, "elevation noise 1-sigma, degrees")
	fs.Float64Var(&fv.sigmaRng, "sigma-range-m", s.SigmaRangeM, "range noise 1-sigma, metres (radar)")
	fs.StringVar(&fv.band, "band", s.Band, "EO/IR band: VISIBLE, SWIR, MWIR or LWIR")
	fs.Int64Var(&fv.seed, "seed", 0, "random seed (default: from the clock)")
	fs.DurationVar(&fv.duration, "duration", 0, "run time, e.g. 5m; 0 runs until signalled")
	fs.DurationVar(&fv.heartbeatInterval, "heartbeat-interval", s.HeartbeatInterval, "how often the emplacement heartbeats, whole seconds from 1s to 300s")
	fs.BoolVar(&fv.verbose, "verbose", false, "log every report as JSON as it is sent, and each ack")
	fs.StringVar(&fv.terrainCache, "terrain-cache", d.Terrain.CacheDir, "terrain tile cache `dir`; empty turns terrain off")
	fs.StringVar(&fv.terrainURL, "terrain-url", d.Terrain.SourceURL, "terrain tile source `url`")
	return fs
}

// Usage returns the flag help text.
func Usage() string {
	var fv flagValues
	fs := newFlagSet(&fv, defaults())
	var b bytes.Buffer
	fs.SetOutput(&b)
	fs.PrintDefaults()
	return b.String()
}

// fileDoc is the YAML document: a Config plus the seed, kept as a pointer so
// that an explicit seed of 0 can be told from none.
type fileDoc struct {
	Config `yaml:",inline"`
	Seed   *int64 `yaml:"seed"`
}

// Load builds the configuration from args (without the program name) and env.
// It returns flag.ErrHelp for -h/--help; use Usage for the text. env may be
// nil.
func Load(args []string, env func(string) string) (Config, error) {
	if env == nil {
		env = func(string) string { return "" }
	}

	var fv flagValues
	fs := newFlagSet(&fv, defaults())
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return Config{}, err
		}
		return Config{}, fmt.Errorf("flags: %w", redact(err, fv.token, env))
	}
	if fs.NArg() > 0 {
		// Do not echo the argument: a mistyped "--token abc" would print it.
		return Config{}, fmt.Errorf("flags: unexpected positional argument (%d given); every option is a --flag", fs.NArg())
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	// defaults < YAML
	doc := fileDoc{Config: defaults()}
	if fv.configFile != "" {
		if err := readYAML(fv.configFile, &doc); err != nil {
			return Config{}, redact(err, fv.token, env)
		}
	}
	cfg := doc.Config
	cfg.ConfigFile = fv.configFile
	if cfg.ScenarioFile != "" {
		if len(cfg.Scenario.Targets) > 0 || !cfg.Scenario.Epoch.IsZero() {
			return Config{}, fmt.Errorf("config file %s: set scenario or scenarioFile, not both", fv.configFile)
		}
		path := cfg.ScenarioFile
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(fv.configFile), path)
		}
		if err := readScenario(path, &cfg.Scenario); err != nil {
			return Config{}, redact(err, fv.token, env)
		}
		cfg.ScenarioFile = path
	}
	if doc.Seed != nil {
		cfg.Seed = *doc.Seed
	}
	seedKnown := doc.Seed != nil

	// YAML < environment
	if v := env(EnvAddr); v != "" {
		cfg.Connection.Addr = v
	}
	if v := env(EnvFallbackToken); v != "" {
		cfg.Connection.Token = v
	}
	if v := env(EnvToken); v != "" {
		cfg.Connection.Token = v
	}

	// environment < flags (only those set explicitly)
	applyFlags(&cfg, &fv, set)
	if set["seed"] {
		seedKnown = true
	}
	if !seedKnown {
		cfg.Seed = time.Now().UnixNano()
		cfg.SeedGenerated = true
	}

	cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return Config{}, redact(err, fv.token, env)
	}
	return cfg, nil
}

func override[T any](set map[string]bool, name string, dst *T, v T) {
	if set[name] {
		*dst = v
	}
}

func overridePtr(set map[string]bool, name string, dst **float64, v float64) {
	if set[name] {
		*dst = &v
	}
}

func applyFlags(c *Config, fv *flagValues, s map[string]bool) {
	override(s, "addr", &c.Connection.Addr, fv.addr)
	override(s, "token", &c.Connection.Token, fv.token)

	// Choosing a transport on the command line replaces the YAML's choice, and
	// the TLS-only fields that went with it. Setting both flags to true is left
	// for Validate to reject.
	override(s, "insecure", &c.Connection.Insecure, fv.insecure)
	override(s, "tls", &c.Connection.TLS, fv.tls)
	insecureOnly := s["insecure"] && fv.insecure && !(s["tls"] && fv.tls)
	tlsOnly := s["tls"] && fv.tls && !(s["insecure"] && fv.insecure)
	if insecureOnly {
		c.Connection.TLS = false
		c.Connection.CA, c.Connection.Cert, c.Connection.Key = "", "", ""
	}
	if tlsOnly {
		c.Connection.Insecure = false
	}
	override(s, "ca", &c.Connection.CA, fv.ca)
	override(s, "cert", &c.Connection.Cert, fv.cert)
	override(s, "key", &c.Connection.Key, fv.key)

	e := &c.Sensor
	override(s, "kind", &e.Kind, fv.kind)
	override(s, "name", &e.Name, fv.name)
	override(s, "lat", &e.Lat, fv.lat)
	override(s, "lon", &e.Lon, fv.lon)
	// --alt and --height-above-ground each replace the YAML's choice of the
	// other, so a flag can switch the datum. Both on the command line is left
	// for Validate to reject.
	if s["alt"] && !s["height-above-ground"] {
		e.HeightAboveGroundM = nil
	}
	if s["height-above-ground"] && !s["alt"] {
		e.Alt = nil
	}
	overridePtr(s, "alt", &e.Alt, fv.alt)
	overridePtr(s, "height-above-ground", &e.HeightAboveGroundM, fv.height)
	override(s, "boresight", &e.Boresight, fv.boresight)
	override(s, "width", &e.Width, fv.width)
	override(s, "range-km", &e.RangeKm, fv.rangeKm)
	override(s, "min-range-km", &e.MinRangeKm, fv.minRangeKm)
	overridePtr(s, "elev-min", &e.ElevationMinDeg, fv.elevMin)
	overridePtr(s, "elev-max", &e.ElevationMaxDeg, fv.elevMax)
	override(s, "rate", &e.Rate, fv.rate)
	override(s, "pd", &e.Pd, fv.pd)
	override(s, "sigma-az", &e.SigmaAzDeg, fv.sigmaAz)
	override(s, "sigma-el", &e.SigmaElDeg, fv.sigmaEl)
	override(s, "sigma-range-m", &e.SigmaRangeM, fv.sigmaRng)
	override(s, "band", &e.Band, fv.band)
	override(s, "heartbeat-interval", &e.HeartbeatInterval, fv.heartbeatInterval)

	override(s, "verbose", &c.Verbose, fv.verbose)
	override(s, "seed", &c.Seed, fv.seed)
	override(s, "duration", &c.Duration, fv.duration)
	override(s, "terrain-cache", &c.Terrain.CacheDir, fv.terrainCache)
	override(s, "terrain-url", &c.Terrain.SourceURL, fv.terrainURL)
}

func readYAML(path string, doc *fileDoc) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("config file: %w", err)
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(doc); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("config file %s: %w", path, err)
	}
	return nil
}

// readScenario reads a scenario file: the scenario's own keys at the top level.
func readScenario(path string, s *scenario.Scenario) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("scenario file: %w", err)
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(s); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("scenario file %s: %w", path, err)
	}
	return nil
}

// redact removes every token candidate from an error message. The errors this
// package builds never contain the token; this covers messages that come from
// the flag and YAML libraries, which can quote a value.
func redact(err error, flagToken string, env func(string) string) error {
	msg := err.Error()
	redacted := false
	for _, tok := range []string{flagToken, env(EnvToken), env(EnvFallbackToken)} {
		if len(tok) >= 4 && strings.Contains(msg, tok) {
			msg = strings.ReplaceAll(msg, tok, "[redacted]")
			redacted = true
		}
	}
	if !redacted {
		return err
	}
	return errors.New(msg)
}

// normalize canonicalises the enum-like strings so that the rest of the
// program can compare them directly.
func (c *Config) normalize() {
	s := &c.Sensor
	s.Kind = strings.ToLower(strings.TrimSpace(s.Kind))
	s.Band = strings.ToUpper(strings.TrimSpace(s.Band))
	if s.Band == "" {
		s.Band = "VISIBLE"
	}
	if !math.IsNaN(s.Boresight) && !math.IsInf(s.Boresight, 0) {
		s.Boresight = math.Mod(math.Mod(s.Boresight, 360)+360, 360)
	}
	for i := range c.Scenario.Targets {
		t := &c.Scenario.Targets[i]
		t.Kind = strings.ToLower(strings.TrimSpace(t.Kind))
	}
}

func positive(x float64) bool    { return x > 0 && !math.IsInf(x, 0) } // false for NaN
func nonNegative(x float64) bool { return x >= 0 && !math.IsInf(x, 0) }
func finite(x float64) bool      { return !math.IsNaN(x) && !math.IsInf(x, 0) }

// Validate reports every problem it finds, joined into one error. It
// accepts enum strings in any case, so it also works on a hand-built Config.
func (c Config) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	c.validateConnection(add)
	c.validateSensor(add)
	if c.Duration < 0 {
		add("duration must be >= 0 (got %v)", c.Duration)
	}
	if err := c.Scenario.Validate(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (c Config) validateConnection(add func(string, ...any)) {
	n := c.Connection
	if n.Addr == "" {
		add("connection addr is required (--addr or %s)", EnvAddr)
	} else if host, port, err := net.SplitHostPort(n.Addr); err != nil || host == "" || port == "" {
		add("connection addr %q must be host:port", n.Addr)
	}
	if n.Token == "" {
		add("connection token is required (--token, %s or %s)", EnvToken, EnvFallbackToken)
	}
	switch {
	case n.Insecure && n.TLS:
		add("--insecure and --tls are mutually exclusive")
	case !n.Insecure && !n.TLS:
		add("one of --insecure and --tls is required")
	}
	if !n.TLS {
		if n.CA != "" {
			add("--ca requires --tls")
		}
		if n.Cert != "" || n.Key != "" {
			add("--cert and --key require --tls")
		}
	}
	if n.Cert != "" && n.Key == "" {
		add("--cert requires --key")
	}
	if n.Key != "" && n.Cert == "" {
		add("--key requires --cert")
	}
}

func (c Config) validateSensor(add func(string, ...any)) {
	s := c.Sensor
	kind := strings.ToLower(s.Kind)
	if kind != KindRadar && kind != KindEOIR {
		add("sensor kind (--kind) %q must be radar or eoir", s.Kind)
	}
	if strings.TrimSpace(s.Name) == "" {
		add("sensor name (--name) is required")
	}
	if s.Lat == 0 && s.Lon == 0 {
		add("sensor lat/lon are required (0,0 counts as unset)")
	}
	if !(s.Lat >= -90 && s.Lat <= 90) { // also false for NaN
		add("sensor lat must be in [-90,90] (got %v)", s.Lat)
	}
	if !(s.Lon >= -180 && s.Lon <= 180) {
		add("sensor lon must be in [-180,180] (got %v)", s.Lon)
	}

	terrainOn := c.Terrain.CacheDir != ""
	switch {
	case s.Alt != nil && s.HeightAboveGroundM != nil:
		add("--alt and --height-above-ground are mutually exclusive")
	case kind == KindEOIR && s.Alt == nil && s.HeightAboveGroundM == nil:
		add("an eoir sensor needs --alt or --height-above-ground")
	case kind == KindRadar && terrainOn && s.Alt == nil && s.HeightAboveGroundM == nil:
		// Targets sit on the real terrain, so a site left at 0 m would look
		// up at all of them.
		add("a radar with terrain needs --alt or --height-above-ground: without one the site is at 0 m, below the targets on the terrain")
	}
	if kind == KindEOIR && !terrainOn {
		// Without terrain the camera would see through hills, and the
		// targets would sit at 0 m instead of on the ground.
		add("an eoir sensor needs terrain (--terrain-cache dir): it decides what the camera can see")
	}
	if s.Alt != nil && !finite(*s.Alt) {
		add("sensor alt must be finite (got %v)", *s.Alt)
	}
	if h := s.HeightAboveGroundM; h != nil {
		if !(*h >= 0 && *h <= MaxHeightAboveGroundM) {
			add("sensor height-above-ground must be from 0 to %d metres (got %v)", MaxHeightAboveGroundM, *h)
		}
		if c.Terrain.CacheDir == "" {
			add("--height-above-ground needs terrain (--terrain-cache)")
		}
	}

	if !finite(s.Boresight) {
		add("sensor boresight must be finite (got %v)", s.Boresight)
	}
	if !(s.Width > 0 && s.Width <= 360) {
		add("sensor width must be in (0,360] degrees (got %v)", s.Width)
	}
	if !positive(s.RangeKm) {
		add("sensor range-km must be > 0 (got %v)", s.RangeKm)
	}
	if !nonNegative(s.MinRangeKm) || (positive(s.RangeKm) && s.MinRangeKm >= s.RangeKm) {
		add("sensor min-range-km must be >= 0 and below range-km (got %v)", s.MinRangeKm)
	}
	for name, v := range map[string]*float64{"elev-min": s.ElevationMinDeg, "elev-max": s.ElevationMaxDeg} {
		if v != nil && !(*v >= -90 && *v <= 90) {
			add("sensor %s must be in [-90,90] degrees (got %v)", name, *v)
		}
	}
	// The window the sensor will use, defaults included: --elev-min 80 alone
	// on a camera meets its default maximum of 75.
	if lo, hi := s.ElevationWindow(); !(lo < hi) {
		add("sensor elevation window %v..%v degrees is empty: elev-min must be below elev-max (a camera defaults to %v..%v, a radar to -90..90)",
			lo, hi, DefaultCameraElevMinDeg, DefaultCameraElevMaxDeg)
	}

	if !positive(s.Rate) {
		add("sensor rate must be > 0 (got %v)", s.Rate)
	} else if s.Rate > MaxRate {
		add("sensor rate must be <= %d Hz (got %v)", MaxRate, s.Rate)
	}
	if !(s.Pd > 0 && s.Pd <= 1) {
		add("sensor pd must be in (0,1] (got %v)", s.Pd)
	}
	if !positive(s.SigmaAzDeg) {
		add("sensor sigma-az must be > 0 (got %v)", s.SigmaAzDeg)
	}
	if !positive(s.SigmaElDeg) {
		add("sensor sigma-el must be > 0 (got %v)", s.SigmaElDeg)
	}
	if !positive(s.SigmaRangeM) {
		add("sensor sigma-range-m must be > 0 (got %v)", s.SigmaRangeM)
	}
	if hb := s.HeartbeatInterval; hb < time.Second || hb > MaxHeartbeatInterval || hb%time.Second != 0 {
		add("heartbeat interval (--heartbeat-interval) must be whole seconds from 1s to 300s (got %v)", hb)
	}
	if kind == KindEOIR {
		ok := false
		for _, b := range bands {
			ok = ok || strings.EqualFold(s.Band, b)
		}
		if !ok {
			add("sensor band %q must be one of %s", s.Band, strings.Join(bands, ", "))
		}
	}
}
