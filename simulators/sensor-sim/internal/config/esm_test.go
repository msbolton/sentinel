package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The ESM kind's configuration tests: ew-sensor's, ported to sensor-sim's
// configuration shape (the site under sensor:, --kind esm), so they keep
// asserting what ew-sensor did.

const testToken = "si_0123456789abcdefSECRET"

// esmArgs is the smallest ESM command line that loads and validates.
func esmArgs(extra ...string) []string {
	return append([]string{
		"--kind", "esm",
		"--addr", "ingest.example:50051",
		"--token", testToken,
		"--insecure",
		"--lat", "36.6", "--lon", "-121.9",
	}, extra...)
}

func mustLoad(t *testing.T, args []string, env map[string]string) Config {
	t.Helper()
	cfg, err := Load(args, envOf(env))
	if err != nil {
		t.Fatalf("Load(%v): %v", args, err)
	}
	return cfg
}

func TestESMDefaults(t *testing.T) {
	cfg := mustLoad(t, esmArgs(), nil)

	if cfg.Sensor.Kind != KindESM {
		t.Errorf("Kind = %q, want esm", cfg.Sensor.Kind)
	}
	if cfg.Sensor.Name != DefaultESMName {
		t.Errorf("Name = %q, want %q", cfg.Sensor.Name, DefaultESMName)
	}
	if cfg.EmitterCount != 5 {
		t.Errorf("EmitterCount = %d, want 5", cfg.EmitterCount)
	}
	if cfg.Sensor.Rate != 1.0 {
		t.Errorf("Rate = %v, want 1.0", cfg.Sensor.Rate)
	}
	if cfg.Sensor.Pd != 0.9 {
		t.Errorf("Pd = %v, want 0.9", cfg.Sensor.Pd)
	}
	if cfg.Sensor.SigmaM != 250 {
		t.Errorf("SigmaM = %v, want 250", cfg.Sensor.SigmaM)
	}
	if cfg.Sensor.RangeKm != 120 {
		t.Errorf("RangeKm = %v, want 120", cfg.Sensor.RangeKm)
	}
	if cfg.Sensor.Width != 120 {
		t.Errorf("Width = %v, want 120", cfg.Sensor.Width)
	}
	if cfg.Sensor.SendTimestamps {
		t.Error("SendTimestamps default must be false")
	}
	if cfg.Sensor.Classification != "" {
		t.Errorf("Classification = %q, want empty", cfg.Sensor.Classification)
	}
	if cfg.Sensor.Mobility != MobilityFixed || cfg.Mobile() {
		t.Errorf("Mobility = %q, want FIXED", cfg.Sensor.Mobility)
	}
	if cfg.Sensor.AzimuthReference != AzimuthTrueNorth {
		t.Errorf("AzimuthReference = %q, want TRUE_NORTH", cfg.Sensor.AzimuthReference)
	}
	if cfg.Duration != 0 {
		t.Errorf("Duration = %v, want 0", cfg.Duration)
	}
	if cfg.Ownship.Path != PathCircle || cfg.Ownship.SpeedKts <= 0 || cfg.Ownship.RadiusKm <= 0 {
		t.Errorf("Ownship defaults = %+v", cfg.Ownship)
	}
	if len(cfg.Emitters) != 0 {
		t.Errorf("Emitters = %d, want 0 (synthesised later)", len(cfg.Emitters))
	}
	if cfg.Sensor.HeartbeatInterval != 5*time.Second || cfg.Sensor.HeartbeatSeconds() != 5 {
		t.Errorf("HeartbeatInterval = %v, want 5s", cfg.Sensor.HeartbeatInterval)
	}
	if cfg.Terrain.CacheDir != "" || len(cfg.Scenario.Targets) != 0 {
		t.Errorf("esm needs no terrain and no scenario: terrain %q targets %d", cfg.Terrain.CacheDir, len(cfg.Scenario.Targets))
	}
	if cfg.Sensor.Alt != nil {
		t.Errorf("Alt = %v, want unset (0 m)", *cfg.Sensor.Alt)
	}
}

// The kind can come from the YAML; its defaults follow it.
func TestESMKindFromYAML(t *testing.T) {
	y := writeFile(t, "sensor: {kind: ESM, lat: 1, lon: 1}\n")
	cfg := mustLoad(t, []string{"--addr", "h:1", "--token", "t", "--insecure", "--config", y}, nil)
	if cfg.Sensor.Kind != KindESM || cfg.Sensor.Width != 120 || cfg.Sensor.RangeKm != 120 || cfg.EmitterCount != 5 {
		t.Fatalf("sensor %+v emitters %d, want esm with its defaults", cfg.Sensor, cfg.EmitterCount)
	}
	// --kind beats the YAML's kind, and picks that kind's defaults.
	y2 := writeFile(t, "sensor: {kind: radar, lat: 1, lon: 1}\n")
	cfg = mustLoad(t, []string{"--kind", "esm", "--addr", "h:1", "--token", "t", "--insecure", "--config", y2}, nil)
	if cfg.Sensor.Kind != KindESM || cfg.Sensor.Width != 120 {
		t.Fatalf("sensor %+v, want esm with its defaults", cfg.Sensor)
	}
}

func TestLoadESMCoastalExample(t *testing.T) {
	cfg := mustLoad(t, []string{"--kind", "esm", "--addr", "h:1", "--token", testToken, "--config", "../../examples/esm-coastal.yaml"}, nil)

	if !cfg.Connection.Insecure || cfg.Connection.TLS {
		t.Errorf("the example should default to insecure: %+v", cfg.Connection)
	}
	if cfg.Mobile() {
		t.Error("coastal site must be fixed")
	}
	if cfg.Sensor.Name == "" || cfg.Sensor.Lat == 0 || cfg.Sensor.Lon == 0 {
		t.Errorf("site not loaded: %+v", cfg.Sensor)
	}
	if n := len(cfg.Emitters); n < 4 || n > 6 {
		t.Fatalf("emitters = %d, want 4-6", n)
	}
	if cfg.EmitterCount != len(cfg.Emitters) {
		t.Errorf("EmitterCount = %d, want %d", cfg.EmitterCount, len(cfg.Emitters))
	}
	types := map[string]bool{}
	motions := map[string]bool{}
	for _, e := range cfg.Emitters {
		types[e.EmitterType] = true
		motions[e.Motion.Type] = true
		if e.FrequencyHz <= 0 || e.ID == "" {
			t.Errorf("emitter %q incomplete: %+v", e.ID, e)
		}
	}
	for _, want := range []string{"RADAR_SEARCH", "RADAR_TRACK", "SATCOM", "JAMMER"} {
		if !types[want] {
			t.Errorf("missing emitter type %s", want)
		}
	}
	if len(motions) < 2 {
		t.Errorf("want mixed motions, got %v", motions)
	}
	if cfg.Sensor.Classification != "UNCLASSIFIED" {
		t.Errorf("Classification = %q", cfg.Sensor.Classification)
	}
	// the headline X-band search radar
	var found bool
	for _, e := range cfg.Emitters {
		if e.FrequencyHz == 9.41e9 && e.PriUs == 1000 {
			found = true
		}
	}
	if !found {
		t.Error("expected a 9.41 GHz / PRI 1000 us emitter")
	}
}

func TestLoadESMMobileExample(t *testing.T) {
	cfg := mustLoad(t, []string{"--kind", "esm", "--addr", "h:1", "--token", testToken, "--config", "../../examples/esm-mobile.yaml"}, nil)

	if !cfg.Connection.Insecure || cfg.Connection.TLS {
		t.Errorf("the example should default to insecure: %+v", cfg.Connection)
	}
	if !cfg.Mobile() {
		t.Fatal("mobile example must be MOBILE")
	}
	if cfg.Sensor.AzimuthReference != AzimuthPlatform {
		t.Errorf("AzimuthReference = %q, want PLATFORM", cfg.Sensor.AzimuthReference)
	}
	if cfg.Ownship.Path != PathWaypoints || len(cfg.Ownship.Waypoints) < 2 || cfg.Ownship.SpeedKts <= 0 {
		t.Errorf("ownship = %+v", cfg.Ownship)
	}
	if len(cfg.Emitters) < 3 {
		t.Errorf("emitters = %d, want >= 3", len(cfg.Emitters))
	}
}

func TestESMYAMLFieldsDecode(t *testing.T) {
	y := writeFile(t, `
connection:
  addr: yaml.example:1
  token: yaml-token
  tls: true
  ca: /ca.pem
  cert: /c.pem
  key: /k.pem
sensor:
  kind: esm
  emplacementId: emp-1
  name: Site
  lat: 10
  lon: 20
  alt: 30
  boresight: 40
  width: 50
  rangeKm: 60
  minRangeKm: 5
  rate: 2
  pd: 0.5
  sigmaM: 100
  classification: secret
  sendTimestamps: true
duration: 90s
seed: 0
emitters:
  - id: e1
    displayName: One
    emitterType: RADAR_SEARCH
    frequencyHz: 9.41e9
    bandwidthHz: 1e6
    priUs: 1000
    pulseWidthUs: 1
    modulation: PULSE
    scanType: CIRCULAR
    powerDbm: -50
    affiliation: hostile
    trackEnvironment: sea_surface
    start: {lat: 10.1, lon: 20.1}
    motion:
      type: Waypoints
      speedKts: 10
      waypoints:
        - {lat: 10.2, lon: 20.2}
`)
	cfg := mustLoad(t, []string{"--config", y}, nil)

	c := cfg.Connection
	if c.Addr != "yaml.example:1" || c.Token != "yaml-token" || !c.TLS || c.Insecure || c.CA != "/ca.pem" || c.Cert != "/c.pem" || c.Key != "/k.pem" {
		t.Errorf("connection = %+v", c)
	}
	s := cfg.Sensor
	if s.EmplacementID != "emp-1" || s.Name != "Site" || s.Lat != 10 || s.Lon != 20 || s.Alt == nil || *s.Alt != 30 ||
		s.Boresight != 40 || s.Width != 50 || s.RangeKm != 60 || s.MinRangeKm != 5 {
		t.Errorf("site = %+v", s)
	}
	if s.Rate != 2 || s.Pd != 0.5 || s.SigmaM != 100 || s.Classification != "SECRET" || !s.SendTimestamps {
		t.Errorf("sensor = %+v", s)
	}
	if cfg.Duration != 90*time.Second {
		t.Errorf("Duration = %v", cfg.Duration)
	}
	if cfg.Seed != 0 || cfg.SeedGenerated {
		t.Errorf("explicit YAML seed 0 must be kept: seed=%d generated=%v", cfg.Seed, cfg.SeedGenerated)
	}
	if len(cfg.Emitters) != 1 {
		t.Fatalf("emitters = %d", len(cfg.Emitters))
	}
	em := cfg.Emitters[0]
	if em.ID != "e1" || em.DisplayName != "One" || em.EmitterType != "RADAR_SEARCH" || em.FrequencyHz != 9.41e9 ||
		em.BandwidthHz != 1e6 || em.PriUs != 1000 || em.PulseWidthUs != 1 || em.Modulation != "PULSE" ||
		em.ScanType != "CIRCULAR" || em.PowerDbm == nil || *em.PowerDbm != -50 ||
		em.Affiliation != "HOSTILE" || em.TrackEnvironment != "SEA_SURFACE" ||
		em.Start != (LatLon{10.1, 20.1}) {
		t.Errorf("emitter = %+v", em)
	}
	if em.Motion.Type != MotionWaypoints || em.Motion.SpeedKts != 10 || len(em.Motion.Waypoints) != 1 {
		t.Errorf("motion = %+v", em.Motion)
	}
}

// ew-sensor's environment names still work, below the new ones, and each
// one set produces a single warning for the caller to log.
func TestDeprecatedEnvironmentFallbacks(t *testing.T) {
	y := writeFile(t, `
connection:
  addr: yaml:1
  token: yaml-token
  insecure: true
sensor: {kind: esm, lat: 1, lon: 1}
`)
	cases := []struct {
		name      string
		args      []string
		env       map[string]string
		wantToken string
		wantAddr  string
		wantWarn  []string
	}{
		{"yaml only", []string{"--config", y}, nil, "yaml-token", "yaml:1", nil},
		{"EW_SENSOR_TOKEN beats yaml", []string{"--config", y}, map[string]string{EnvDeprecatedToken: "tok-est"}, "tok-est", "yaml:1",
			[]string{EnvDeprecatedToken + " is deprecated", "rename it " + EnvToken}},
		// A simulator's own token file (EW_SENSOR_TOKEN for esm-sim) must beat
		// the generic SENTINEL_INGEST_TOKEN a shared env file may also set.
		{"EW_SENSOR_TOKEN beats SENTINEL_INGEST_TOKEN", []string{"--config", y}, map[string]string{EnvFallbackToken: "tok-sit", EnvDeprecatedToken: "tok-est"}, "tok-est", "yaml:1",
			[]string{EnvDeprecatedToken + " is deprecated", "rename it " + EnvToken}},
		{"SENSOR_SIM_TOKEN beats both", []string{"--config", y}, map[string]string{EnvToken: "tok-sst", EnvFallbackToken: "tok-sit", EnvDeprecatedToken: "tok-est"}, "tok-sst", "yaml:1",
			[]string{"ignored", EnvToken}},
		{"SENTINEL_INGEST_TOKEN beats yaml", []string{"--config", y}, map[string]string{EnvFallbackToken: "tok-sit"}, "tok-sit", "yaml:1", nil},
		{"EW_SENSOR_ADDR beats yaml", []string{"--config", y}, map[string]string{EnvDeprecatedAddr: "env:2"}, "yaml-token", "env:2",
			[]string{EnvDeprecatedAddr + " is deprecated", "rename it " + EnvAddr}},
		{"SENSOR_SIM_ADDR beats EW_SENSOR_ADDR", []string{"--config", y}, map[string]string{EnvAddr: "new:3", EnvDeprecatedAddr: "env:2"}, "yaml-token", "new:3",
			[]string{EnvDeprecatedAddr + " is deprecated", "ignored"}},
		{"flags beat everything", []string{"--config", y, "--token", "flag-token", "--addr", "flag:4"},
			map[string]string{EnvDeprecatedToken: "tok-est", EnvDeprecatedAddr: "env:2"}, "flag-token", "flag:4",
			[]string{"ignored", "--token", "--addr"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := mustLoad(t, tc.args, tc.env)
			if cfg.Connection.Token != tc.wantToken {
				t.Errorf("token mismatch (len got=%d want=%d)", len(cfg.Connection.Token), len(tc.wantToken))
			}
			if cfg.Connection.Addr != tc.wantAddr {
				t.Errorf("addr = %q, want %q", cfg.Connection.Addr, tc.wantAddr)
			}
			all := strings.Join(cfg.Warnings, "\n")
			if len(tc.wantWarn) == 0 && len(cfg.Warnings) != 0 {
				t.Errorf("warnings %q, want none", cfg.Warnings)
			}
			for _, w := range tc.wantWarn {
				if !strings.Contains(all, w) {
					t.Errorf("warnings %q lack %q", cfg.Warnings, w)
				}
			}
			for _, k := range []string{EnvToken, EnvFallbackToken, EnvDeprecatedToken} {
				if v := tc.env[k]; v != "" && strings.Contains(all, v) {
					t.Errorf("a warning shows the %s value: %q", k, cfg.Warnings)
				}
			}
			// One warning per deprecated variable set.
			n := 0
			for _, k := range []string{EnvDeprecatedToken, EnvDeprecatedAddr} {
				if tc.env[k] != "" {
					n++
				}
			}
			if len(cfg.Warnings) != n {
				t.Errorf("%d warnings %q, want %d", len(cfg.Warnings), cfg.Warnings, n)
			}
		})
	}
	// The deprecated token is redacted from errors too.
	_, err := Load([]string{"--kind", "esm", "--insecure", "--lat", "1", "--lon", "1", "--duration", "si_deprecatedsecret"},
		envOf(map[string]string{EnvDeprecatedToken: "si_deprecatedsecret"}))
	if err == nil || strings.Contains(err.Error(), "si_deprecatedsecret") {
		t.Errorf("error %v leaks the deprecated token", err)
	}
}

// ESM fixes are positional: the kind needs no terrain and no scenario.
func TestESMRunsWithoutTerrainOrScenario(t *testing.T) {
	cfg := mustLoad(t, esmArgs("--terrain-cache", ""), nil)
	if cfg.Terrain.CacheDir != "" {
		t.Fatalf("terrain %q", cfg.Terrain.CacheDir)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestESMValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"missing addr", []string{"--kind", "esm", "--token", "t", "--insecure", "--lat", "1", "--lon", "1"}, "addr"},
		{"missing token", []string{"--kind", "esm", "--addr", "h:1", "--insecure", "--lat", "1", "--lon", "1"}, "token"},
		{"pd zero", esmArgs("--pd", "0"), "pd"},
		{"pd above one", esmArgs("--pd", "1.5"), "pd"},
		{"pd NaN", esmArgs("--pd", "NaN"), "pd"},
		{"rate zero", esmArgs("--rate", "0"), "rate"},
		{"rate above the bound", esmArgs("--rate", "100.5"), "rate"},
		{"emitters above the bound", esmArgs("--emitters", "1001"), "emitters"},
		{"sigma zero", esmArgs("--sigma-m", "0"), "sigma"},
		{"heartbeat interval zero", esmArgs("--heartbeat-interval", "0s"), "heartbeat"},
		{"heartbeat interval above the bound", esmArgs("--heartbeat-interval", "301s"), "heartbeat"},
		{"heartbeat interval not whole seconds", esmArgs("--heartbeat-interval", "1500ms"), "heartbeat"},
		{"width zero", esmArgs("--width", "0"), "width"},
		{"width over 360", esmArgs("--width", "361"), "width"},
		{"range zero", esmArgs("--range-km", "0"), "range"},
		{"min range not below range", esmArgs("--min-range-km", "120"), "min-range"},
		{"lat out of range", esmArgs("--lat", "91"), "lat"},
		{"lon out of range", esmArgs("--lon", "181"), "lon"},
		{"no position", []string{"--kind", "esm", "--addr", "h:1", "--token", "t", "--insecure"}, "lat"},
		{"negative duration", esmArgs("--duration", "-1s"), "duration"},
		{"negative emitters", esmArgs("--emitters", "-1"), "emitters"},
		{"zero emitters", esmArgs("--emitters", "0"), "emitter"},
		{"bad path", esmArgs("--mobile", "--path", "zigzag"), "path"},
		{"mobile speed zero", esmArgs("--mobile", "--speed-kts", "0"), "speed"},
		{"waypoints path without waypoints", esmArgs("--mobile", "--path", "waypoints"), "waypoints"},
		{"height above ground", esmArgs("--height-above-ground", "10", "--terrain-cache", "/tmp/t"), "--alt"},
		{"elevation limits", esmArgs("--elev-max", "30"), "elev"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(tc.args, envOf(nil))
			if err == nil {
				t.Fatalf("Load(%v) succeeded, want error mentioning %q", tc.args, tc.want)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}

	t.Run("a scenario", func(t *testing.T) {
		_, err := Load(esmArgs("--config", writeFile(t, scenarioYAML())), envOf(nil))
		if err == nil || !strings.Contains(err.Error(), "scenario") {
			t.Fatalf("Load = %v, want a scenario error", err)
		}
	})

	t.Run("all problems reported together", func(t *testing.T) {
		_, err := Load([]string{"--kind", "esm", "--pd", "7", "--rate", "-1"}, envOf(nil))
		if err == nil {
			t.Fatal("expected error")
		}
		for _, w := range []string{"addr", "token", "pd", "rate"} {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("error missing %q: %v", w, err)
			}
		}
	})
}

// The settings only an ESM sensor uses are refused on a radar or a camera
// rather than silently ignored.
func TestESMSettingsRefusedForOtherKinds(t *testing.T) {
	file := writeFile(t, scenarioYAML())
	cases := []struct {
		name string
		args []string
		yaml string
		want string
	}{
		{"--mobile", baseArgs("--mobile"), "", "mobile"},
		{"--emplacement-id", baseArgs("--emplacement-id", "emp-1"), "", "emplacement-id"},
		{"--emitters", baseArgs("--emitters", "3"), "", "emitters"},
		{"--send-timestamps", baseArgs("--send-timestamps"), "", "send-timestamps"},
		{"emitters list", nil, "emitters:\n  - {id: a, emitterType: X, frequencyHz: 1e9, start: {lat: 1, lon: 1}}\n", "emitters"},
		{"classification", nil, "sensor: {classification: SECRET}\n", "classification"},
		{"platform azimuth", nil, "sensor: {azimuthReference: PLATFORM}\n", "azimuthReference"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := tc.args
			if tc.yaml != "" {
				args = baseArgs("--config", writeFile(t, tc.yaml+scenarioYAML()))
			} else {
				args = append(args, "--config", file)
			}
			_, err := Load(args, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load = %v, want an error naming %q", err, tc.want)
			}
		})
	}
}

func TestESMEmplacementIDDoesNotRequireNameOrPosition(t *testing.T) {
	cfg := mustLoad(t, []string{"--kind", "esm", "--addr", "h:1", "--token", "t", "--insecure", "--emplacement-id", "emp-9", "--name", ""}, nil)
	if cfg.Sensor.EmplacementID != "emp-9" {
		t.Errorf("id = %q", cfg.Sensor.EmplacementID)
	}
}

func TestESMMobileValidation(t *testing.T) {
	cfg := mustLoad(t, esmArgs("--mobile"), nil)
	if !cfg.Mobile() || cfg.Ownship.Path != PathCircle {
		t.Errorf("--mobile defaults: %+v", cfg)
	}

	// PLATFORM-relative boresight needs a platform with a heading.
	y := writeFile(t, `
connection: {addr: h:1, token: t, insecure: true}
sensor: {kind: esm, lat: 1, lon: 1, azimuthReference: platform}
`)
	if _, err := Load([]string{"--config", y}, envOf(nil)); err == nil || !strings.Contains(err.Error(), "azimuthReference") {
		t.Errorf("PLATFORM on a FIXED emplacement must fail, got %v", err)
	}
	cfg = mustLoad(t, []string{"--config", y, "--mobile"}, nil)
	if cfg.Sensor.AzimuthReference != AzimuthPlatform {
		t.Errorf("AzimuthReference = %q", cfg.Sensor.AzimuthReference)
	}

	y2 := writeFile(t, `
connection: {addr: h:1, token: t, insecure: true}
sensor: {kind: esm, lat: 1, lon: 1, mobility: bogus}
`)
	if _, err := Load([]string{"--config", y2}, envOf(nil)); err == nil || !strings.Contains(err.Error(), "mobility") {
		t.Errorf("bad mobility must fail, got %v", err)
	}

	// Ownship settings are ignored (not validated) for a fixed site.
	y3 := writeFile(t, `
connection: {addr: h:1, token: t, insecure: true}
sensor: {kind: esm, lat: 1, lon: 1}
ownship: {path: waypoints}
`)
	mustLoad(t, []string{"--config", y3}, nil)
}

func TestESMClassification(t *testing.T) {
	const hdr = "connection: {addr: h:1, token: t, insecure: true}\nsensor: {kind: esm, lat: 1, lon: 1, classification: "
	for _, in := range []string{"unclassified", "Confidential", "SECRET", "top_secret"} {
		got := mustLoad(t, []string{"--config", writeFile(t, hdr+in+"}\n")}, nil)
		if got.Sensor.Classification != strings.ToUpper(in) {
			t.Errorf("classification %q -> %q", in, got.Sensor.Classification)
		}
	}
	if _, err := Load([]string{"--config", writeFile(t, hdr+"cosmic}\n")}, envOf(nil)); err == nil || !strings.Contains(err.Error(), "classification") {
		t.Errorf("unknown classification must fail, got %v", err)
	}
}

func TestESMEmitterValidation(t *testing.T) {
	const hdr = "connection: {addr: h:1, token: t, insecure: true}\nsensor: {kind: esm, lat: 1, lon: 1}\nemitters:\n"
	ok := "  - {id: a, emitterType: SATCOM, frequencyHz: 1e9, start: {lat: 1, lon: 1}, motion: {type: static}}\n"
	cases := []struct{ name, body, want string }{
		{"missing id", "  - {emitterType: SATCOM, frequencyHz: 1e9, start: {lat: 1, lon: 1}}\n", "id"},
		{"duplicate id", ok + ok, "duplicate"},
		{"missing type", "  - {id: a, frequencyHz: 1e9, start: {lat: 1, lon: 1}}\n", "emitterType"},
		{"zero frequency", "  - {id: a, emitterType: X, start: {lat: 1, lon: 1}}\n", "frequencyHz"},
		{"negative pri", "  - {id: a, emitterType: X, frequencyHz: 1e9, priUs: -1, start: {lat: 1, lon: 1}}\n", "priUs"},
		{"bad start", "  - {id: a, emitterType: X, frequencyHz: 1e9, start: {lat: 95, lon: 1}}\n", "start"},
		{"bad motion", "  - {id: a, emitterType: X, frequencyHz: 1e9, start: {lat: 1, lon: 1}, motion: {type: teleport}}\n", "motion"},
		{"random-walk without speed", "  - {id: a, emitterType: X, frequencyHz: 1e9, start: {lat: 1, lon: 1}, motion: {type: random-walk}}\n", "speedKts"},
		{"waypoints without list", "  - {id: a, emitterType: X, frequencyHz: 1e9, start: {lat: 1, lon: 1}, motion: {type: waypoints, speedKts: 5}}\n", "waypoints"},
		{"bad affiliation", "  - {id: a, emitterType: X, frequencyHz: 1e9, start: {lat: 1, lon: 1}, affiliation: ENEMY}\n", "affiliation"},
		{"bad environment", "  - {id: a, emitterType: X, frequencyHz: 1e9, start: {lat: 1, lon: 1}, trackEnvironment: MARS}\n", "trackEnvironment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load([]string{"--config", writeFile(t, hdr+tc.body)}, envOf(nil))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want mention of %q", err, tc.want)
			}
		})
	}

	t.Run("defaults: motion static, environment GROUND", func(t *testing.T) {
		body := hdr + "  - {id: a, emitterType: X, frequencyHz: 1e9, start: {lat: 1, lon: 1}}\n  - {id: b, emitterType: X, frequencyHz: 1e9, start: {lat: 1, lon: 1}, trackEnvironment: land}\n"
		cfg := mustLoad(t, []string{"--config", writeFile(t, body)}, nil)
		for _, e := range cfg.Emitters {
			if e.Motion.Type != MotionStatic || e.TrackEnvironment != "GROUND" {
				t.Errorf("emitter %s = motion %q env %q", e.ID, e.Motion.Type, e.TrackEnvironment)
			}
		}
		if cfg.Emitters[0].PowerDbm != nil {
			t.Error("unset powerDbm must stay nil")
		}
	})
}

func TestESMEmitterCount(t *testing.T) {
	const hdr = "connection: {addr: h:1, token: t, insecure: true}\nsensor: {kind: esm, lat: 1, lon: 1}\nemitters:\n"
	two := "  - {id: a, emitterType: X, frequencyHz: 1e9, start: {lat: 1, lon: 1}}\n  - {id: b, emitterType: X, frequencyHz: 1e9, start: {lat: 1, lon: 1}}\n"
	y := writeFile(t, hdr+two)

	cfg := mustLoad(t, []string{"--config", y}, nil)
	if cfg.EmitterCount != 2 {
		t.Errorf("YAML list only: EmitterCount = %d, want 2", cfg.EmitterCount)
	}
	cfg = mustLoad(t, []string{"--config", y, "--emitters", "4"}, nil)
	if cfg.EmitterCount != 4 || len(cfg.Emitters) != 2 {
		t.Errorf("top-up: count=%d len=%d, want 4/2", cfg.EmitterCount, len(cfg.Emitters))
	}
	cfg = mustLoad(t, []string{"--config", y, "--emitters", "1"}, nil)
	if cfg.EmitterCount != 1 || len(cfg.Emitters) != 1 || cfg.Emitters[0].ID != "a" {
		t.Errorf("truncate: count=%d emitters=%v", cfg.EmitterCount, cfg.Emitters)
	}
}

func TestESMRateAndEmitterBounds(t *testing.T) {
	cfg := mustLoad(t, esmArgs("--rate", "100", "--emitters", "1000"), nil)
	if cfg.Sensor.Rate != 100 || cfg.EmitterCount != 1000 {
		t.Fatalf("rate %v emitters %d, want the bounds accepted", cfg.Sensor.Rate, cfg.EmitterCount)
	}
	cfg.Sensor.Rate = 2e9
	cfg.EmitterCount = 1001
	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate accepted rate 2e9 and 1001 emitters")
	}
	for _, w := range []string{"rate must be <= 100", "emitters must be <= 1000"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error missing %q: %v", w, err)
		}
	}
}

// The flags ew-sensor had that sensor-sim did not, under their ew-sensor
// names.
func TestESMFlags(t *testing.T) {
	cfg := mustLoad(t, esmArgs("--emplacement-id", "emp-2", "--sigma-m", "75", "--send-timestamps",
		"--mobile", "--path", "waypoints", "--speed-kts", "9", "--alt", "18",
		"--config", writeFile(t, "ownship:\n  waypoints:\n    - {lat: 1, lon: 1}\n    - {lat: 1.1, lon: 1}\n")), nil)
	s := cfg.Sensor
	if s.EmplacementID != "emp-2" || s.SigmaM != 75 || !s.SendTimestamps || !cfg.Mobile() || s.Alt == nil || *s.Alt != 18 {
		t.Errorf("sensor %+v", s)
	}
	if cfg.Ownship.Path != PathWaypoints || cfg.Ownship.SpeedKts != 9 {
		t.Errorf("ownship %+v", cfg.Ownship)
	}
	// --mobile=false turns a YAML MOBILE site back into a fixed one.
	y := writeFile(t, "sensor: {mobility: MOBILE}\n")
	if cfg := mustLoad(t, esmArgs("--config", y, "--mobile=false"), nil); cfg.Mobile() {
		t.Error("--mobile=false did not override the YAML")
	}
	u := Usage()
	for _, f := range []string{"emplacement-id", "emitters", "sigma-m", "mobile", "path", "speed-kts", "send-timestamps"} {
		if !strings.Contains(u, "-"+f) {
			t.Errorf("usage lacks --%s", f)
		}
	}
	if !strings.Contains(u, "esm") {
		t.Error("usage does not mention the esm kind")
	}
}

func TestESMTokenNeverLeaks(t *testing.T) {
	bad := [][]string{
		esmArgs("--pd", "9"),
		esmArgs("--tls"),
		esmArgs("--config", writeFile(t, "sensor: {bogus: 1}\n")),
		esmArgs("--config", writeFile(t, "connection: {token: "+testToken+"}\nsensor: {rate: [1]}\n")),
		esmArgs(testToken), // stray positional argument that happens to be the token
		esmArgs("--duration", testToken),
	}
	for _, args := range bad {
		_, err := Load(args, envOf(map[string]string{EnvDeprecatedToken: testToken}))
		if err == nil {
			t.Fatalf("expected error for %v", args)
		}
		if strings.Contains(err.Error(), testToken) {
			t.Errorf("error leaks token: %v", err)
		}
	}
	cfg := mustLoad(t, esmArgs(), nil)
	for _, s := range []string{fmt.Sprintf("%v", cfg), fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg)} {
		if strings.Contains(s, testToken) {
			t.Errorf("formatted output leaks token: %s", s)
		}
	}
}

func TestESMExamplesShareNothingWithTheScenario(t *testing.T) {
	for _, f := range []string{"esm-coastal.yaml", "esm-mobile.yaml"} {
		cfg := mustLoad(t, []string{"--kind", "esm", "--addr", "h:1", "--token", "t", "--config", filepath.Join("..", "..", "examples", f)}, nil)
		if cfg.ScenarioFile != "" || len(cfg.Scenario.Targets) != 0 || cfg.Terrain.CacheDir != "" {
			t.Errorf("%s: scenario %q targets %d terrain %q, want none", f, cfg.ScenarioFile, len(cfg.Scenario.Targets), cfg.Terrain.CacheDir)
		}
	}
}
