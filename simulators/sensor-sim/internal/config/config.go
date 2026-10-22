// Package config loads and validates the sensor-sim configuration.
//
// Values come from built-in defaults (which depend on the kind), an optional
// YAML file (--config) and command-line flags, in increasing order of
// precedence. Two connection values also read the environment: the token
// (SENSOR_SIM_TOKEN, then ew-sensor's deprecated EW_SENSOR_TOKEN, then
// SENTINEL_INGEST_TOKEN) and the address (SENSOR_SIM_ADDR, then the deprecated
// EW_SENSOR_ADDR), which sit between the flags and the YAML. The deprecated
// names outrank SENTINEL_INGEST_TOKEN because Compose gives esm-sim its own
// env file (a simulator's own variables beat a generic one that a shared file
// may also set). A flag overrides
// only when it was set explicitly on the command line, so --rate 1 beats a
// YAML rate of 5 even though 1 is the default.
//
// One process simulates one sensor (--kind): a radar or an EO/IR camera
// watching a scenario of targets, or an ESM sensor geolocating RF emitters.
// The scenario is YAML only and is meant to be shared by the radar and camera
// processes through one file; the ESM sensor's emitters and ownship path are
// YAML only too.
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
	// EnvDeprecatedToken and EnvDeprecatedAddr are ew-sensor's names, read
	// below EnvToken and EnvAddr but above EnvFallbackToken, so an existing
	// secrets/ew-sensor.env keeps working for esm-sim. Load warns
	// (Config.Warnings) whenever one is set.
	EnvDeprecatedToken = "EW_SENSOR_TOKEN"
	EnvDeprecatedAddr  = "EW_SENSOR_ADDR"
)

// Sensor kinds.
const (
	KindRadar = "radar"
	KindEOIR  = "eoir"
	// KindESM is an electronic-support (ESM) sensor: it geolocates simulated
	// RF emitters and reports fixes with an error ellipse.
	KindESM = "esm"
)

// Emplacement mobility, named after the proto Mobility enum. Only an ESM
// sensor can be MOBILE.
const (
	MobilityFixed  = "FIXED"
	MobilityMobile = "MOBILE"
)

// Azimuth references, named after the proto AzimuthReference enum. PLATFORM
// (the boresight measured from the platform's heading) needs a MOBILE sensor.
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

// DefaultESMName is the emplacement name of an ESM sensor given none.
const DefaultESMName = "esm-sim"

const defaultEmitterCount = 5

// EO/IR bands, named after the proto EoIrBand enum.
var bands = []string{"VISIBLE", "SWIR", "MWIR", "LWIR"}

// DefaultTerrainURL is where terrain tiles come from when none is given.
const DefaultTerrainURL = "https://copernicus-dem-30m.s3.amazonaws.com"

const (
	// MaxRate is the largest --rate, in Hz.
	MaxRate = 100
	// MaxEmitters is the largest --emitters.
	MaxEmitters = 1000
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
	// Ownship is the path a MOBILE (ESM) sensor follows. It is only
	// validated and used when the sensor is MOBILE. A circle is centred on
	// the sensor's lat/lon.
	Ownship Ownship `yaml:"ownship"`
	// Emitters are the ESM sensor's emitters listed in the YAML (after any
	// truncation by --emitters).
	Emitters []Emitter `yaml:"emitters"`
	// EmitterCount is the total number of emitters an ESM sensor simulates.
	// It is len(Emitters) unless --emitters says otherwise, or 5 when there
	// is no YAML list; the sensor synthesises the difference. It is 0 for
	// the other kinds.
	EmitterCount int `yaml:"-"`
	// ConfigFile is the --config path, or empty.
	ConfigFile string `yaml:"-"`
	// Warnings are problems that do not stop the run (a deprecated
	// environment variable), for the caller to log once.
	Warnings []string `yaml:"-"`
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
	// Kind is KindRadar, KindEOIR or KindESM.
	Kind string `yaml:"kind"`
	// EmplacementID makes an ESM sensor report as an existing emplacement
	// bound to the connection, with the server's geometry, instead of
	// upserting one from the fields below.
	EmplacementID string  `yaml:"emplacementId"`
	Name          string  `yaml:"name"`
	Lat           float64 `yaml:"lat"`
	Lon           float64 `yaml:"lon"`
	// Alt is the site altitude in metres above the WGS84 ellipsoid (0 when
	// unset for a radar without terrain or an ESM sensor).
	// HeightAboveGroundM places the site that far above the terrain instead
	// (it needs terrain; radar and camera only). An EO/IR camera needs one of
	// them; at most one may be set.
	Alt                *float64 `yaml:"alt"`
	HeightAboveGroundM *float64 `yaml:"heightAboveGroundM"`
	// Mobility is MobilityFixed or MobilityMobile (ESM only).
	Mobility string `yaml:"mobility"`
	// AzimuthReference is AzimuthTrueNorth or AzimuthPlatform (boresight
	// relative to the platform's heading; a MOBILE ESM sensor only).
	AzimuthReference string `yaml:"azimuthReference"`

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
	// Measurement noise, 1-sigma. SigmaRangeM is unused by a camera; the
	// three are unused by an ESM sensor, whose fixes use SigmaM.
	SigmaAzDeg  float64 `yaml:"sigmaAzDeg"`
	SigmaElDeg  float64 `yaml:"sigmaElDeg"`
	SigmaRangeM float64 `yaml:"sigmaRangeM"`
	// SigmaM is an ESM fix's 1-sigma position error at the reference range
	// (10 km), metres; the cross-range error grows with range beyond it.
	SigmaM float64 `yaml:"sigmaM"`
	// Classification is UNCLASSIFIED, CONFIDENTIAL, SECRET or TOP_SECRET, or
	// empty to let the server apply its floor (ESM only).
	Classification string `yaml:"classification"`
	// SendTimestamps sets source_timestamp (and EmplacementState.time) (ESM
	// only). Off by default: the server refuses clocks skewed beyond +5 s /
	// -60 s.
	SendTimestamps bool `yaml:"sendTimestamps"`
	// HeartbeatInterval is how often the emplacement sends its EmplacementState
	// heartbeat, and the interval it declares: whole seconds, 1s to 300s.
	HeartbeatInterval time.Duration `yaml:"heartbeatInterval"`
	// Band is the EO/IR band: VISIBLE, SWIR, MWIR or LWIR.
	Band string `yaml:"band"`
}

// Ownship is the path a mobile ESM sensor follows.
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

// Mobile reports whether the sensor platform moves (a MOBILE ESM sensor).
func (c Config) Mobile() bool {
	return strings.EqualFold(c.Sensor.Mobility, MobilityMobile)
}

// ESM reports whether the sensor is an ESM sensor.
func (c Config) ESM() bool { return strings.EqualFold(c.Sensor.Kind, KindESM) }

// AltM is the configured site altitude, 0 when none is set.
func (s Sensor) AltM() float64 {
	if s.Alt == nil {
		return 0
	}
	return *s.Alt
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

// defaults are the built-in values for a sensor of the given kind: an ESM
// sensor gets a 120 degree, 120 km sector and five emitters.
func defaults(kind string) Config {
	c := Config{
		Sensor: Sensor{
			Name: "sensor-sim", Width: 90, RangeKm: 10,
			Rate: 1, Pd: 0.9, SigmaAzDeg: 0.5, SigmaElDeg: 0.5, SigmaRangeM: 10, SigmaM: 250,
			HeartbeatInterval: DefaultHeartbeatInterval, Band: "VISIBLE",
			Mobility: MobilityFixed, AzimuthReference: AzimuthTrueNorth,
		},
		Terrain: TerrainConfig{SourceURL: DefaultTerrainURL},
		Ownship: Ownship{Path: PathCircle, SpeedKts: 12, RadiusKm: 5},
	}
	if strings.EqualFold(strings.TrimSpace(kind), KindESM) {
		c.Sensor.Name, c.Sensor.Width, c.Sensor.RangeKm = DefaultESMName, 120, 120
		c.EmitterCount = defaultEmitterCount
	}
	return c
}

// flagValues receives the parsed flags. Which of them the user actually set
// is read from FlagSet.Visit, never inferred from the values.
type flagValues struct {
	configFile                 string
	addr, token                string
	insecure, tls              bool
	ca, cert, key              string
	kind, name                 string
	emplacementID              string
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
	// ESM
	emitters       int
	sigmaM         float64
	mobile         bool
	path           string
	speedKts       float64
	sendTimestamps bool
}

func newFlagSet(fv *flagValues, d Config) *flag.FlagSet {
	fs := flag.NewFlagSet("sensor-sim", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.StringVar(&fv.configFile, "config", "", "YAML `file` with the connection, sensor, scenario and terrain; flags override it")

	fs.StringVar(&fv.addr, "addr", "", "ingest service `host:port` (env "+EnvAddr+")")
	fs.StringVar(&fv.token, "token", "", "ingest token (env "+EnvToken+", else "+EnvDeprecatedToken+" or "+EnvFallbackToken+")")
	fs.BoolVar(&fv.insecure, "insecure", false, "plaintext h2c; the token travels in clear (exactly one of --insecure and --tls)")
	fs.BoolVar(&fv.tls, "tls", false, "use TLS (exactly one of --insecure and --tls)")
	fs.StringVar(&fv.ca, "ca", "", "extra root CA `file` (requires --tls)")
	fs.StringVar(&fv.cert, "cert", "", "client certificate `file` (requires --tls and --key)")
	fs.StringVar(&fv.key, "key", "", "client key `file` (requires --tls and --cert)")

	s := d.Sensor
	fs.StringVar(&fv.kind, "kind", "", "sensor kind: radar, eoir or esm (required)")
	fs.StringVar(&fv.emplacementID, "emplacement-id", "", "esm: report as this existing emplacement, with the server's geometry; else one is upserted from the flags below")
	fs.StringVar(&fv.name, "name", s.Name, "sensor name (the emplacement name); esm defaults to "+DefaultESMName)
	fs.Float64Var(&fv.lat, "lat", 0, "sensor latitude, degrees")
	fs.Float64Var(&fv.lon, "lon", 0, "sensor longitude, degrees")
	fs.Float64Var(&fv.alt, "alt", 0, "sensor altitude, metres above the WGS84 ellipsoid (an EO/IR camera needs --alt or --height-above-ground)")
	fs.Float64Var(&fv.height, "height-above-ground", 0, "sensor height above the terrain, metres (radar and eoir; needs --terrain-cache; instead of --alt)")
	fs.Float64Var(&fv.boresight, "boresight", s.Boresight, "boresight azimuth, degrees")
	fs.Float64Var(&fv.width, "width", s.Width, "azimuth coverage width, degrees (360 is omni); esm defaults to 120")
	fs.Float64Var(&fv.rangeKm, "range-km", s.RangeKm, "maximum range, km; esm defaults to 120")
	fs.Float64Var(&fv.minRangeKm, "min-range-km", s.MinRangeKm, "minimum range, km")
	fs.Float64Var(&fv.elevMin, "elev-min", 0, "lowest elevation covered, degrees (radar and eoir)")
	fs.Float64Var(&fv.elevMax, "elev-max", 0, "highest elevation covered, degrees (radar and eoir)")

	fs.Float64Var(&fv.rate, "rate", s.Rate, "scans (esm: ticks) per second")
	fs.Float64Var(&fv.pd, "pd", s.Pd, "probability of detection, (0,1]")
	fs.Float64Var(&fv.sigmaAz, "sigma-az", s.SigmaAzDeg, "azimuth noise 1-sigma, degrees")
	fs.Float64Var(&fv.sigmaEl, "sigma-el", s.SigmaElDeg, "elevation noise 1-sigma, degrees")
	fs.Float64Var(&fv.sigmaRng, "sigma-range-m", s.SigmaRangeM, "range noise 1-sigma, metres (radar)")
	fs.Float64Var(&fv.sigmaM, "sigma-m", s.SigmaM, "esm: fix position error 1-sigma at the 10 km reference range, metres")
	fs.StringVar(&fv.band, "band", s.Band, "EO/IR band: VISIBLE, SWIR, MWIR or LWIR")
	fs.IntVar(&fv.emitters, "emitters", defaultEmitterCount, "esm: number of emitters; extra ones are synthesised, surplus YAML ones dropped (default: the YAML list, else 5)")
	fs.BoolVar(&fv.mobile, "mobile", false, "esm: the sensor moves along an ownship path (MOBILE emplacement)")
	fs.StringVar(&fv.path, "path", d.Ownship.Path, "esm: ownship path, circle or waypoints (the waypoints come from the YAML)")
	fs.Float64Var(&fv.speedKts, "speed-kts", d.Ownship.SpeedKts, "esm: ownship speed, knots")
	fs.BoolVar(&fv.sendTimestamps, "send-timestamps", false, "esm: set source timestamps (the server refuses clocks skewed beyond +5s/-60s)")
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
	fs := newFlagSet(&fv, defaults(""))
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
	fs := newFlagSet(&fv, defaults(""))
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

	// defaults < YAML. The defaults depend on the kind, which the flag or
	// else the YAML gives.
	kind := fv.kind
	if !set["kind"] && fv.configFile != "" {
		kind = peekKind(fv.configFile)
	}
	doc := fileDoc{Config: defaults(kind)}
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

	// YAML < environment < flags (only those set explicitly). Within the
	// environment the lowest-precedence name comes first: the generic
	// SENTINEL_INGEST_TOKEN, then the deprecated ew-sensor names, then the
	// SENSOR_SIM_* names.
	addrFrom := setFromEnv(env, &cfg.Connection.Addr, EnvDeprecatedAddr, EnvAddr)
	tokenFrom := setFromEnv(env, &cfg.Connection.Token, EnvFallbackToken, EnvDeprecatedToken, EnvToken)
	applyFlags(&cfg, &fv, set)
	if set["addr"] {
		addrFrom = "--addr"
	}
	if set["token"] {
		tokenFrom = "--token"
	}
	cfg.Warnings = append(cfg.Warnings,
		deprecationWarning(env, EnvDeprecatedAddr, EnvAddr, addrFrom)...)
	cfg.Warnings = append(cfg.Warnings,
		deprecationWarning(env, EnvDeprecatedToken, EnvToken, tokenFrom)...)
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

// setFromEnv sets dst from the first of names, in increasing precedence, that
// is set in the environment, and returns the one it used ("" for none).
func setFromEnv(env func(string) string, dst *string, names ...string) string {
	from := ""
	for _, n := range names {
		if v := env(n); v != "" {
			*dst, from = v, n
		}
	}
	return from
}

// deprecationWarning is the warning for a deprecated variable that is set:
// whether it was used, or which source beat it. It never includes the value.
func deprecationWarning(env func(string) string, deprecated, replacement, usedFrom string) []string {
	if env(deprecated) == "" {
		return nil
	}
	if usedFrom == deprecated {
		return []string{fmt.Sprintf("%s is deprecated (ew-sensor's name) but still used: rename it %s", deprecated, replacement)}
	}
	return []string{fmt.Sprintf("%s is deprecated (ew-sensor's name) and ignored: %s takes precedence; remove it or rename it %s", deprecated, usedFrom, replacement)}
}

// peekKind returns the sensor kind a YAML file sets, or "" when it sets none
// or cannot be read (the full decode reports that).
func peekKind(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var doc struct {
		Sensor struct {
			Kind string `yaml:"kind"`
		} `yaml:"sensor"`
	}
	if yaml.Unmarshal(b, &doc) != nil {
		return ""
	}
	return doc.Sensor.Kind
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
	override(s, "emplacement-id", &e.EmplacementID, fv.emplacementID)
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
	override(s, "sigma-m", &e.SigmaM, fv.sigmaM)
	override(s, "send-timestamps", &e.SendTimestamps, fv.sendTimestamps)
	if s["mobile"] {
		e.Mobility = MobilityFixed
		if fv.mobile {
			e.Mobility = MobilityMobile
		}
	}
	override(s, "emitters", &c.EmitterCount, fv.emitters)
	override(s, "path", &c.Ownship.Path, fv.path)
	override(s, "speed-kts", &c.Ownship.SpeedKts, fv.speedKts)

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
	for _, tok := range []string{flagToken, env(EnvToken), env(EnvFallbackToken), env(EnvDeprecatedToken)} {
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
	up := strings.ToUpper
	s.EmplacementID = strings.TrimSpace(s.EmplacementID)
	s.Mobility = up(strings.TrimSpace(s.Mobility))
	if s.Mobility == "" {
		s.Mobility = MobilityFixed
	}
	s.AzimuthReference = up(strings.TrimSpace(s.AzimuthReference))
	if s.AzimuthReference == "" {
		s.AzimuthReference = AzimuthTrueNorth
	}
	s.Classification = up(strings.TrimSpace(s.Classification))
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

func validLat(x float64) bool { return x >= -90 && x <= 90 }
func validLon(x float64) bool { return x >= -180 && x <= 180 }

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
	if c.ESM() {
		c.validateESM(add)
	} else {
		c.refuseESMSettings(add)
		if err := c.Scenario.Validate(); err != nil {
			errs = append(errs, err)
		}
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
	if kind != KindRadar && kind != KindEOIR && kind != KindESM {
		add("sensor kind (--kind) %q must be radar, eoir or esm", s.Kind)
	}
	// An ESM sensor reporting as an existing emplacement takes its name and
	// site from the server.
	bound := kind == KindESM && s.EmplacementID != ""
	if strings.TrimSpace(s.Name) == "" && !bound {
		add("sensor name (--name) is required")
	}
	if s.Lat == 0 && s.Lon == 0 && !bound {
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
	case kind == KindESM && s.HeightAboveGroundM != nil:
		add("an esm sensor takes --alt, not --height-above-ground: its fixes need no terrain")
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
	if h := s.HeightAboveGroundM; h != nil && kind != KindESM {
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
		switch {
		case v != nil && kind == KindESM:
			add("sensor %s is for radar and eoir: an esm sensor's coverage has no elevation limits", name)
		case v != nil && !(*v >= -90 && *v <= 90):
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
	if kind == KindESM {
		if !positive(s.SigmaM) {
			add("sensor sigmaM (--sigma-m) must be > 0 (got %v)", s.SigmaM)
		}
	} else {
		if !positive(s.SigmaAzDeg) {
			add("sensor sigma-az must be > 0 (got %v)", s.SigmaAzDeg)
		}
		if !positive(s.SigmaElDeg) {
			add("sensor sigma-el must be > 0 (got %v)", s.SigmaElDeg)
		}
		if !positive(s.SigmaRangeM) {
			add("sensor sigma-range-m must be > 0 (got %v)", s.SigmaRangeM)
		}
	}
	mob := s.Mobility
	if mob == "" {
		mob = MobilityFixed
	}
	mobile := strings.EqualFold(mob, MobilityMobile)
	if !oneOf(mob, []string{MobilityFixed, MobilityMobile}) {
		add("sensor mobility %q must be FIXED or MOBILE", s.Mobility)
	}
	az := s.AzimuthReference
	if az == "" {
		az = AzimuthTrueNorth
	}
	if !oneOf(az, []string{AzimuthTrueNorth, AzimuthPlatform}) {
		add("sensor azimuthReference %q must be TRUE_NORTH or PLATFORM", s.AzimuthReference)
	} else if strings.EqualFold(az, AzimuthPlatform) && !mobile {
		add("sensor azimuthReference PLATFORM needs a MOBILE emplacement (--mobile, esm only)")
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

// validateESM checks what only an ESM sensor uses: its emitters, its ownship
// path when it is mobile, and its classification. It needs no terrain and
// observes no scenario.
func (c Config) validateESM(add func(string, ...any)) {
	s := c.Sensor
	if s.Classification != "" && !oneOf(s.Classification, classifications) {
		add("sensor classification %q must be one of %s", s.Classification, strings.Join(classifications, ", "))
	}
	if len(c.Scenario.Targets) > 0 || !c.Scenario.Epoch.IsZero() || c.ScenarioFile != "" {
		add("an esm sensor observes emitters, not a scenario: remove scenario and scenarioFile from its configuration")
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
}

// refuseESMSettings reports the ESM-only settings a radar or camera was
// given, rather than ignore them. The ownship path is ignored, as it is for
// a fixed ESM sensor.
func (c Config) refuseESMSettings(add func(string, ...any)) {
	s := c.Sensor
	only := func(what string) { add("%s is for --kind esm only", what) }
	if s.EmplacementID != "" {
		only("--emplacement-id (sensor.emplacementId)")
	}
	if strings.EqualFold(s.Mobility, MobilityMobile) {
		only("a MOBILE sensor (--mobile, sensor.mobility)")
	}
	if s.Classification != "" {
		only("sensor.classification")
	}
	if s.SendTimestamps {
		only("--send-timestamps (sensor.sendTimestamps)")
	}
	if len(c.Emitters) > 0 || c.EmitterCount != 0 {
		only("--emitters (the emitters list)")
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
		if e.PowerDbm != nil && !finite(*e.PowerDbm) {
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
