package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/sentinel/go-common/geo"
	"github.com/sentinel/go-common/terrain"

	"github.com/sentinel/sensor-sim/internal/config"
	"github.com/sentinel/sensor-sim/internal/scenario"
)

func noEnv(string) string { return "" }

func TestExitCodesBeforeConnecting(t *testing.T) {
	cases := []struct {
		name string
		args []string
		code int
		out  []string
	}{
		{"help", []string{"--help"}, 0, []string{"-kind", "-terrain-cache", "-heartbeat-interval"}},
		{"unknown flag", []string{"--bogus"}, 1, []string{"--help", "bogus"}},
		{"bad flag value", []string{"--rate", "fast"}, 1, []string{"--help", "rate"}},
		{"missing token", []string{"--kind", "radar", "--addr", "localhost:1", "--insecure", "--lat", "1", "--lon", "1",
			"--config", "../../examples/radar.yaml"}, 1, []string{"token is required", config.EnvToken}},
		{"eoir without terrain", []string{"--kind", "eoir", "--addr", "localhost:1", "--token", "si_secretsecretsecret", "--insecure",
			"--config", "../../examples/camera.yaml", "--terrain-cache", ""}, 1, []string{"needs terrain"}},
		{"esm help names the kind", []string{"--help"}, 0, []string{"esm", "-emitters", "-mobile", "-emplacement-id"}},
		{"esm missing addr and token", []string{"--kind", "esm", "--insecure", "--name", "x", "--lat", "1", "--lon", "1"}, 1, []string{"addr is required"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, noEnv, &stdout, &stderr); code != tc.code {
				t.Fatalf("exit %d, want %d; stderr: %s", code, tc.code, stderr.String())
			}
			out := stdout.String() + stderr.String()
			for _, w := range tc.out {
				if !strings.Contains(out, w) {
					t.Fatalf("output lacks %q:\n%s", w, out)
				}
			}
			if strings.Contains(out, "si_secretsecretsecret") {
				t.Fatal("token printed")
			}
		})
	}
}

// The token can come from the environment instead of the flag.
func TestTokenFromEnvironment(t *testing.T) {
	var stdout, stderr bytes.Buffer
	env := func(k string) string {
		if k == config.EnvToken {
			return "si_fromenvironment"
		}
		return ""
	}
	// A bad --ca stops it after the configuration loaded, before connecting.
	args := []string{"--kind", "radar", "--addr", "localhost:1", "--tls", "--ca", "/nonexistent/ca.pem",
		"--config", "../../examples/radar.yaml", "--terrain-cache", "", "--alt", "100"}
	if code := run(args, env, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d, want 1; stderr: %s", code, stderr.String())
	}
	if s := stderr.String(); strings.Contains(s, "token is required") || !strings.Contains(s, "--ca") {
		t.Fatalf("stderr: %s", s)
	}
	if strings.Contains(stderr.String(), "si_fromenvironment") {
		t.Fatal("token logged")
	}
}

// ew-sensor's exit-code test, under --kind esm: a bad --ca stops it before
// connecting, without logging the token.
func TestESMBadCAExitsFatal(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{"--kind", "esm", "--addr", "localhost:1", "--token", "si_secretsecretsecret", "--tls", "--ca", "/nonexistent/ca.pem",
		"--name", "x", "--lat", "1", "--lon", "1"}
	if code := run(args, noEnv, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d, want 1; stderr: %s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "si_secretsecretsecret") {
		t.Fatal("token logged")
	}
}

// An ESM sensor starts without terrain: the bundled example loads with no
// terrain cache, and the run gets as far as the credentials. Terrain is not
// even opened when a cache is given.
func TestESMRunsWithoutTerrain(t *testing.T) {
	for _, extra := range [][]string{{"--terrain-cache", ""}, {"--terrain-cache", "/nonexistent/terrain"}} {
		var stdout, stderr bytes.Buffer
		args := append([]string{"--kind", "esm", "--addr", "localhost:1", "--token", "si_secretsecretsecret", "--tls", "--ca", "/nonexistent/ca.pem",
			"--config", "../../examples/esm-coastal.yaml"}, extra...)
		if code := run(args, noEnv, &stdout, &stderr); code != 1 {
			t.Fatalf("%v: exit %d, want 1; stderr: %s", extra, code, stderr.String())
		}
		s := stderr.String()
		if !strings.Contains(s, "--ca") || strings.Contains(s, "terrain on") || strings.Contains(s, "needs terrain") {
			t.Fatalf("%v: stderr: %s", extra, s)
		}
	}
}

// ew-sensor's environment names still work, with one warning each.
func TestDeprecatedEnvironmentWarnsOnce(t *testing.T) {
	var stdout, stderr bytes.Buffer
	env := func(k string) string {
		switch k {
		case config.EnvDeprecatedToken:
			return "si_fromtheoldname"
		case config.EnvDeprecatedAddr:
			return "localhost:1"
		}
		return ""
	}
	args := []string{"--kind", "esm", "--tls", "--ca", "/nonexistent/ca.pem", "--lat", "1", "--lon", "1"}
	if code := run(args, env, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d, want 1; stderr: %s", code, stderr.String())
	}
	s := stderr.String()
	if strings.Contains(s, "token is required") || strings.Contains(s, "addr is required") || !strings.Contains(s, "--ca") {
		t.Fatalf("the deprecated variables were not used: %s", s)
	}
	for _, k := range []string{config.EnvDeprecatedToken, config.EnvDeprecatedAddr} {
		if n := strings.Count(s, k+" is deprecated"); n != 1 {
			t.Fatalf("%d warnings for %s, want 1:\n%s", n, k, s)
		}
	}
	if strings.Contains(s, "si_fromtheoldname") {
		t.Fatal("token logged")
	}
}

// The ESM source is the esm sensor on its own stepped clock, at the
// configured site, with no scenario truth.
func TestNewSourceESM(t *testing.T) {
	cfg, err := config.Load([]string{"--kind", "esm", "--addr", "x:1", "--token", "si_x", "--config", "../../examples/esm-mobile.yaml", "--seed", "1"}, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	src, err := sourceFunc(geo.LLA{}, nil)(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !src.Stepped || src.State == nil || src.Truth != nil || src.Detector == nil {
		t.Fatalf("source %+v, want a stepped detector with a state and no truth", src)
	}
	if want := (geo.LLA{Lat: cfg.Sensor.Lat, Lon: cfg.Sensor.Lon, Alt: 18}); src.Site != want {
		t.Fatalf("site %+v, want %+v", src.Site, want)
	}
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	if st := src.State("emp-1", now); st.GetEmplacementId() != "emp-1" || st.Heading == nil {
		t.Fatalf("state %v, want the mobile platform's pose", st)
	}
	var obs int
	for i := 1; i <= 20; i++ {
		obs += len(src.Detector.Detect(context.Background(), now.Add(time.Duration(i)*time.Second), nil))
	}
	if obs == 0 {
		t.Fatal("the mobile example detected nothing in 20 ticks")
	}
}

// The connection's send guide prints exactly these command lines; each must
// load with the bundled examples.
func TestSendGuideCommandLinesLoad(t *testing.T) {
	for _, tc := range []struct{ kind, file string }{{"radar", "radar.yaml"}, {"eoir", "camera.yaml"}, {"esm", "esm-coastal.yaml"}} {
		for _, security := range [][]string{{"--insecure"}, {"--tls", "--ca", "ca.pem"}, {"--tls", "--ca", "ca.pem", "--cert", "client.pem", "--key", "client-key.pem"}} {
			args := append([]string{"--kind", tc.kind, "--addr", "sentinel.example:6200", "--token", "si_guidetoken"}, security...)
			args = append(args, "--config", "../../examples/"+tc.file)
			cfg, err := config.Load(args, noEnv)
			if err != nil {
				t.Fatalf("%s %v: %v", tc.kind, security, err)
			}
			if tc.kind == "esm" {
				if cfg.Sensor.Kind != tc.kind || len(cfg.Emitters) == 0 || cfg.Terrain.CacheDir != "" {
					t.Fatalf("esm: kind %q emitters %d terrain %q", cfg.Sensor.Kind, len(cfg.Emitters), cfg.Terrain.CacheDir)
				}
				continue
			}
			if cfg.Sensor.Kind != tc.kind || cfg.Terrain.CacheDir == "" || cfg.Scenario.Epoch.IsZero() {
				t.Fatalf("%s: kind %q terrain %q epoch %v", tc.kind, cfg.Sensor.Kind, cfg.Terrain.CacheDir, cfg.Scenario.Epoch)
			}
		}
	}
}

// The bundled examples are the demo the docs describe: a 360 degree, 10 km
// radar and a 60 degree, 8 km camera at the same site, sharing one scenario
// of at least four vehicles and two drones flying 60 to 150 m above ground.
func TestExamples(t *testing.T) {
	load := func(kind, file string) config.Config {
		t.Helper()
		cfg, err := config.Load([]string{"--kind", kind, "--addr", "x:1", "--token", "si_x", "--insecure",
			"--config", "../../examples/" + file}, noEnv)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		return cfg
	}
	r, c := load("radar", "radar.yaml"), load("eoir", "camera.yaml")
	if r.Sensor.Width != 360 || r.Sensor.RangeKm != 10 || r.Sensor.HeightAboveGroundM == nil {
		t.Errorf("radar %+v", r.Sensor)
	}
	if c.Sensor.Width != 60 || c.Sensor.RangeKm != 8 || c.Sensor.HeightAboveGroundM == nil {
		t.Errorf("camera %+v", c.Sensor)
	}
	if r.Sensor.Lat != c.Sensor.Lat || r.Sensor.Lon != c.Sensor.Lon || r.Sensor.Name == c.Sensor.Name {
		t.Errorf("the radar and camera should share a site under different names: %+v %+v", r.Sensor, c.Sensor)
	}
	if r.ScenarioFile == "" || r.ScenarioFile != c.ScenarioFile {
		t.Errorf("scenario files %q and %q, want one shared file", r.ScenarioFile, c.ScenarioFile)
	}
	vehicles, drones := 0, 0
	for _, tg := range r.Scenario.Targets {
		switch tg.Kind {
		case scenario.KindVehicle:
			vehicles++
		case scenario.KindDrone:
			drones++
			if tg.HeightAboveGroundM < 60 || tg.HeightAboveGroundM > 150 {
				t.Errorf("drone %s flies %v m above ground, want 60 to 150", tg.ID, tg.HeightAboveGroundM)
			}
		}
	}
	if vehicles < 4 || drones < 2 {
		t.Errorf("%d vehicles and %d drones, want at least 4 and 2", vehicles, drones)
	}
}

func TestResolveSite(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ground := terrain.NewStatic(func(float64, float64) float64 { return 300 })
	alt, hag := 120.0, 8.0
	cases := []struct {
		name    string
		s       config.Sensor
		ter     *terrain.Service
		want    float64
		wantErr bool
	}{
		{"alt as given", config.Sensor{Lat: 54, Lon: -3, Alt: &alt}, ground, 120, false},
		{"ground plus height", config.Sensor{Lat: 54, Lon: -3, HeightAboveGroundM: &hag}, ground, 308, false},
		{"radar with neither and no terrain", config.Sensor{Lat: 54, Lon: -3}, nil, 0, false},
		{"height without terrain", config.Sensor{Lat: 54, Lon: -3, HeightAboveGroundM: &hag}, nil, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveSite(context.Background(), tc.s, tc.ter, log)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err %v, want error %v", err, tc.wantErr)
			}
			if err == nil && (got != geo.LLA{Lat: 54, Lon: -3, Alt: tc.want}) {
				t.Fatalf("site %+v, want alt %v", got, tc.want)
			}
		})
	}
}

// The source sees the scenario over the terrain: a parked target stands on
// the ground plus its height above it.
func TestNewSourceUsesTerrain(t *testing.T) {
	cfg := config.Config{
		Sensor: config.Sensor{Kind: config.KindRadar, Name: "r", Lat: 54, Lon: -3, Width: 360, RangeKm: 10,
			Rate: 1, Pd: 1, SigmaAzDeg: 0.1, SigmaElDeg: 0.1, SigmaRangeM: 1},
		Scenario: scenario.Scenario{Targets: []scenario.Target{
			{ID: "d", Kind: "drone", HeightAboveGroundM: 100, Waypoints: []scenario.LatLon{{Lat: 54.01, Lon: -3}}},
		}},
	}
	ground := terrain.NewStatic(func(float64, float64) float64 { return 250 })
	src, err := sourceFunc(geo.LLA{Lat: 54, Lon: -3, Alt: 260}, ground)(cfg)
	if err != nil {
		t.Fatal(err)
	}
	truth, err := src.Truth(context.Background(), time.Now())
	if err != nil || len(truth) != 1 || truth[0].At.Alt != 350 {
		t.Fatalf("truth %+v err %v, want the drone at 350 m", truth, err)
	}
	if obs := src.Detector.Detect(context.Background(), time.Now(), truth); len(obs) != 1 || obs[0].GetPlatform().GetRadar() == nil {
		t.Fatalf("observations %v, want one radar detection", obs)
	}
}
