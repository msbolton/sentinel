package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sentinel/go-common/terrain"
)

type fakePreloader struct {
	calls       int
	south, west float64
	north, east float64
	ready       int
	err         error
}

func (f *fakePreloader) Preload(_ context.Context, s, w, n, e float64) (int, error) {
	f.calls++
	f.south, f.west, f.north, f.east = s, w, n, e
	return f.ready, f.err
}

// runWith runs the command with a fake terrain service and returns the exit
// code, what it printed, what it complained about and the config the service
// was built with.
func runWith(t *testing.T, f *fakePreloader, args ...string) (code int, out, errOut string, cfg terrain.Config, built bool) {
	t.Helper()
	var so, se bytes.Buffer
	code = run(context.Background(), args, &so, &se, func(c terrain.Config) (preloader, error) {
		cfg, built = c, true
		return f, nil
	})
	return code, so.String(), se.String(), cfg, built
}

func TestPreloadArgs(t *testing.T) {
	bad := map[string][]string{
		"no args":           {},
		"no bbox":           {"--cache-dir", "/c"},
		"no cache dir":      {"--bbox", "38,-78,40,-76"},
		"three numbers":     {"--cache-dir", "/c", "--bbox", "38,-78,40"},
		"not numbers":       {"--cache-dir", "/c", "--bbox", "a,b,c,d"},
		"south above north": {"--cache-dir", "/c", "--bbox", "40,-78,38,-76"},
		"equal south north": {"--cache-dir", "/c", "--bbox", "38,-78,38,-76"},
		"latitude too big":  {"--cache-dir", "/c", "--bbox", "38,-78,91,-76"},
		"longitude too big": {"--cache-dir", "/c", "--bbox", "38,-78,40,181"},
		"unknown flag":      {"--cache-dir", "/c", "--bbox", "38,-78,40,-76", "--wat"},
		"extra argument":    {"--cache-dir", "/c", "--bbox", "38,-78,40,-76", "extra"},
		"zero cache max":    {"--cache-dir", "/c", "--bbox", "38,-78,40,-76", "--cache-max-mb", "0"},
	}
	for name, args := range bad {
		t.Run(name, func(t *testing.T) {
			f := &fakePreloader{}
			code, _, errOut, _, built := runWith(t, f, args...)
			if code != 2 {
				t.Errorf("exit = %d, want 2", code)
			}
			if !strings.Contains(errOut, "usage: terrain-preload") {
				t.Errorf("stderr lacks the usage line: %q", errOut)
			}
			if f.calls != 0 || built {
				t.Error("a malformed command must not build or call the service")
			}
		})
	}

	t.Run("valid", func(t *testing.T) {
		f := &fakePreloader{ready: 4}
		code, out, errOut, cfg, _ := runWith(t, f, "--cache-dir", "/c", "--source-url", "http://mirror/dem", "--bbox", "38,-78,40,-76")
		if code != 0 {
			t.Fatalf("exit = %d, stderr %q", code, errOut)
		}
		if f.calls != 1 || f.south != 38 || f.west != -78 || f.north != 40 || f.east != -76 {
			t.Errorf("Preload called %d times with %v,%v,%v,%v", f.calls, f.south, f.west, f.north, f.east)
		}
		if !strings.Contains(out, "4") {
			t.Errorf("stdout lacks the tile count: %q", out)
		}
		if cfg.CacheDir != "/c" || cfg.SourceURL != "http://mirror/dem" || cfg.MaxCacheBytes != 2048<<20 {
			t.Errorf("config = %+v", cfg)
		}
	})

	t.Run("antimeridian box", func(t *testing.T) {
		f := &fakePreloader{ready: 2}
		code, _, errOut, _, _ := runWith(t, f, "--cache-dir", "/c", "--bbox", "10,179,11,-179")
		if code != 0 || f.calls != 1 {
			t.Errorf("exit %d, calls %d, stderr %q", code, f.calls, errOut)
		}
	})
}

func TestPreloadFailureExitsNonZero(t *testing.T) {
	f := &fakePreloader{ready: 1, err: errors.New("tile N39_00_W077_00: timeout")}
	code, out, errOut, _, _ := runWith(t, f, "--cache-dir", "/c", "--bbox", "38,-78,40,-76")
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut, "timeout") {
		t.Errorf("stderr lacks the cause: %q", errOut)
	}
	if !strings.Contains(out, "1") {
		t.Errorf("stdout should still say how many tiles are ready: %q", out)
	}
}

func TestPreloadServiceError(t *testing.T) {
	var so, se bytes.Buffer
	code := run(context.Background(), []string{"--cache-dir", "/c", "--bbox", "38,-78,40,-76"}, &so, &se,
		func(terrain.Config) (preloader, error) { return nil, errors.New("boom") })
	if code != 1 || !strings.Contains(se.String(), "boom") {
		t.Errorf("exit %d, stderr %q", code, se.String())
	}
}

// Preload itself has no cap, so a box needing more tiles than the cache keeps
// would download far more than survives; the command refuses it up front.
func TestPreloadRefusesBoxOverCacheCap(t *testing.T) {
	// 4 tiles x 50 MB = 200 MB > 128 MB.
	f := &fakePreloader{}
	code, _, errOut, _, built := runWith(t, f, "--cache-dir", "/c", "--bbox", "38,-78,40,-76", "--cache-max-mb", "128")
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "--cache-max-mb") || !strings.Contains(errOut, "4 tiles") {
		t.Errorf("stderr should name the tile count and the flag: %q", errOut)
	}
	if f.calls != 0 || built {
		t.Error("an over-cap box must not download anything")
	}

	// The same box fits when the cache is large enough (4 x 50 = 200 MB <= 200).
	f = &fakePreloader{ready: 4}
	if code, _, errOut, _, _ := runWith(t, f, "--cache-dir", "/c", "--bbox", "38,-78,40,-76", "--cache-max-mb", "200"); code != 0 {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

func TestEstimateTiles(t *testing.T) {
	cases := []struct {
		name                     string
		south, west, north, east float64
		want                     int
	}{
		{"whole degrees", 38, -78, 40, -76, 4},
		{"inside one tile", 38.2, -77.8, 38.9, -77.1, 1},
		{"straddles four", 38.5, -77.5, 39.5, -76.5, 4},
		{"antimeridian", 10, 179, 11, -179, 2},
		{"top of world", 89.5, 0, 90, 1, 1},
	}
	for _, tc := range cases {
		if got := estimateTiles(tc.south, tc.west, tc.north, tc.east); got != tc.want {
			t.Errorf("%s: %d tiles, want %d", tc.name, got, tc.want)
		}
	}
}
