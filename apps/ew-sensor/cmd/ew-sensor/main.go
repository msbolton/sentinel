// Command ew-sensor simulates an EW (ESM) sensor: it geolocates simulated
// RF emitters and streams the fixes to Sentinel's gRPC ingest service.
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

	"github.com/sentinel/ew-sensor/internal/client"
	"github.com/sentinel/ew-sensor/internal/config"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, env func(string) string, stdout, stderr io.Writer) int {
	cfg, err := config.Load(args, env)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintf(stdout, "Usage: ew-sensor --addr host:port (--insecure | --tls) [flags]\n\n%s", config.Usage())
		return client.ExitOK
	}
	if err != nil {
		fmt.Fprintf(stderr, "ew-sensor: %v\nRun ew-sensor --help for the flags.\n", err)
		return client.ExitFatal
	}

	log := slog.New(slog.NewTextHandler(stderr, nil))
	log.Info("ew-sensor starting",
		"addr", cfg.Connection.Addr,
		"tls", cfg.Connection.TLS,
		"token", cfg.Connection.RedactedToken(),
		"seed", cfg.Seed,
		"seed_generated", cfg.SeedGenerated,
		"emitters", cfg.EmitterCount,
		"rate_hz", cfg.Sensor.Rate,
		"mobile", cfg.Mobile(),
		"duration", cfg.Duration,
		"config", cfg.ConfigFile)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if cfg.Duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Duration)
		defer cancel()
	}

	c, err := client.New(cfg, client.Options{Logger: log})
	if err != nil {
		log.Error("ew-sensor cannot start", "error", err)
		return client.ExitCode(err)
	}
	err = c.Run(ctx)
	log.Info("final stats", c.Stats().Attrs()...)
	code := client.ExitCode(err)
	if err != nil {
		log.Error("ew-sensor stopped", "error", err, "exit_code", code)
	} else {
		log.Info("ew-sensor stopped", "exit_code", code)
	}
	return code
}
