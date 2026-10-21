package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testToken = "si_0123456789abcdefSECRET"

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// minimal is the smallest argument list that loads and validates.
func minimal(extra ...string) []string {
	return append([]string{
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

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sensor.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaults(t *testing.T) {
	cfg := mustLoad(t, minimal(), nil)

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
	if cfg.Emplacement.RangeKm != 120 {
		t.Errorf("RangeKm = %v, want 120", cfg.Emplacement.RangeKm)
	}
	if cfg.Emplacement.Width != 120 {
		t.Errorf("Width = %v, want 120", cfg.Emplacement.Width)
	}
	if cfg.Sensor.SendTimestamps {
		t.Error("SendTimestamps default must be false")
	}
	if cfg.Sensor.Classification != "" {
		t.Errorf("Classification = %q, want empty", cfg.Sensor.Classification)
	}
	if cfg.Emplacement.Mobility != MobilityFixed || cfg.Mobile() {
		t.Errorf("Mobility = %q, want FIXED", cfg.Emplacement.Mobility)
	}
	if cfg.Emplacement.AzimuthReference != AzimuthTrueNorth {
		t.Errorf("AzimuthReference = %q, want TRUE_NORTH", cfg.Emplacement.AzimuthReference)
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
}

func TestLoadCoastalExample(t *testing.T) {
	cfg := mustLoad(t, []string{"--addr", "h:1", "--token", testToken, "--config", "../../examples/coastal-esm.yaml"}, nil)

	if cfg.Connection.Insecure == cfg.Connection.TLS {
		t.Errorf("exactly one of insecure/tls expected: %+v", cfg.Connection)
	}
	if cfg.Mobile() {
		t.Error("coastal site must be fixed")
	}
	if cfg.Emplacement.Name == "" || cfg.Emplacement.Lat == 0 || cfg.Emplacement.Lon == 0 {
		t.Errorf("emplacement not loaded: %+v", cfg.Emplacement)
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

func TestLoadMobileExample(t *testing.T) {
	cfg := mustLoad(t, []string{"--addr", "h:1", "--token", testToken, "--config", "../../examples/mobile-esm.yaml"}, nil)

	if !cfg.Mobile() {
		t.Fatal("mobile example must be MOBILE")
	}
	if cfg.Emplacement.AzimuthReference != AzimuthPlatform {
		t.Errorf("AzimuthReference = %q, want PLATFORM", cfg.Emplacement.AzimuthReference)
	}
	if cfg.Ownship.Path != PathWaypoints || len(cfg.Ownship.Waypoints) < 2 || cfg.Ownship.SpeedKts <= 0 {
		t.Errorf("ownship = %+v", cfg.Ownship)
	}
	if len(cfg.Emitters) < 3 {
		t.Errorf("emitters = %d, want >= 3", len(cfg.Emitters))
	}
}

func TestYAMLFieldsDecode(t *testing.T) {
	y := writeYAML(t, `
connection:
  addr: yaml.example:1
  token: yaml-token
  tls: true
  ca: /ca.pem
  cert: /c.pem
  key: /k.pem
emplacement:
  emplacementId: emp-1
  name: Site
  lat: 10
  lon: 20
  alt: 30
  boresight: 40
  width: 50
  rangeKm: 60
  minRangeKm: 5
sensor:
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
	e := cfg.Emplacement
	if e.EmplacementID != "emp-1" || e.Name != "Site" || e.Lat != 10 || e.Lon != 20 || e.Alt != 30 ||
		e.Boresight != 40 || e.Width != 50 || e.RangeKm != 60 || e.MinRangeKm != 5 {
		t.Errorf("emplacement = %+v", e)
	}
	s := cfg.Sensor
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

func TestPrecedenceTokenAndAddr(t *testing.T) {
	y := writeYAML(t, `
connection:
  addr: yaml:1
  token: yaml-token
  insecure: true
emplacement: {lat: 1, lon: 1}
`)
	cases := []struct {
		name      string
		args      []string
		env       map[string]string
		wantToken string
		wantAddr  string
	}{
		{"yaml only", []string{"--config", y}, nil, "yaml-token", "yaml:1"},
		{"SENTINEL_INGEST_TOKEN beats yaml", []string{"--config", y}, map[string]string{"SENTINEL_INGEST_TOKEN": "sit"}, "sit", "yaml:1"},
		{"EW_SENSOR_TOKEN beats SENTINEL_INGEST_TOKEN", []string{"--config", y}, map[string]string{"SENTINEL_INGEST_TOKEN": "sit", "EW_SENSOR_TOKEN": "est"}, "est", "yaml:1"},
		{"EW_SENSOR_ADDR beats yaml", []string{"--config", y}, map[string]string{"EW_SENSOR_ADDR": "env:2"}, "yaml-token", "env:2"},
		{"flags beat env and yaml", []string{"--config", y, "--token", "flag-token", "--addr", "flag:3"},
			map[string]string{"SENTINEL_INGEST_TOKEN": "sit", "EW_SENSOR_TOKEN": "est", "EW_SENSOR_ADDR": "env:2"}, "flag-token", "flag:3"},
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
		})
	}
}

func TestFlagsOverrideYAML(t *testing.T) {
	y := writeYAML(t, `
connection: {addr: y:1, token: ytok, tls: true, ca: /y-ca, cert: /y-cert, key: /y-key}
emplacement: {emplacementId: y-id, name: y-name, lat: 1, lon: 2, alt: 3, boresight: 4, width: 5, rangeKm: 6, minRangeKm: 1}
sensor: {rate: 7, pd: 0.1, sigmaM: 8, sendTimestamps: false}
ownship: {path: waypoints, speedKts: 9, waypoints: [{lat: 1, lon: 1}, {lat: 2, lon: 2}]}
duration: 1h
seed: 5
emitters:
  - {id: a, emitterType: SATCOM, frequencyHz: 1e9, start: {lat: 1, lon: 1}, motion: {type: static}}
  - {id: b, emitterType: SATCOM, frequencyHz: 1e9, start: {lat: 1, lon: 1}, motion: {type: static}}
`)
	args := []string{
		"--config", y,
		"--addr", "f:2", "--token", "ftok", "--insecure", "--tls=false",
		"--emplacement-id", "f-id", "--name", "f-name",
		"--lat", "11", "--lon", "12", "--alt", "13", "--boresight", "14", "--width", "15",
		"--range-km", "16", "--min-range-km", "2",
		"--emitters", "1", "--rate", "17", "--pd", "0.7", "--sigma-m", "18",
		"--seed", "99", "--duration", "30s", "--mobile", "--path", "circle", "--speed-kts", "19",
		"--send-timestamps",
	}
	cfg := mustLoad(t, args, nil)

	c := cfg.Connection
	if c.Addr != "f:2" || c.Token != "ftok" || !c.Insecure || c.TLS {
		t.Errorf("connection = %+v", c)
	}
	if c.CA != "" || c.Cert != "" || c.Key != "" {
		t.Errorf("--insecure must drop the YAML's TLS-only fields: %+v", c)
	}
	e := cfg.Emplacement
	if e.EmplacementID != "f-id" || e.Name != "f-name" || e.Lat != 11 || e.Lon != 12 || e.Alt != 13 ||
		e.Boresight != 14 || e.Width != 15 || e.RangeKm != 16 || e.MinRangeKm != 2 || e.Mobility != MobilityMobile {
		t.Errorf("emplacement = %+v", e)
	}
	s := cfg.Sensor
	if s.Rate != 17 || s.Pd != 0.7 || s.SigmaM != 18 || !s.SendTimestamps {
		t.Errorf("sensor = %+v", s)
	}
	if cfg.EmitterCount != 1 || len(cfg.Emitters) != 1 {
		t.Errorf("emitters: count=%d len=%d, want 1/1 (truncated)", cfg.EmitterCount, len(cfg.Emitters))
	}
	if cfg.Seed != 99 || cfg.SeedGenerated {
		t.Errorf("seed = %d generated=%v", cfg.Seed, cfg.SeedGenerated)
	}
	if cfg.Duration != 30*time.Second {
		t.Errorf("Duration = %v", cfg.Duration)
	}
	if cfg.Ownship.Path != PathCircle || cfg.Ownship.SpeedKts != 19 {
		t.Errorf("ownship = %+v", cfg.Ownship)
	}
}

func TestExplicitFlagEqualToDefaultStillOverrides(t *testing.T) {
	y := writeYAML(t, `
connection: {insecure: true}
emplacement: {lat: 1, lon: 1, width: 45, rangeKm: 10}
sensor: {rate: 5, pd: 0.5, sigmaM: 10}
`)
	cfg := mustLoad(t, []string{"--config", y, "--addr", "h:1", "--token", "t",
		"--rate", "1", "--pd", "0.9", "--sigma-m", "250", "--width", "120", "--range-km", "120"}, nil)
	if cfg.Sensor.Rate != 1 || cfg.Sensor.Pd != 0.9 || cfg.Sensor.SigmaM != 250 ||
		cfg.Emplacement.Width != 120 || cfg.Emplacement.RangeKm != 120 {
		t.Errorf("explicit default-valued flags ignored: %+v %+v", cfg.Sensor, cfg.Emplacement)
	}
}

func TestExplicitZeroFlagOverridesYAML(t *testing.T) {
	y := writeYAML(t, `
connection: {insecure: true}
emplacement: {lat: 1, lon: 1, minRangeKm: 5, alt: 100}
duration: 1h
`)
	cfg := mustLoad(t, []string{"--config", y, "--addr", "h:1", "--token", "t",
		"--min-range-km", "0", "--alt", "0", "--duration", "0"}, nil)
	if cfg.Emplacement.MinRangeKm != 0 || cfg.Emplacement.Alt != 0 || cfg.Duration != 0 {
		t.Errorf("explicit zero flags ignored: %+v duration=%v", cfg.Emplacement, cfg.Duration)
	}
}

func TestTransportFlagSwitchesYAMLMode(t *testing.T) {
	y := writeYAML(t, `
connection: {addr: h:1, token: t, tls: true, ca: /ca.pem}
emplacement: {lat: 1, lon: 1}
`)
	// --insecure on the command line replaces the YAML's tls mode (and its tls-only fields).
	cfg := mustLoad(t, []string{"--config", y, "--insecure"}, nil)
	if !cfg.Connection.Insecure || cfg.Connection.TLS || cfg.Connection.CA != "" {
		t.Errorf("connection = %+v", cfg.Connection)
	}
	// Both flags explicitly set is still an error.
	if _, err := Load([]string{"--config", y, "--insecure", "--tls"}, envOf(nil)); err == nil {
		t.Error("--insecure with --tls must fail")
	}
	// With the YAML left alone it stays TLS.
	cfg = mustLoad(t, []string{"--config", y}, nil)
	if !cfg.Connection.TLS || cfg.Connection.Insecure {
		t.Errorf("connection = %+v", cfg.Connection)
	}
}

func TestValidationErrors(t *testing.T) {
	good := func(t *testing.T) Config { return mustLoad(t, minimal(), nil) }
	cases := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"missing addr", []string{"--token", "t", "--insecure", "--lat", "1", "--lon", "1"}, nil, "addr"},
		{"missing token", []string{"--addr", "h:1", "--insecure", "--lat", "1", "--lon", "1"}, nil, "token"},
		{"bad addr", minimal("--addr", "nohostport"), nil, "addr"},
		{"neither insecure nor tls", []string{"--addr", "h:1", "--token", "t", "--lat", "1", "--lon", "1"}, nil, "--insecure"},
		{"both insecure and tls", minimal("--tls"), nil, "--insecure"},
		{"cert without key", minimal("--insecure=false", "--tls", "--cert", "c.pem"), nil, "--key"},
		{"key without cert", minimal("--insecure=false", "--tls", "--key", "k.pem"), nil, "--cert"},
		{"cert without tls", minimal("--cert", "c.pem", "--key", "k.pem"), nil, "--tls"},
		{"ca without tls", minimal("--ca", "ca.pem"), nil, "--ca"},
		{"pd zero", minimal("--pd", "0"), nil, "pd"},
		{"pd above one", minimal("--pd", "1.5"), nil, "pd"},
		{"pd negative", minimal("--pd", "-0.1"), nil, "pd"},
		{"pd NaN", minimal("--pd", "NaN"), nil, "pd"},
		{"rate zero", minimal("--rate", "0"), nil, "rate"},
		{"rate negative", minimal("--rate", "-1"), nil, "rate"},
		{"rate above the bound", minimal("--rate", "100.5"), nil, "rate"},
		{"rate far above the bound", minimal("--rate", "2e9"), nil, "rate"},
		{"emitters above the bound", minimal("--emitters", "1001"), nil, "emitters"},
		{"sigma zero", minimal("--sigma-m", "0"), nil, "sigma"},
		{"heartbeat interval zero", minimal("--heartbeat-interval", "0s"), nil, "heartbeat"},
		{"heartbeat interval above the bound", minimal("--heartbeat-interval", "301s"), nil, "heartbeat"},
		{"heartbeat interval not whole seconds", minimal("--heartbeat-interval", "1500ms"), nil, "heartbeat"},
		{"width zero", minimal("--width", "0"), nil, "width"},
		{"width over 360", minimal("--width", "361"), nil, "width"},
		{"range zero", minimal("--range-km", "0"), nil, "range"},
		{"min range not below range", minimal("--min-range-km", "120"), nil, "min-range"},
		{"lat out of range", minimal("--lat", "91"), nil, "lat"},
		{"lon out of range", minimal("--lon", "181"), nil, "lon"},
		{"no position", []string{"--addr", "h:1", "--token", "t", "--insecure"}, nil, "lat"},
		{"negative duration", minimal("--duration", "-1s"), nil, "duration"},
		{"negative emitters", minimal("--emitters", "-1"), nil, "emitters"},
		{"zero emitters", minimal("--emitters", "0"), nil, "emitter"},
		{"bad path", minimal("--mobile", "--path", "zigzag"), nil, "path"},
		{"mobile speed zero", minimal("--mobile", "--speed-kts", "0"), nil, "speed"},
		{"waypoints path without waypoints", minimal("--mobile", "--path", "waypoints"), nil, "waypoints"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(tc.args, envOf(tc.env))
			if err == nil {
				t.Fatalf("Load(%v) succeeded, want error mentioning %q", tc.args, tc.want)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}

	t.Run("Validate on a hand-built config", func(t *testing.T) {
		c := good(t)
		if err := c.Validate(); err != nil {
			t.Fatalf("good config invalid: %v", err)
		}
		c.Sensor.Pd = 2
		if err := c.Validate(); err == nil {
			t.Error("Validate accepted pd=2")
		}
	})

	t.Run("all problems reported together", func(t *testing.T) {
		_, err := Load([]string{"--pd", "7", "--rate", "-1"}, envOf(nil))
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

func TestEmplacementIDDoesNotRequireNameOrPosition(t *testing.T) {
	cfg := mustLoad(t, []string{"--addr", "h:1", "--token", "t", "--insecure", "--emplacement-id", "emp-9"}, nil)
	if cfg.Emplacement.EmplacementID != "emp-9" {
		t.Errorf("id = %q", cfg.Emplacement.EmplacementID)
	}
}

func TestMobileValidation(t *testing.T) {
	cfg := mustLoad(t, minimal("--mobile"), nil)
	if !cfg.Mobile() || cfg.Ownship.Path != PathCircle {
		t.Errorf("--mobile defaults: %+v", cfg)
	}

	// PLATFORM-relative boresight needs a platform with a heading.
	y := writeYAML(t, `
connection: {addr: h:1, token: t, insecure: true}
emplacement: {lat: 1, lon: 1, azimuthReference: platform}
`)
	if _, err := Load([]string{"--config", y}, envOf(nil)); err == nil || !strings.Contains(err.Error(), "azimuthReference") {
		t.Errorf("PLATFORM on a FIXED emplacement must fail, got %v", err)
	}
	cfg = mustLoad(t, []string{"--config", y, "--mobile"}, nil)
	if cfg.Emplacement.AzimuthReference != AzimuthPlatform {
		t.Errorf("AzimuthReference = %q", cfg.Emplacement.AzimuthReference)
	}

	y2 := writeYAML(t, `
connection: {addr: h:1, token: t, insecure: true}
emplacement: {lat: 1, lon: 1, mobility: bogus}
`)
	if _, err := Load([]string{"--config", y2}, envOf(nil)); err == nil || !strings.Contains(err.Error(), "mobility") {
		t.Errorf("bad mobility must fail, got %v", err)
	}

	// Ownship settings are ignored (not validated) for a fixed site.
	y3 := writeYAML(t, `
connection: {addr: h:1, token: t, insecure: true}
emplacement: {lat: 1, lon: 1}
ownship: {path: waypoints}
`)
	mustLoad(t, []string{"--config", y3}, nil)
}

func TestClassification(t *testing.T) {
	for _, in := range []string{"unclassified", "Confidential", "SECRET", "top_secret"} {
		y := writeYAML(t, "connection: {addr: h:1, token: t, insecure: true}\nemplacement: {lat: 1, lon: 1}\nsensor: {classification: "+in+"}\n")
		got := mustLoad(t, []string{"--config", y}, nil)
		if got.Sensor.Classification != strings.ToUpper(in) {
			t.Errorf("classification %q -> %q", in, got.Sensor.Classification)
		}
	}
	y := writeYAML(t, "connection: {addr: h:1, token: t, insecure: true}\nemplacement: {lat: 1, lon: 1}\nsensor: {classification: cosmic}\n")
	if _, err := Load([]string{"--config", y}, envOf(nil)); err == nil || !strings.Contains(err.Error(), "classification") {
		t.Errorf("unknown classification must fail, got %v", err)
	}
}

func TestEmitterValidation(t *testing.T) {
	const hdr = "connection: {addr: h:1, token: t, insecure: true}\nemplacement: {lat: 1, lon: 1}\nemitters:\n"
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
			_, err := Load([]string{"--config", writeYAML(t, hdr+tc.body)}, envOf(nil))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want mention of %q", err, tc.want)
			}
		})
	}

	t.Run("defaults: motion static, environment GROUND", func(t *testing.T) {
		body := hdr + "  - {id: a, emitterType: X, frequencyHz: 1e9, start: {lat: 1, lon: 1}}\n  - {id: b, emitterType: X, frequencyHz: 1e9, start: {lat: 1, lon: 1}, trackEnvironment: land}\n"
		cfg := mustLoad(t, []string{"--config", writeYAML(t, body)}, nil)
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

func TestEmitterCount(t *testing.T) {
	const hdr = "connection: {addr: h:1, token: t, insecure: true}\nemplacement: {lat: 1, lon: 1}\nemitters:\n"
	two := "  - {id: a, emitterType: X, frequencyHz: 1e9, start: {lat: 1, lon: 1}}\n  - {id: b, emitterType: X, frequencyHz: 1e9, start: {lat: 1, lon: 1}}\n"
	y := writeYAML(t, hdr+two)

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

func TestSeed(t *testing.T) {
	cfg := mustLoad(t, minimal(), nil)
	if !cfg.SeedGenerated || cfg.Seed == 0 {
		t.Errorf("unset seed must be generated: seed=%d generated=%v", cfg.Seed, cfg.SeedGenerated)
	}
	cfg = mustLoad(t, minimal("--seed", "0"), nil)
	if cfg.SeedGenerated || cfg.Seed != 0 {
		t.Errorf("explicit --seed 0 must be kept: seed=%d generated=%v", cfg.Seed, cfg.SeedGenerated)
	}
	cfg = mustLoad(t, minimal("--seed", "-42"), nil)
	if cfg.SeedGenerated || cfg.Seed != -42 {
		t.Errorf("seed = %d generated=%v", cfg.Seed, cfg.SeedGenerated)
	}
}

func TestParseProblems(t *testing.T) {
	if _, err := Load([]string{"--help"}, envOf(nil)); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("--help err = %v, want flag.ErrHelp", err)
	}
	if u := Usage(); !strings.Contains(u, "--send-timestamps") && !strings.Contains(u, "-send-timestamps") {
		t.Errorf("Usage() lacks flags: %q", u)
	}
	if _, err := Load([]string{"--no-such-flag"}, envOf(nil)); err == nil {
		t.Error("unknown flag must fail")
	}
	if _, err := Load(minimal("--config", filepath.Join(t.TempDir(), "missing.yaml")), envOf(nil)); err == nil {
		t.Error("missing config file must fail")
	}
	if _, err := Load(minimal("--config", writeYAML(t, "sensor: {rat: 2}\n")), envOf(nil)); err == nil || !strings.Contains(err.Error(), "rat") {
		t.Errorf("unknown YAML key must fail naming it, got %v", err)
	}
	if _, err := Load(minimal("--config", writeYAML(t, "")), envOf(nil)); err != nil {
		t.Errorf("empty YAML file is fine: %v", err)
	}
	if _, err := Load(minimal("stray"), envOf(nil)); err == nil {
		t.Error("positional argument must fail")
	}
	// nil env is tolerated.
	if _, err := Load(minimal(), nil); err != nil {
		t.Errorf("nil env: %v", err)
	}
}

func TestTokenNeverLeaks(t *testing.T) {
	// Errors from every failure path must not contain the token.
	bad := [][]string{
		minimal("--pd", "9"),
		minimal("--tls"),
		minimal("--config", writeYAML(t, "sensor: {bogus: 1}\n")),
		minimal("--config", writeYAML(t, "connection: {token: "+testToken+"}\nsensor: {rate: [1]}\n")),
		minimal(testToken), // stray positional argument that happens to be the token
		minimal("--duration", testToken),
	}
	for _, args := range bad {
		_, err := Load(args, envOf(map[string]string{"EW_SENSOR_TOKEN": testToken}))
		if err == nil {
			t.Fatalf("expected error for %v", args)
		}
		if strings.Contains(err.Error(), testToken) {
			t.Errorf("error leaks token: %v", err)
		}
	}

	cfg := mustLoad(t, minimal(), nil)
	for _, s := range []string{
		cfg.Connection.String(),
		cfg.Connection.RedactedToken(),
		// every formatting verb a logger might use
		sprint("%v", cfg), sprint("%+v", cfg), sprint("%#v", cfg),
		sprint("%v", cfg.Connection), sprint("%+v", &cfg.Connection), sprint("%#v", cfg.Connection),
	} {
		if strings.Contains(s, testToken) {
			t.Errorf("formatted output leaks token: %s", s)
		}
	}
	if got := cfg.Connection.RedactedToken(); len(strings.TrimSuffix(got, "...")) > 8 || !strings.HasPrefix(testToken, strings.TrimSuffix(got, "...")) {
		t.Errorf("RedactedToken() = %q, want a prefix of at most 8 chars", got)
	}
	short := Connection{Token: "abcd"}
	if r := short.RedactedToken(); strings.Contains(r, "abcd") {
		t.Errorf("short token must not be shown whole: %q", r)
	}
	if r := (Connection{}).RedactedToken(); r == "" {
		t.Error("empty token should render a placeholder")
	}
}

func sprint(format string, a ...any) string { return fmt.Sprintf(format, a...) }

func TestRateAndEmitterBounds(t *testing.T) {
	cfg := mustLoad(t, minimal("--rate", "100", "--emitters", "1000"), nil)
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

func TestHeartbeatIntervalFlag(t *testing.T) {
	cfg := mustLoad(t, minimal("--heartbeat-interval", "20s"), nil)
	if cfg.Sensor.HeartbeatInterval != 20*time.Second || cfg.Sensor.HeartbeatSeconds() != 20 || cfg.Sensor.HeartbeatEvery() != 20*time.Second {
		t.Fatalf("heartbeat = %v / %d / %v", cfg.Sensor.HeartbeatInterval, cfg.Sensor.HeartbeatSeconds(), cfg.Sensor.HeartbeatEvery())
	}
	// A hand-built Config with none takes the default.
	if s := (Sensor{}); s.HeartbeatSeconds() != 5 || s.HeartbeatEvery() != 5*time.Second {
		t.Fatalf("zero value = %d / %v", s.HeartbeatSeconds(), s.HeartbeatEvery())
	}
}

func TestVerboseFlagAndYAML(t *testing.T) {
	base := []string{"--addr", "h:1", "--token", "t", "--insecure", "--lat", "1", "--lon", "2"}
	if mustLoad(t, base, nil).Verbose {
		t.Error("verbose must default to false")
	}
	if !mustLoad(t, append([]string{"--verbose"}, base...), nil).Verbose {
		t.Error("--verbose did not set Verbose")
	}
	y := writeYAML(t, "verbose: true\n")
	if !mustLoad(t, append([]string{"--config", y}, base...), nil).Verbose {
		t.Error("YAML verbose: true did not set Verbose")
	}
	if mustLoad(t, append([]string{"--config", y, "--verbose=false"}, base...), nil).Verbose {
		t.Error("--verbose=false must override the YAML")
	}
}
