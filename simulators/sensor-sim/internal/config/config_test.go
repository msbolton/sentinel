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

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// baseArgs is the smallest valid command line for a radar.
func baseArgs(extra ...string) []string {
	return append([]string{
		"--kind", "radar", "--addr", "ingest:50051", "--token", "si_flagtoken", "--insecure",
		"--lat", "45", "--lon", "10",
	}, extra...)
}

func scenarioYAML() string {
	return `scenario:
  epoch: 2026-10-21T12:00:00Z
  targets:
    - id: t1
      kind: vehicle
      speedKts: 20
      loop: true
      waypoints:
        - {lat: 45.0, lon: 10.0}
        - {lat: 45.0, lon: 10.1}
`
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sim.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadPrecedence(t *testing.T) {
	yamlDoc := `connection:
  addr: yaml:1
  token: si_yamltoken
  insecure: true
sensor:
  kind: eoir
  name: from-yaml
  lat: 44
  lon: 9
  heightAboveGroundM: 12
  rate: 5
  pd: 0.5
  rangeKm: 7
seed: 11
terrain:
  cacheDir: /tmp/yaml-terrain
` + scenarioYAML()
	file := writeFile(t, yamlDoc)

	tests := []struct {
		name  string
		args  []string
		env   map[string]string
		check func(t *testing.T, c Config)
	}{
		{"yaml only", []string{"--config", file}, nil, func(t *testing.T, c Config) {
			if c.Connection.Token != "si_yamltoken" || c.Connection.Addr != "yaml:1" {
				t.Errorf("connection %v", c.Connection)
			}
			if c.Sensor.Kind != "eoir" || c.Sensor.Name != "from-yaml" || c.Sensor.Rate != 5 || c.Sensor.Pd != 0.5 || c.Sensor.RangeKm != 7 {
				t.Errorf("sensor %+v", c.Sensor)
			}
			if c.Seed != 11 || c.SeedGenerated {
				t.Errorf("seed %d generated=%v", c.Seed, c.SeedGenerated)
			}
			if c.Terrain.CacheDir != "/tmp/yaml-terrain" || c.Terrain.SourceURL != DefaultTerrainURL {
				t.Errorf("terrain %+v", c.Terrain)
			}
			if len(c.Scenario.Targets) != 1 || c.Scenario.Epoch.IsZero() {
				t.Errorf("scenario %+v", c.Scenario)
			}
		}},
		{"fallback env token beats yaml", []string{"--config", file}, map[string]string{EnvFallbackToken: "si_fallback"}, func(t *testing.T, c Config) {
			if c.Connection.Token != "si_fallback" {
				t.Errorf("token %q", c.Connection.Token)
			}
		}},
		{"sim env token beats fallback", []string{"--config", file}, map[string]string{EnvFallbackToken: "si_fallback", EnvToken: "si_simtoken"}, func(t *testing.T, c Config) {
			if c.Connection.Token != "si_simtoken" {
				t.Errorf("token %q", c.Connection.Token)
			}
		}},
		{"flag token beats both env", []string{"--config", file, "--token", "si_flag"}, map[string]string{EnvFallbackToken: "si_fallback", EnvToken: "si_simtoken"}, func(t *testing.T, c Config) {
			if c.Connection.Token != "si_flag" {
				t.Errorf("token %q", c.Connection.Token)
			}
		}},
		{"env addr beats yaml", []string{"--config", file}, map[string]string{EnvAddr: "env:2"}, func(t *testing.T, c Config) {
			if c.Connection.Addr != "env:2" {
				t.Errorf("addr %q", c.Connection.Addr)
			}
		}},
		{"flag addr beats env", []string{"--config", file, "--addr", "flag:3"}, map[string]string{EnvAddr: "env:2"}, func(t *testing.T, c Config) {
			if c.Connection.Addr != "flag:3" {
				t.Errorf("addr %q", c.Connection.Addr)
			}
		}},
		{"flag equal to the default still beats yaml", []string{"--config", file, "--rate", "1"}, nil, func(t *testing.T, c Config) {
			if c.Sensor.Rate != 1 {
				t.Errorf("rate %v, want the explicit 1", c.Sensor.Rate)
			}
		}},
		{"kind flag beats yaml and alt replaces height", []string{"--config", file, "--kind", "Radar", "--alt", "250"}, nil, func(t *testing.T, c Config) {
			if c.Sensor.Kind != "radar" {
				t.Errorf("kind %q", c.Sensor.Kind)
			}
			if c.Sensor.Alt == nil || *c.Sensor.Alt != 250 {
				t.Errorf("alt %v", c.Sensor.Alt)
			}
			// The YAML's height is still there; Validate rejects the pair.
			if c.Sensor.HeightAboveGroundM != nil {
				t.Errorf("--alt should have replaced the YAML height above ground, got %v", *c.Sensor.HeightAboveGroundM)
			}
		}},
		{"explicit seed zero", []string{"--config", file, "--seed", "0"}, nil, func(t *testing.T, c Config) {
			if c.Seed != 0 || c.SeedGenerated {
				t.Errorf("seed %d generated=%v", c.Seed, c.SeedGenerated)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Load(tc.args, envOf(tc.env))
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, c)
		})
	}
}

func TestLoadFlagsAndDefaults(t *testing.T) {
	file := writeFile(t, scenarioYAML())
	c, err := Load(baseArgs("--config", file, "--name", "north", "--height-above-ground", "6",
		"--terrain-cache", "/tmp/terrain", "--boresight", "-90", "--width", "60", "--range-km", "25", "--min-range-km", "1",
		"--elev-min", "-5", "--elev-max", "40", "--pd", "0.8", "--sigma-az", "0.2", "--sigma-el", "0.3", "--sigma-range-m", "7",
		"--band", "lwir", "--heartbeat-interval", "9s", "--duration", "2m", "--verbose", "--seed", "3"), nil)
	if err != nil {
		t.Fatal(err)
	}
	s := c.Sensor
	if s.Name != "north" || s.Boresight != 270 || s.Width != 60 || s.RangeKm != 25 || s.MinRangeKm != 1 {
		t.Errorf("geometry %+v", s)
	}
	if s.HeightAboveGroundM == nil || *s.HeightAboveGroundM != 6 || s.Alt != nil {
		t.Errorf("height %v alt %v", s.HeightAboveGroundM, s.Alt)
	}
	if s.ElevationMinDeg == nil || *s.ElevationMinDeg != -5 || s.ElevationMaxDeg == nil || *s.ElevationMaxDeg != 40 {
		t.Errorf("elevation %v %v", s.ElevationMinDeg, s.ElevationMaxDeg)
	}
	if s.Pd != 0.8 || s.SigmaAzDeg != 0.2 || s.SigmaElDeg != 0.3 || s.SigmaRangeM != 7 || s.Band != "LWIR" || s.HeartbeatInterval != 9*time.Second {
		t.Errorf("model %+v", s)
	}
	if c.Duration != 2*time.Minute || !c.Verbose || c.Seed != 3 || c.Terrain.CacheDir != "/tmp/terrain" {
		t.Errorf("run %+v", c)
	}
	if c.Terrain.SourceURL != "https://copernicus-dem-30m.s3.amazonaws.com" {
		t.Errorf("terrain url %q", c.Terrain.SourceURL)
	}

	// Defaults: a seed is made up when none is given.
	d, err := Load(baseArgs("--config", file), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !d.SeedGenerated || d.Sensor.Rate <= 0 || d.Sensor.Pd <= 0 || d.Sensor.HeartbeatInterval != DefaultHeartbeatInterval {
		t.Errorf("defaults %+v", d)
	}
}

func TestLoadErrors(t *testing.T) {
	file := writeFile(t, scenarioYAML())
	if _, err := Load([]string{"--help"}, nil); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("--help: %v", err)
	}
	if _, err := Load(baseArgs("--config", file, "stray"), nil); err == nil || strings.Contains(err.Error(), "stray") {
		t.Errorf("positional argument: %v", err)
	}
	if _, err := Load(baseArgs("--config", "/nonexistent/sim.yaml"), nil); err == nil {
		t.Error("missing config file accepted")
	}
	bad := writeFile(t, "sensor:\n  bogus: 1\n")
	if _, err := Load(baseArgs("--config", bad), nil); err == nil {
		t.Error("unknown YAML field accepted")
	}
	// A token that appears in an error is redacted.
	_, err := Load([]string{"--token", "si_secretvalue", "--rate", "si_secretvalue"}, nil)
	if err == nil || strings.Contains(err.Error(), "si_secretvalue") {
		t.Errorf("token leaked into %v", err)
	}
}

func TestTokenNeverPrinted(t *testing.T) {
	c := Connection{Addr: "a:1", Token: "si_supersecrettoken", Insecure: true}
	for _, s := range []string{fmt.Sprint(c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c), fmt.Sprint(Config{Connection: c})} {
		if strings.Contains(s, "supersecret") {
			t.Errorf("token printed: %s", s)
		}
	}
}

func f64(v float64) *float64 { return &v }

func validRadar() Config {
	var c Config
	c.Connection = Connection{Addr: "ingest:50051", Token: "si_x", Insecure: true}
	c.Sensor = Sensor{
		Kind: "radar", Name: "r1", Lat: 45, Lon: 10, Width: 90, RangeKm: 10,
		Rate: 1, Pd: 0.9, SigmaAzDeg: 0.5, SigmaElDeg: 0.5, SigmaRangeM: 10, HeartbeatInterval: 5 * time.Second,
	}
	c.Scenario.Targets = nil
	return c
}

func TestValidate(t *testing.T) {
	file := writeFile(t, scenarioYAML())
	loaded, err := Load(baseArgs("--config", file), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.Validate(); err != nil {
		t.Fatalf("loaded config does not validate: %v", err)
	}
	eoir := func(c *Config) { c.Sensor.Kind, c.Sensor.Band = "eoir", "VISIBLE" }

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"valid", func(*Config) {}, ""},
		{"valid eoir with alt", func(c *Config) { eoir(c); c.Sensor.Alt = f64(30); c.Terrain.CacheDir = "/tmp/t" }, ""},
		{"eoir needs terrain", func(c *Config) { eoir(c); c.Sensor.Alt = f64(30) }, "an eoir sensor needs terrain"},
		{"radar with terrain needs a height", func(c *Config) { c.Terrain.CacheDir = "/tmp/t" }, "a radar with terrain needs --alt or --height-above-ground"},
		{"radar with terrain and alt", func(c *Config) { c.Terrain.CacheDir = "/tmp/t"; c.Sensor.Alt = f64(300) }, ""},
		{"radar with terrain and height", func(c *Config) { c.Terrain.CacheDir = "/tmp/t"; c.Sensor.HeightAboveGroundM = f64(10) }, ""},
		{"radar without terrain needs no height", func(*Config) {}, ""},
		{"height too tall", func(c *Config) { c.Terrain.CacheDir = "/tmp/t"; c.Sensor.HeightAboveGroundM = f64(1001) }, "height-above-ground"},
		// The elevation window is checked after the kind's defaults apply: a
		// camera's is -15..+75 and a radar's -90..+90.
		{"camera elev-min alone above the default max", func(c *Config) {
			eoir(c)
			c.Sensor.Alt, c.Terrain.CacheDir = f64(30), "/tmp/t"
			c.Sensor.ElevationMinDeg = f64(80)
		}, "elevation window"},
		{"camera elev-max alone below the default min", func(c *Config) {
			eoir(c)
			c.Sensor.Alt, c.Terrain.CacheDir = f64(30), "/tmp/t"
			c.Sensor.ElevationMaxDeg = f64(-20)
		}, "elevation window"},
		{"camera elev-min alone inside the default", func(c *Config) {
			eoir(c)
			c.Sensor.Alt, c.Terrain.CacheDir = f64(30), "/tmp/t"
			c.Sensor.ElevationMinDeg = f64(-5)
		}, ""},
		{"radar elev-min alone at the top", func(c *Config) { c.Sensor.ElevationMinDeg = f64(90) }, "elevation window"},
		{"radar elev-min alone", func(c *Config) { c.Sensor.ElevationMinDeg = f64(80) }, ""},
		{"valid eoir with height", func(c *Config) {
			eoir(c)
			c.Sensor.HeightAboveGroundM = f64(5)
			c.Terrain.CacheDir = "/tmp/t"
		}, ""},
		{"kind empty", func(c *Config) { c.Sensor.Kind = "" }, "kind"},
		{"kind wrong", func(c *Config) { c.Sensor.Kind = "lidar" }, "kind"},
		{"eoir needs a height", func(c *Config) { eoir(c) }, "alt"},
		{"eoir alt and height", func(c *Config) { eoir(c); c.Sensor.Alt = f64(1); c.Sensor.HeightAboveGroundM = f64(1) }, "mutually exclusive"},
		{"height needs terrain", func(c *Config) { eoir(c); c.Sensor.HeightAboveGroundM = f64(5) }, "terrain"},
		{"negative height", func(c *Config) {
			eoir(c)
			c.Sensor.HeightAboveGroundM = f64(-1)
			c.Terrain.CacheDir = "/tmp/t"
		}, "height-above-ground"},
		{"bad band", func(c *Config) { eoir(c); c.Sensor.Alt = f64(1); c.Terrain.CacheDir = "/tmp/t"; c.Sensor.Band = "UV" }, "band"},
		{"sigma az zero", func(c *Config) { c.Sensor.SigmaAzDeg = 0 }, "sigma-az"},
		{"sigma el negative", func(c *Config) { c.Sensor.SigmaElDeg = -1 }, "sigma-el"},
		{"sigma range zero", func(c *Config) { c.Sensor.SigmaRangeM = 0 }, "sigma-range-m"},
		{"pd zero", func(c *Config) { c.Sensor.Pd = 0 }, "pd"},
		{"pd above one", func(c *Config) { c.Sensor.Pd = 1.1 }, "pd"},
		{"rate zero", func(c *Config) { c.Sensor.Rate = 0 }, "rate"},
		{"rate too high", func(c *Config) { c.Sensor.Rate = MaxRate + 1 }, "rate"},
		{"width zero", func(c *Config) { c.Sensor.Width = 0 }, "width"},
		{"range zero", func(c *Config) { c.Sensor.RangeKm = 0 }, "range-km"},
		{"min range above range", func(c *Config) { c.Sensor.MinRangeKm = 20 }, "min-range-km"},
		{"elevation order", func(c *Config) { c.Sensor.ElevationMinDeg, c.Sensor.ElevationMaxDeg = f64(30), f64(10) }, "elevation window"},
		{"elevation out of range", func(c *Config) { c.Sensor.ElevationMaxDeg = f64(95) }, "elev-max"},
		{"lat unset", func(c *Config) { c.Sensor.Lat, c.Sensor.Lon = 0, 0 }, "lat/lon"},
		{"lat out of range", func(c *Config) { c.Sensor.Lat = 91 }, "lat"},
		{"name blank", func(c *Config) { c.Sensor.Name = " " }, "name"},
		{"heartbeat fractional", func(c *Config) { c.Sensor.HeartbeatInterval = 1500 * time.Millisecond }, "heartbeat"},
		{"heartbeat too long", func(c *Config) { c.Sensor.HeartbeatInterval = 301 * time.Second }, "heartbeat"},
		{"negative duration", func(c *Config) { c.Duration = -time.Second }, "duration"},
		{"no addr", func(c *Config) { c.Connection.Addr = "" }, "addr"},
		{"addr without port", func(c *Config) { c.Connection.Addr = "ingest" }, "host:port"},
		{"no token", func(c *Config) { c.Connection.Token = "" }, "token"},
		{"no transport", func(c *Config) { c.Connection.Insecure = false }, "--insecure and --tls"},
		{"both transports", func(c *Config) { c.Connection.TLS = true }, "mutually exclusive"},
		{"ca without tls", func(c *Config) { c.Connection.CA = "ca.pem" }, "--ca"},
		{"cert without key", func(c *Config) { c.Connection.Insecure, c.Connection.TLS, c.Connection.Cert = false, true, "c.pem" }, "--key"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validRadar()
			c.Scenario = loaded.Scenario
			tc.mutate(&c)
			err := c.Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}

	t.Run("scenario errors are reported", func(t *testing.T) {
		c := validRadar() // no targets
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "target") {
			t.Fatalf("error %v, want a missing-target error", err)
		}
	})
	t.Run("several problems at once", func(t *testing.T) {
		c := validRadar()
		c.Scenario = loaded.Scenario
		c.Sensor.SigmaAzDeg, c.Sensor.SigmaElDeg = 0, 0
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "sigma-az") || !strings.Contains(err.Error(), "sigma-el") {
			t.Fatalf("error %v, want both sigmas", err)
		}
	})
}

func TestUsageMentionsEveryFlag(t *testing.T) {
	u := Usage()
	for _, f := range []string{"config", "addr", "token", "insecure", "tls", "ca", "cert", "key", "kind", "name", "lat", "lon", "alt",
		"height-above-ground", "boresight", "width", "range-km", "min-range-km", "elev-min", "elev-max", "rate", "pd",
		"sigma-az", "sigma-el", "sigma-range-m", "band", "seed", "duration", "heartbeat-interval", "verbose", "terrain-cache", "terrain-url"} {
		if !strings.Contains(u, "-"+f) {
			t.Errorf("usage lacks --%s", f)
		}
	}
}

func TestElevationWindow(t *testing.T) {
	tests := []struct {
		name   string
		kind   string
		lo, hi *float64
		wantLo float64
		wantHi float64
	}{
		{"camera defaults", KindEOIR, nil, nil, DefaultCameraElevMinDeg, DefaultCameraElevMaxDeg},
		{"camera min only", KindEOIR, f64(-5), nil, -5, DefaultCameraElevMaxDeg},
		{"camera max only", KindEOIR, nil, f64(30), DefaultCameraElevMinDeg, 30},
		{"radar unlimited", KindRadar, nil, nil, -90, 90},
		{"radar both", KindRadar, f64(-2), f64(40), -2, 40},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := Sensor{Kind: tc.kind, ElevationMinDeg: tc.lo, ElevationMaxDeg: tc.hi}
			lo, hi := s.ElevationWindow()
			if lo != tc.wantLo || hi != tc.wantHi {
				t.Fatalf("window %v..%v, want %v..%v", lo, hi, tc.wantLo, tc.wantHi)
			}
		})
	}
}

// --elev-min 80 alone on a camera leaves the default max of 75 above it, so
// Load refuses it rather than build a camera that can see nothing.
func TestLoadRefusesCameraElevMinAboveDefaultMax(t *testing.T) {
	file := writeFile(t, scenarioYAML())
	_, err := Load([]string{"--kind", "eoir", "--addr", "ingest:50051", "--token", "si_flagtoken", "--insecure",
		"--lat", "45", "--lon", "10", "--alt", "100", "--terrain-cache", "/tmp/t", "--config", file, "--elev-min", "80"}, nil)
	if err == nil || !strings.Contains(err.Error(), "elevation window") {
		t.Fatalf("Load = %v, want an elevation window error", err)
	}
}

func TestScenarioFileInclude(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("shared/scenario.yaml", `epoch: 2026-10-21T00:00:00Z
targets:
  - id: v1
    kind: vehicle
    speedKts: 20
    loop: true
    waypoints:
      - {lat: 45.0, lon: 10.0}
      - {lat: 45.0, lon: 10.1}
`)
	t.Run("relative to the config file", func(t *testing.T) {
		cfg := write("radar.yaml", "scenarioFile: shared/scenario.yaml\n")
		c, err := Load(baseArgs("--config", cfg), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(c.Scenario.Targets) != 1 || c.Scenario.Targets[0].ID != "v1" {
			t.Fatalf("scenario %+v", c.Scenario)
		}
		if want := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC); !c.Scenario.Epoch.Equal(want) {
			t.Fatalf("epoch %v, want %v", c.Scenario.Epoch, want)
		}
	})
	t.Run("absolute path", func(t *testing.T) {
		cfg := write("abs.yaml", "scenarioFile: "+filepath.Join(dir, "shared/scenario.yaml")+"\n")
		if _, err := Load(baseArgs("--config", cfg), nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("not both", func(t *testing.T) {
		cfg := write("both.yaml", "scenarioFile: shared/scenario.yaml\n"+scenarioYAML())
		if _, err := Load(baseArgs("--config", cfg), nil); err == nil || !strings.Contains(err.Error(), "scenarioFile") {
			t.Fatalf("Load = %v, want a scenario/scenarioFile conflict", err)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		cfg := write("missing.yaml", "scenarioFile: nope.yaml\n")
		if _, err := Load(baseArgs("--config", cfg), nil); err == nil || !strings.Contains(err.Error(), "nope.yaml") {
			t.Fatalf("Load = %v, want an error naming the file", err)
		}
	})
	t.Run("unknown keys refused", func(t *testing.T) {
		write("bad-scenario.yaml", "epoch: 2026-10-21T00:00:00Z\nbogus: 1\n")
		cfg := write("bad.yaml", "scenarioFile: bad-scenario.yaml\n")
		if _, err := Load(baseArgs("--config", cfg), nil); err == nil || !strings.Contains(err.Error(), "bogus") {
			t.Fatalf("Load = %v, want an unknown-field error", err)
		}
	})
}

// The bundled examples are for the plaintext dev listener, so they choose
// --insecure themselves (Compose's `up` passes no transport flag); a
// transport on the command line still replaces their choice.
func TestExamplesDefaultToInsecure(t *testing.T) {
	for _, tc := range []struct{ kind, file string }{{"radar", "radar.yaml"}, {"eoir", "camera.yaml"},
		{"esm", "esm-coastal.yaml"}, {"esm", "esm-mobile.yaml"}} {
		t.Run(tc.file, func(t *testing.T) {
			base := []string{"--kind", tc.kind, "--addr", "localhost:6200", "--token", "si_exampletoken",
				"--config", filepath.Join("..", "..", "examples", tc.file)}
			c, err := Load(base, nil)
			if err != nil {
				t.Fatalf("with no transport flag: %v", err)
			}
			if !c.Connection.Insecure || c.Connection.TLS {
				t.Errorf("connection %v, want insecure from the example", c.Connection)
			}
			c, err = Load(append(base, "--tls", "--ca", "x"), nil)
			if err != nil {
				t.Fatalf("--tls --ca x: %v", err)
			}
			if c.Connection.Insecure || !c.Connection.TLS || c.Connection.CA != "x" {
				t.Errorf("--tls --ca x: connection %v, want TLS with CA x and not insecure", c.Connection)
			}
		})
	}
}
