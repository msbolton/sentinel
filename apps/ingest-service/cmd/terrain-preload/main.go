// Command terrain-preload downloads the Copernicus GLO-30 terrain tiles for a
// box into the ingest service's terrain cache, so the first detections there
// do not wait on a download:
//
//	terrain-preload --cache-dir DIR [--source-url URL] [--cache-max-mb N] --bbox south,west,north,east
//
// It prints how many tiles are ready. The exit code is 0 when every listed
// tile in the box is cached, 1 when a download or the setup failed, and 2 for a
// malformed command line or a box larger than the cache keeps.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/sentinel/go-common/terrain"
)

// tileBytes is what the cache budget counts per tile: a GLO-30 tile is up to
// about 50 MB.
const tileBytes = 50 << 20

const usage = "usage: terrain-preload --cache-dir DIR [--source-url URL] [--cache-max-mb N] --bbox south,west,north,east"

// preloader is the part of terrain.Service the command uses.
type preloader interface {
	Preload(ctx context.Context, south, west, north, east float64) (int, error)
}

func newService(cfg terrain.Config) (preloader, error) { return terrain.New(cfg) }

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, newService))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, build func(terrain.Config) (preloader, error)) int {
	fs := flag.NewFlagSet("terrain-preload", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cacheDir := fs.String("cache-dir", "", "terrain cache folder (the service's INGEST_TERRAIN_CACHE_DIR)")
	sourceURL := fs.String("source-url", "", "tile source (default: the Copernicus bucket)")
	cacheMaxMB := fs.Int("cache-max-mb", 2048, "cache size cap in MB (the service's INGEST_TERRAIN_CACHE_MAX_MB)")
	bboxArg := fs.String("bbox", "", "south,west,north,east in degrees")
	bad := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "terrain-preload: "+format+"\n%s\n", append(a, usage)...)
		return 2
	}
	if err := fs.Parse(args); err != nil {
		return bad("%v", err)
	}
	if fs.NArg() > 0 {
		return bad("unexpected argument %q", fs.Arg(0))
	}
	if *cacheDir == "" {
		return bad("--cache-dir is required")
	}
	if *cacheMaxMB <= 0 {
		return bad("--cache-max-mb must be positive")
	}
	south, west, north, east, err := parseBBox(*bboxArg)
	if err != nil {
		return bad("--bbox: %v", err)
	}
	// Preload has no cap of its own, so a box bigger than the cache would
	// download far more than the cache keeps (and evict what it just fetched).
	tiles := estimateTiles(south, west, north, east)
	if need := int64(tiles) * tileBytes; need > int64(*cacheMaxMB)<<20 {
		return bad("the box covers %d tiles, about %d MB, over --cache-max-mb %d: shrink the box or raise the cap",
			tiles, need>>20, *cacheMaxMB)
	}

	svc, err := build(terrain.Config{SourceURL: *sourceURL, CacheDir: *cacheDir, MaxCacheBytes: int64(*cacheMaxMB) << 20})
	if err != nil {
		fmt.Fprintf(stderr, "terrain-preload: %v\n", err)
		return 1
	}
	ready, err := svc.Preload(ctx, south, west, north, east)
	fmt.Fprintf(stdout, "%d tiles ready\n", ready)
	if err != nil {
		fmt.Fprintf(stderr, "terrain-preload: %v\n", err)
		return 1
	}
	return 0
}

// parseBBox reads "south,west,north,east". A west edge east of the east edge
// crosses the antimeridian, as terrain.Preload reads it.
func parseBBox(s string) (south, west, north, east float64, err error) {
	if s == "" {
		return 0, 0, 0, 0, fmt.Errorf("required")
	}
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return 0, 0, 0, 0, fmt.Errorf("want south,west,north,east, got %d values", len(parts))
	}
	var v [4]float64
	for i, p := range parts {
		f, perr := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if perr != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, 0, 0, 0, fmt.Errorf("%q is not a number", p)
		}
		v[i] = f
	}
	south, west, north, east = v[0], v[1], v[2], v[3]
	if south < -90 || north > 90 || south >= north {
		return 0, 0, 0, 0, fmt.Errorf("latitudes must satisfy -90 <= south < north <= 90")
	}
	if west < -180 || west > 180 || east < -180 || east > 180 {
		return 0, 0, 0, 0, fmt.Errorf("longitudes must be within -180..180")
	}
	return south, west, north, east, nil
}

// estimateTiles counts the 1 degree tiles a box touches (an edge on a whole
// degree does not pull in the tile beyond it), sea tiles included, so it is
// an upper bound on what Preload downloads.
func estimateTiles(south, west, north, east float64) int {
	lastIndex := func(edge float64, first int) int { return max(int(math.Ceil(edge))-1, first) }
	lat0 := int(math.Floor(south))
	lat1 := min(lastIndex(north, lat0), 89)
	span := east - west
	if span < 0 {
		span += 360
	}
	lon0 := int(math.Floor(west))
	lon1 := lastIndex(west+span, lon0)
	return (lat1 - lat0 + 1) * min(lon1-lon0+1, 360)
}
