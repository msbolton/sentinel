// Package config loads and validates the ew-sensor configuration.
//
// Values come from built-in defaults, an optional YAML file (--config) and
// command-line flags, in increasing order of precedence. Two connection values
// also read the environment: the token (EW_SENSOR_TOKEN, then
// SENTINEL_INGEST_TOKEN) and the address (EW_SENSOR_ADDR), which sit between
// the flags and the YAML. A flag overrides only when it was set explicitly on
// the command line, so --rate 1 beats a YAML rate of 5 even though 1 is the
// default.
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
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Environment variables read by Load.
const (
	EnvToken         = "EW_SENSOR_TOKEN"
	EnvFallbackToken = "SENTINEL_INGEST_TOKEN"
	EnvAddr          = "EW_SENSOR_ADDR"
)

// Emplacement mobility, named after the proto Mobility enum.
const (
	MobilityFixed  = "FIXED"
	MobilityMobile = "MOBILE"
)

// Azimuth references, named after the proto AzimuthReference enum.
const (
	AzimuthTrueNorth = "TRUE_NORTH"
	AzimuthPlatform  = "PLATFORM"
)

// Ownship path kinds.
const (
	PathCircle    = "circle"
	PathWaypoints = "waypoints"
)

// Emitter motion kinds.
const (
	MotionStatic     = "static"
	MotionRandomWalk = "random-walk"
	MotionWaypoints  = "waypoints"
)

const defaultEmitterCount = 5

// Upper bounds, so a typo cannot become a zero tick interval or a runaway
// simulation.
const (
	// MaxRate is the largest --rate, in Hz.
	MaxRate = 100
	// MaxEmitters is the largest --emitters.
	MaxEmitters = 1000
	// DefaultHeartbeatInterval is how often the emplacement heartbeats by default.
	DefaultHeartbeatInterval = 5 * time.Second
	// MaxHeartbeatInterval is the longest --heartbeat-interval (the server's limit).
	MaxHeartbeatInterval = 300 * time.Second
)

// Config is everything the sensor needs to run. Load returns it normalised
// (enum strings upper-cased, defaults filled) and validated.
type Config struct {
	Connection  Connection  `yaml:"connection"`
	Emplacement Emplacement `yaml:"emplacement"`
	Sensor      Sensor      `yaml:"sensor"`
	// Ownship is the path a MOBILE sensor follows. It is only validated and
	// used when the emplacement is MOBILE. A circle is centred on the
	// emplacement's lat/lon.
	Ownship Ownship `yaml:"ownship"`
	// Emitters are the emitters listed in the YAML (after any truncation by
	// --emitters).
	Emitters []Emitter `yaml:"emitters"`
	// EmitterCount is the total number of emitters to simulate. It is
	// len(Emitters) unless --emitters says otherwise, or 5 when there is
	// no YAML list. When EmitterCount > len(Emitters), the sensor synthesises
	// the difference.
	EmitterCount int `yaml:"-"`
	// Duration is how long to run; 0 means until signalled.
	Duration time.Duration `yaml:"duration"`
	// Seed seeds every random draw. SeedGenerated is true when it was chosen
	// from the clock because none was given, so the caller can log it.
	Seed          int64 `yaml:"-"`
	SeedGenerated bool  `yaml:"-"`
	// Verbose logs every report as it is sent, and each ack.
	Verbose bool `yaml:"verbose"`
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

// Emplacement is the sensor site.
type Emplacement struct {
	// EmplacementID selects an existing emplacement. When empty the sensor
	// upserts one from the fields below.
	EmplacementID string  `yaml:"emplacementId"`
	Name          string  `yaml:"name"`
	Lat           float64 `yaml:"lat"`
	Lon           float64 `yaml:"lon"`
	Alt           float64 `yaml:"alt"`        // metres
	Boresight     float64 `yaml:"boresight"`  // degrees, [0,360)
	Width         float64 `yaml:"width"`      // azimuth width, degrees; 360 is omni
	RangeKm       float64 `yaml:"rangeKm"`    // maximum range
	MinRangeKm    float64 `yaml:"minRangeKm"` // blind zone
	// Mobility is MobilityFixed or MobilityMobile.
	Mobility string `yaml:"mobility"`
	// AzimuthReference is AzimuthTrueNorth or AzimuthPlatform (boresight
	// relative to the ship's heading; MOBILE only).
	AzimuthReference string `yaml:"azimuthReference"`
}

// Sensor holds the detection model settings.
type Sensor struct {
	Rate   float64 `yaml:"rate"`   // ticks per second
	Pd     float64 `yaml:"pd"`     // probability of detection per emitter per tick, (0,1]
	SigmaM float64 `yaml:"sigmaM"` // 1-sigma position error at the reference range, metres
	// Classification is UNCLASSIFIED, CONFIDENTIAL, SECRET or TOP_SECRET, or
	// empty to let the server apply its floor.
	Classification string `yaml:"classification"`
	// SendTimestamps sets source_timestamp (and EmplacementState.time).
	// Off by default: the server refuses clocks skewed beyond +5 s / -60 s.
	SendTimestamps bool `yaml:"sendTimestamps"`
	// HeartbeatInterval is how often the emplacement sends its EmplacementState
	// heartbeat, and the interval it declares (heartbeat_interval_sec): whole
	// seconds, 1s to 300s. The server calls it offline after three missed ones.
	HeartbeatInterval time.Duration `yaml:"heartbeatInterval"`
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

// Ownship is the path a mobile sensor follows.
type Ownship struct {
	Path      string   `yaml:"path"` // PathCircle or PathWaypoints
	SpeedKts  float64  `yaml:"speedKts"`
	RadiusKm  float64  `yaml:"radiusKm"` // circle only
	Waypoints []LatLon `yaml:"waypoints"`
}

// LatLon is a geographic point in degrees.
type LatLon struct {
	Lat float64 `yaml:"lat"`
	Lon float64 `yaml:"lon"`
}

// Emitter is one simulated RF emitter. It is plain data; the emitter package
// turns it into behaviour.
type Emitter struct {
	ID               string   `yaml:"id"` // observation id is "EMITTER-<id>"
	DisplayName      string   `yaml:"displayName"`
	EmitterType      string   `yaml:"emitterType"` // e.g. RADAR_SEARCH, RADAR_TRACK, SATCOM, JAMMER
	FrequencyHz      float64  `yaml:"frequencyHz"`
	BandwidthHz      float64  `yaml:"bandwidthHz"`      // 0 means unset
	PriUs            float64  `yaml:"priUs"`            // 0 means unset
	PulseWidthUs     float64  `yaml:"pulseWidthUs"`     // 0 means unset
	Modulation       string   `yaml:"modulation"`       // e.g. PULSE, CW, FHSS
	ScanType         string   `yaml:"scanType"`         // e.g. CIRCULAR, SECTOR, FIXED
	PowerDbm         *float64 `yaml:"powerDbm"`         // nil means unset (0 dBm is a value)
	Affiliation      string   `yaml:"affiliation"`      // FRIENDLY, HOSTILE, ... ; empty means unspecified
	TrackEnvironment string   `yaml:"trackEnvironment"` // AIR, SEA_SURFACE, ...; default GROUND
	Start            LatLon   `yaml:"start"`
	Motion           Motion   `yaml:"motion"`
}

// Motion says how an emitter moves. Type is MotionStatic (the default),
// MotionRandomWalk or MotionWaypoints. The last two need SpeedKts > 0, and
// MotionWaypoints needs at least one waypoint.
type Motion struct {
	Type      string   `yaml:"type"`
	SpeedKts  float64  `yaml:"speedKts"`
	Waypoints []LatLon `yaml:"waypoints"`
}

// Mobile reports whether the sensor platform moves.
func (c Config) Mobile() bool {
	return strings.EqualFold(c.Emplacement.Mobility, MobilityMobile)
}

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
		Emplacement: Emplacement{
			Name:             "ew-sensor",
			Width:            120,
			RangeKm:          120,
			Mobility:         MobilityFixed,
			AzimuthReference: AzimuthTrueNorth,
		},
		Sensor:       Sensor{Rate: 1, Pd: 0.9, SigmaM: 250, HeartbeatInterval: DefaultHeartbeatInterval},
		Ownship:      Ownship{Path: PathCircle, SpeedKts: 12, RadiusKm: 5},
		EmitterCount: defaultEmitterCount,
	}
}

// flagValues receives the parsed flags. Which of them the user actually set
// is read from FlagSet.Visit, never inferred from the values.
type flagValues struct {
	configFile                 string
	addr, token                string
	insecure, tls              bool
	ca, cert, key              string
	emplacementID, name        string
	lat, lon, alt, boresight   float64
	width, rangeKm, minRangeKm float64
	emitters                   int
	rate, pd, sigmaM           float64
	seed                       int64
	duration                   time.Duration
	mobile                     bool
	path                       string
	speedKts                   float64
	sendTimestamps             bool
	heartbeatInterval          time.Duration
	verbose                    bool
}

func newFlagSet(fv *flagValues, d Config) *flag.FlagSet {
	fs := flag.NewFlagSet("ew-sensor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.StringVar(&fv.configFile, "config", "", "YAML `file` with the connection, emplacement, sensor, ownship and emitters; flags override it")

	fs.StringVar(&fv.addr, "addr", "", "ingest service `host:port` (env "+EnvAddr+")")
	fs.StringVar(&fv.token, "token", "", "ingest token (env "+EnvToken+" or "+EnvFallbackToken+")")
	fs.BoolVar(&fv.insecure, "insecure", false, "plaintext h2c; the token travels in clear (exactly one of --insecure and --tls)")
	fs.BoolVar(&fv.tls, "tls", false, "use TLS (exactly one of --insecure and --tls)")
	fs.StringVar(&fv.ca, "ca", "", "extra root CA `file` (requires --tls)")
	fs.StringVar(&fv.cert, "cert", "", "client certificate `file` (requires --tls and --key)")
	fs.StringVar(&fv.key, "key", "", "client key `file` (requires --tls and --cert)")

	e := d.Emplacement
	fs.StringVar(&fv.emplacementID, "emplacement-id", "", "use this existing emplacement; else one is upserted from the flags below")
	fs.StringVar(&fv.name, "name", e.Name, "emplacement name")
	fs.Float64Var(&fv.lat, "lat", 0, "sensor latitude, degrees")
	fs.Float64Var(&fv.lon, "lon", 0, "sensor longitude, degrees")
	fs.Float64Var(&fv.alt, "alt", e.Alt, "sensor altitude, metres")
	fs.Float64Var(&fv.boresight, "boresight", e.Boresight, "boresight azimuth, degrees")
	fs.Float64Var(&fv.width, "width", e.Width, "azimuth coverage width, degrees (360 is omni)")
	fs.Float64Var(&fv.rangeKm, "range-km", e.RangeKm, "maximum range, km")
	fs.Float64Var(&fv.minRangeKm, "min-range-km", e.MinRangeKm, "minimum range, km")

	fs.IntVar(&fv.emitters, "emitters", d.EmitterCount, "number of emitters; extra ones are synthesised, surplus YAML ones dropped")
	fs.Float64Var(&fv.rate, "rate", d.Sensor.Rate, "ticks per second")
	fs.Float64Var(&fv.pd, "pd", d.Sensor.Pd, "probability of detection, (0,1]")
	fs.Float64Var(&fv.sigmaM, "sigma-m", d.Sensor.SigmaM, "position error 1-sigma at the reference range, metres")
	fs.Int64Var(&fv.seed, "seed", 0, "random seed (default: from the clock)")
	fs.DurationVar(&fv.duration, "duration", 0, "run time, e.g. 5m; 0 runs until signalled")
	fs.BoolVar(&fv.mobile, "mobile", false, "the sensor moves along an ownship path")
	fs.StringVar(&fv.path, "path", d.Ownship.Path, "ownship path: circle or waypoints")
	fs.Float64Var(&fv.speedKts, "speed-kts", d.Ownship.SpeedKts, "ownship speed, knots")
	fs.BoolVar(&fv.sendTimestamps, "send-timestamps", false, "set source timestamps (the server refuses clocks skewed beyond +5s/-60s)")
	fs.DurationVar(&fv.heartbeatInterval, "heartbeat-interval", d.Sensor.HeartbeatInterval, "how often the emplacement heartbeats (EmplacementState), whole seconds from 1s to 300s; the server calls it offline after three missed")
	fs.BoolVar(&fv.verbose, "verbose", false, "log every report as JSON as it is sent, and each ack")
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

	// --emitters N: the total. Without it, a YAML list sets the count.
	switch {
	case set["emitters"]:
		if cfg.EmitterCount >= 0 && len(cfg.Emitters) > cfg.EmitterCount {
			cfg.Emitters = cfg.Emitters[:cfg.EmitterCount]
		}
	case len(cfg.Emitters) > 0:
		cfg.EmitterCount = len(cfg.Emitters)
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

	e := &c.Emplacement
	override(s, "emplacement-id", &e.EmplacementID, fv.emplacementID)
	override(s, "name", &e.Name, fv.name)
	override(s, "lat", &e.Lat, fv.lat)
	override(s, "lon", &e.Lon, fv.lon)
	override(s, "alt", &e.Alt, fv.alt)
	override(s, "boresight", &e.Boresight, fv.boresight)
	override(s, "width", &e.Width, fv.width)
	override(s, "range-km", &e.RangeKm, fv.rangeKm)
	override(s, "min-range-km", &e.MinRangeKm, fv.minRangeKm)
	if s["mobile"] {
		e.Mobility = MobilityFixed
		if fv.mobile {
			e.Mobility = MobilityMobile
		}
	}

	override(s, "emitters", &c.EmitterCount, fv.emitters)
	override(s, "rate", &c.Sensor.Rate, fv.rate)
	override(s, "pd", &c.Sensor.Pd, fv.pd)
	override(s, "sigma-m", &c.Sensor.SigmaM, fv.sigmaM)
	override(s, "send-timestamps", &c.Sensor.SendTimestamps, fv.sendTimestamps)
	override(s, "heartbeat-interval", &c.Sensor.HeartbeatInterval, fv.heartbeatInterval)
	override(s, "verbose", &c.Verbose, fv.verbose)
	override(s, "seed", &c.Seed, fv.seed)
	override(s, "duration", &c.Duration, fv.duration)
	override(s, "path", &c.Ownship.Path, fv.path)
	override(s, "speed-kts", &c.Ownship.SpeedKts, fv.speedKts)
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
	up := strings.ToUpper
	e := &c.Emplacement
	e.Mobility = up(strings.TrimSpace(e.Mobility))
	if e.Mobility == "" {
		e.Mobility = MobilityFixed
	}
	e.AzimuthReference = up(strings.TrimSpace(e.AzimuthReference))
	if e.AzimuthReference == "" {
		e.AzimuthReference = AzimuthTrueNorth
	}
	if !math.IsNaN(e.Boresight) && !math.IsInf(e.Boresight, 0) {
		e.Boresight = math.Mod(math.Mod(e.Boresight, 360)+360, 360)
	}
	c.Sensor.Classification = up(strings.TrimSpace(c.Sensor.Classification))
	c.Ownship.Path = strings.ToLower(strings.TrimSpace(c.Ownship.Path))
	for i := range c.Emitters {
		em := &c.Emitters[i]
		em.Affiliation = up(strings.TrimSpace(em.Affiliation))
		em.TrackEnvironment = up(strings.TrimSpace(em.TrackEnvironment))
		switch em.TrackEnvironment {
		case "", "LAND":
			em.TrackEnvironment = "GROUND"
		}
		em.Motion.Type = strings.ToLower(strings.TrimSpace(em.Motion.Type))
		if em.Motion.Type == "" {
			em.Motion.Type = MotionStatic
		}
	}
}

var (
	classifications = []string{"UNCLASSIFIED", "CONFIDENTIAL", "SECRET", "TOP_SECRET"}
	affiliations    = []string{"FRIENDLY", "HOSTILE", "NEUTRAL", "UNKNOWN", "ASSUMED_FRIENDLY", "SUSPECT", "PENDING"}
	environments    = []string{"AIR", "SEA_SURFACE", "SUBSURFACE", "GROUND", "SPACE", "UNKNOWN"}
)

func oneOf(v string, allowed []string) bool {
	for _, a := range allowed {
		if strings.EqualFold(v, a) {
			return true
		}
	}
	return false
}

func positive(x float64) bool    { return x > 0 && !math.IsInf(x, 0) } // false for NaN
func nonNegative(x float64) bool { return x >= 0 && !math.IsInf(x, 0) }
func validLat(x float64) bool    { return x >= -90 && x <= 90 }
func validLon(x float64) bool    { return x >= -180 && x <= 180 }

// Validate reports every problem it finds, joined into one error. It
// accepts enum strings in any case, so it also works on a hand-built Config.
func (c Config) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	c.validateConnection(add)
	c.validateEmplacement(add)

	s := c.Sensor
	if !positive(s.Rate) {
		add("rate must be > 0 (got %v)", s.Rate)
	} else if s.Rate > MaxRate {
		add("rate must be <= %d Hz (got %v)", MaxRate, s.Rate)
	}
	if !(s.Pd > 0 && s.Pd <= 1) {
		add("pd must be in (0,1] (got %v)", s.Pd)
	}
	if !positive(s.SigmaM) {
		add("sigmaM (--sigma-m) must be > 0 (got %v)", s.SigmaM)
	}
	if hb := s.HeartbeatInterval; hb < time.Second || hb > MaxHeartbeatInterval || hb%time.Second != 0 {
		add("heartbeat interval (--heartbeat-interval) must be whole seconds from 1s to 300s (got %v)", hb)
	}
	if s.Classification != "" && !oneOf(s.Classification, classifications) {
		add("sensor classification %q must be one of %s", s.Classification, strings.Join(classifications, ", "))
	}

	if c.Duration < 0 {
		add("duration must be >= 0 (got %v)", c.Duration)
	}
	if c.EmitterCount < 0 {
		add("emitters must be >= 0 (got %d)", c.EmitterCount)
	} else if c.EmitterCount == 0 && len(c.Emitters) == 0 {
		add("at least one emitter is required")
	}
	if n := max(c.EmitterCount, len(c.Emitters)); n > MaxEmitters {
		add("emitters must be <= %d (got %d)", MaxEmitters, n)
	}

	if c.Mobile() {
		c.validateOwnship(add)
	}
	c.validateEmitters(add)
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

func (c Config) validateEmplacement(add func(string, ...any)) {
	e := c.Emplacement
	if e.EmplacementID == "" {
		if strings.TrimSpace(e.Name) == "" {
			add("emplacement name is required without --emplacement-id")
		}
		if e.Lat == 0 && e.Lon == 0 {
			add("emplacement lat/lon are required without --emplacement-id (0,0 counts as unset)")
		}
	}
	if !validLat(e.Lat) { // also false for NaN
		add("emplacement lat must be in [-90,90] (got %v)", e.Lat)
	}
	if !validLon(e.Lon) {
		add("emplacement lon must be in [-180,180] (got %v)", e.Lon)
	}
	if math.IsNaN(e.Alt) || math.IsInf(e.Alt, 0) {
		add("emplacement alt must be finite (got %v)", e.Alt)
	}
	if math.IsNaN(e.Boresight) || math.IsInf(e.Boresight, 0) {
		add("emplacement boresight must be finite (got %v)", e.Boresight)
	}
	if !(e.Width > 0 && e.Width <= 360) {
		add("emplacement width must be in (0,360] degrees (got %v)", e.Width)
	}
	if !positive(e.RangeKm) {
		add("emplacement rangeKm (--range-km) must be > 0 (got %v)", e.RangeKm)
	}
	if !nonNegative(e.MinRangeKm) || (positive(e.RangeKm) && e.MinRangeKm >= e.RangeKm) {
		add("emplacement minRangeKm (--min-range-km) must be >= 0 and below rangeKm (got %v)", e.MinRangeKm)
	}
	mob := e.Mobility
	if mob == "" {
		mob = MobilityFixed
	}
	if !oneOf(mob, []string{MobilityFixed, MobilityMobile}) {
		add("emplacement mobility %q must be FIXED or MOBILE", e.Mobility)
	}
	az := e.AzimuthReference
	if az == "" {
		az = AzimuthTrueNorth
	}
	if !oneOf(az, []string{AzimuthTrueNorth, AzimuthPlatform}) {
		add("emplacement azimuthReference %q must be TRUE_NORTH or PLATFORM", e.AzimuthReference)
	} else if strings.EqualFold(az, AzimuthPlatform) && !c.Mobile() {
		add("emplacement azimuthReference PLATFORM needs a MOBILE emplacement (--mobile)")
	}
}

func (c Config) validateOwnship(add func(string, ...any)) {
	o := c.Ownship
	if !positive(o.SpeedKts) {
		add("ownship speedKts (--speed-kts) must be > 0 (got %v)", o.SpeedKts)
	}
	switch strings.ToLower(o.Path) {
	case PathCircle:
		if !positive(o.RadiusKm) {
			add("ownship radiusKm must be > 0 for a circle path (got %v)", o.RadiusKm)
		}
	case PathWaypoints:
		if len(o.Waypoints) < 2 {
			add("ownship path waypoints needs at least 2 waypoints (got %d)", len(o.Waypoints))
		}
		for i, w := range o.Waypoints {
			if !validLat(w.Lat) || !validLon(w.Lon) {
				add("ownship waypoint %d is not a valid lat/lon", i)
			}
		}
	default:
		add("ownship path (--path) %q must be circle or waypoints", o.Path)
	}
}

func (c Config) validateEmitters(add func(string, ...any)) {
	seen := map[string]bool{}
	for i, e := range c.Emitters {
		who := fmt.Sprintf("emitter %d", i)
		if e.ID != "" {
			who = fmt.Sprintf("emitter %q", e.ID)
		}
		switch {
		case strings.TrimSpace(e.ID) == "":
			add("%s: id is required", who)
		case seen[e.ID]:
			add("%s: duplicate id", who)
		}
		seen[e.ID] = true
		if strings.TrimSpace(e.EmitterType) == "" {
			add("%s: emitterType is required", who)
		}
		if !positive(e.FrequencyHz) {
			add("%s: frequencyHz must be > 0 (got %v)", who, e.FrequencyHz)
		}
		for name, v := range map[string]float64{"bandwidthHz": e.BandwidthHz, "priUs": e.PriUs, "pulseWidthUs": e.PulseWidthUs} {
			if !nonNegative(v) {
				add("%s: %s must be >= 0 (got %v)", who, name, v)
			}
		}
		if e.PowerDbm != nil && (math.IsNaN(*e.PowerDbm) || math.IsInf(*e.PowerDbm, 0)) {
			add("%s: powerDbm must be finite", who)
		}
		if e.Affiliation != "" && !oneOf(e.Affiliation, affiliations) {
			add("%s: affiliation %q must be one of %s", who, e.Affiliation, strings.Join(affiliations, ", "))
		}
		if e.TrackEnvironment != "" && !strings.EqualFold(e.TrackEnvironment, "LAND") && !oneOf(e.TrackEnvironment, environments) {
			add("%s: trackEnvironment %q must be one of %s", who, e.TrackEnvironment, strings.Join(environments, ", "))
		}
		if !validLat(e.Start.Lat) || !validLon(e.Start.Lon) {
			add("%s: start is not a valid lat/lon", who)
		}
		switch m := e.Motion; strings.ToLower(m.Type) {
		case "", MotionStatic:
		case MotionRandomWalk:
			if !positive(m.SpeedKts) {
				add("%s: motion random-walk needs speedKts > 0", who)
			}
		case MotionWaypoints:
			if !positive(m.SpeedKts) {
				add("%s: motion waypoints needs speedKts > 0", who)
			}
			if len(m.Waypoints) == 0 {
				add("%s: motion waypoints needs a non-empty waypoints list", who)
			}
			for j, w := range m.Waypoints {
				if !validLat(w.Lat) || !validLon(w.Lon) {
					add("%s: motion waypoint %d is not a valid lat/lon", who, j)
				}
			}
		default:
			add("%s: motion type %q must be static, random-walk or waypoints", who, m.Type)
		}
	}
}
