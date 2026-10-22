// Command sensor-sim simulates a sensor and streams what it detects to
// Sentinel's gRPC ingest service. One process is one sensor (--kind):
//
//   - radar and eoir watch a scenario of moving targets from a fixed site and
//     report polar measurements (the camera's are direction only). A radar
//     and a camera that load the same scenario see the same targets, because
//     the targets' positions are a function of the wall clock and the
//     scenario epoch.
//   - esm is an electronic-support sensor, fixed or mobile, that geolocates
//     simulated RF emitters and reports fixes with an error ellipse, on a
//     seeded simulation clock. It needs no terrain and no scenario.
//
// Exit codes: 0 normal end (--duration elapsed, SIGINT/SIGTERM, --help);
// 1 configuration, usage or other fatal error; 2 Unauthenticated,
// PermissionDenied or the connection's sender-managed emplacement cap.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sentinel/go-common/geo"
	"github.com/sentinel/go-common/terrain"

	"github.com/sentinel/sensor-sim/internal/client"
	"github.com/sentinel/sensor-sim/internal/config"
	"github.com/sentinel/sensor-sim/internal/detect"
	"github.com/sentinel/sensor-sim/internal/eoir"
	"github.com/sentinel/sensor-sim/internal/esm"
	"github.com/sentinel/sensor-sim/internal/radar"
	"github.com/sentinel/sensor-sim/internal/scenario"
)

// terrainStartupWait bounds the terrain work done before streaming: the site's
// ground height and a first look at every target, which on a cold cache
// download their tiles (tens of megabytes each). It matches the terrain
// service's own fetch timeout.
const terrainStartupWait = 300 * time.Second

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, env func(string) string, stdout, stderr io.Writer) int {
	cfg, err := config.Load(args, env)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintf(stdout, "Usage: sensor-sim --kind radar|eoir|esm --addr host:port (--insecure | --tls) --config file [flags]\n\n%s", config.Usage())
		return client.ExitOK
	}
	if err != nil {
		fmt.Fprintf(stderr, "sensor-sim: %v\nRun sensor-sim --help for the flags.\n", err)
		return client.ExitFatal
	}

	level := slog.LevelInfo
	if cfg.Verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
	for _, w := range cfg.Warnings {
		log.Warn(w)
	}
	attrs := []any{
		"kind", cfg.Sensor.Kind,
		"name", cfg.Sensor.Name,
		"addr", cfg.Connection.Addr,
		"tls", cfg.Connection.TLS,
		"token", cfg.Connection.RedactedToken(),
		"seed", cfg.Seed,
		"seed_generated", cfg.SeedGenerated,
	}
	if cfg.ESM() {
		attrs = append(attrs, "emitters", cfg.EmitterCount, "mobile", cfg.Mobile())
	} else {
		attrs = append(attrs, "targets", len(cfg.Scenario.Targets), "epoch", epochOf(cfg.Scenario), "terrain_cache", cfg.Terrain.CacheDir)
	}
	log.Info("sensor-sim starting", append(attrs,
		"rate_hz", cfg.Sensor.Rate,
		"verbose", cfg.Verbose,
		"duration", cfg.Duration,
		"config", cfg.ConfigFile)...)
	if !cfg.ESM() && cfg.Scenario.Epoch.IsZero() {
		log.Warn("the scenario has no epoch: it starts at 00:00 UTC today, so a route that does not loop may already have ended; set scenario.epoch")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if cfg.Duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Duration)
		defer cancel()
	}

	// An ESM sensor's fixes are positional: it uses no terrain, and its site
	// is the configured one.
	var ter *terrain.Service
	var site geo.LLA
	if cfg.ESM() {
		if cfg.Terrain.CacheDir != "" {
			log.Info("terrain unused: an esm sensor's fixes need none", "terrain_cache", cfg.Terrain.CacheDir)
		}
	} else {
		if ter, err = openTerrain(cfg.Terrain, log); err != nil {
			log.Error("sensor-sim cannot start", "error", err)
			return client.ExitFatal
		}
		if site, err = resolveSite(ctx, cfg.Sensor, ter, log); err != nil {
			if ctx.Err() != nil {
				return client.ExitOK
			}
			log.Error("sensor-sim cannot start", "error", err)
			return client.ExitFatal
		}
		warmTerrain(ctx, cfg.Scenario, ter, log)
		if ctx.Err() != nil {
			return client.ExitOK
		}
	}

	c, err := client.New(cfg, sourceFunc(site, ter), client.Options{Logger: log})
	if err != nil {
		log.Error("sensor-sim cannot start", "error", err)
		return client.ExitCode(err)
	}
	err = c.Run(ctx)
	log.Info("final stats", c.Stats().Attrs()...)
	code := client.ExitCode(err)
	if err != nil {
		log.Error("sensor-sim stopped", "error", err, "exit_code", code)
	} else {
		log.Info("sensor-sim stopped", "exit_code", code)
	}
	return code
}

func epochOf(s scenario.Scenario) string {
	if s.Epoch.IsZero() {
		return "start of the current UTC day"
	}
	return s.Epoch.UTC().Format(time.RFC3339)
}

// openTerrain returns the terrain service, or nil when terrain is off (no
// cache directory). A directory it cannot write still works, from memory
// only, so every run downloads its tiles again; that is worth a warning.
func openTerrain(tc config.TerrainConfig, log *slog.Logger) (*terrain.Service, error) {
	if tc.CacheDir == "" {
		log.Info("terrain off: targets move over flat ground at 0 m (pass --terrain-cache to use Copernicus GLO-30)")
		return nil, nil
	}
	if err := probeWritable(tc.CacheDir); err != nil {
		log.Warn("terrain cache directory not writable; tiles are kept in memory only and downloaded again on every run",
			"dir", tc.CacheDir, "error", err)
	}
	t, err := terrain.New(terrain.Config{SourceURL: tc.SourceURL, CacheDir: tc.CacheDir})
	if err != nil {
		return nil, fmt.Errorf("terrain: %w", err)
	}
	log.Info("terrain on", "cache", tc.CacheDir, "source", tc.SourceURL)
	return t, nil
}

func probeWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".write-probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// resolveSite is the sensor's position with its altitude in metres above the
// ellipsoid: --alt as given, or the ground under the site plus
// --height-above-ground, or 0 m for a radar with neither (which config allows
// only without terrain).
func resolveSite(ctx context.Context, s config.Sensor, ter *terrain.Service, log *slog.Logger) (geo.LLA, error) {
	site := geo.LLA{Lat: s.Lat, Lon: s.Lon}
	switch {
	case s.Alt != nil:
		site.Alt = *s.Alt
	case s.HeightAboveGroundM != nil:
		if ter == nil {
			return site, errors.New("--height-above-ground needs terrain (--terrain-cache)")
		}
		log.Info("looking up the ground height at the site; a cold cache downloads its terrain tile first")
		tctx, cancel := context.WithTimeout(ctx, terrainStartupWait)
		defer cancel()
		g, err := ter.HeightHAE(tctx, s.Lat, s.Lon)
		if err != nil {
			return site, fmt.Errorf("the ground height at the site (%.5f, %.5f): %w; pass --alt instead, or check the network and --terrain-url", s.Lat, s.Lon, err)
		}
		site.Alt = g + *s.HeightAboveGroundM
		log.Info("site", "ground_hae_m", g, "height_above_ground_m", *s.HeightAboveGroundM, "alt_hae_m", site.Alt)
	}
	return site, nil
}

// warmTerrain fetches the tiles under the targets' starting positions before
// streaming, so the first scans are not skipped while they download. A
// failure is only a warning: the scans retry the terrain themselves.
func warmTerrain(ctx context.Context, sc scenario.Scenario, ter *terrain.Service, log *slog.Logger) {
	if ter == nil {
		return
	}
	tctx, cancel := context.WithTimeout(ctx, terrainStartupWait)
	defer cancel()
	if _, err := sc.TruthAt(tctx, ter, time.Now()); err != nil && ctx.Err() == nil {
		log.Warn("terrain under the targets is not available yet; scans are skipped until it is", "error", err)
	}
}

// sourceFunc builds the simulated sensor for a configuration. A radar or
// camera stands at site (resolved over the terrain) and watches the scenario
// truth over the terrain (flat ground at 0 m when ter is nil). An ESM sensor
// stands at its configured site (a bound emplacement's, when the client
// rebuilds it with the server's geometry) and runs its emitters and ownship
// on a stepped simulation clock.
func sourceFunc(site geo.LLA, ter *terrain.Service) client.SourceFunc {
	return func(cfg config.Config) (client.Source, error) {
		if cfg.ESM() {
			s := esm.New(cfg)
			return client.Source{
				Site:     geo.LLA{Lat: cfg.Sensor.Lat, Lon: cfg.Sensor.Lon, Alt: cfg.Sensor.AltM()},
				Detector: s,
				State:    s.State,
				Stepped:  true,
			}, nil
		}
		var det detect.Detector
		if cfg.Sensor.Kind == config.KindEOIR {
			det = eoir.New(cfg.Sensor, site, ter, cfg.Seed)
		} else {
			det = radar.New(cfg.Sensor, site, cfg.Seed)
		}
		sc := cfg.Scenario
		return client.Source{
			Site:     site,
			Truth:    func(ctx context.Context, now time.Time) ([]scenario.Truth, error) { return sc.TruthAt(ctx, ter, now) },
			Detector: det,
		}, nil
	}
}
