package terrain

import (
	"bytes"
	"context"
	"errors"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const oneMetreLat = 1 / 111_320.0 // degrees of latitude in about a metre

func height(t *testing.T, s *Service, lat, lon float64) float64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h, err := s.HeightHAE(ctx, lat, lon)
	if err != nil {
		t.Fatalf("HeightHAE(%g, %g): %v", lat, lon, err)
	}
	return h
}

func TestNewStaticUsesTheFunction(t *testing.T) {
	s := NewStatic(func(lat, lon float64) float64 { return lat*10 + lon })
	near(t, "static", height(t, s, 38.5, -77.5), 307.5, 0)
	if n, err := s.Preload(context.Background(), 38, -78, 40, -76); n != 0 || err != nil {
		t.Errorf("Preload = %d, %v; want 0, nil", n, err)
	}
}

func TestNewRequiresCacheDir(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New without CacheDir succeeded")
	}
}

func TestHeightBilinear(t *testing.T) {
	cases := []struct {
		name string
		tt   testTile
	}{
		{"point (as Copernicus), predictor 3", testTile{south: 38, west: -78}},
		{"point, predictor 1", testTile{south: 38, west: -78, predictor: 1}},
		{"area, predictor 3", testTile{south: 38, west: -78, areaRaster: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts := newTileServer(t, c.tt)
			s := newTestService(t, ts, nil)
			// Mid-cell: halfway between the centres of pixels (60, 60) and (61, 61).
			lat0, lon0 := c.tt.pixelCentre(60, 60)
			lat1, lon1 := c.tt.pixelCentre(61, 61)
			for _, f := range [][2]float64{{0.5, 0.5}, {0.25, 0.7}, {0, 0}, {0.9, 0.1}} {
				lat := lat0 + (lat1-lat0)*f[0]
				lon := lon0 + (lon1-lon0)*f[1]
				want := planeMSL(lat, lon) + GeoidHeight(lat, lon)
				near(t, "HeightHAE", height(t, s, lat, lon), want, 1e-3)
			}
			if g := GeoidHeight(lat0, lon0); math.Abs(g) < 10 {
				t.Fatalf("geoid here is %g m; the test needs one that shows", g)
			}
		})
	}
}

func TestHeightAcrossTileEdge(t *testing.T) {
	// A plane runs on unbroken across tile edges. Tiles stop their pixel
	// centres short of some edges (a whole pixel on the south and east for
	// Copernicus' PixelIsPoint, half a pixel all round for PixelIsArea), so
	// reproducing the plane exactly there means interpolating between this
	// tile's outermost pixels and the neighbour's.
	for _, c := range []struct {
		name string
		area bool
	}{{"point (as Copernicus)", false}, {"area", true}} {
		t.Run(c.name, func(t *testing.T) {
			var tiles []testTile
			for _, lat := range []int{38, 39} {
				for _, lon := range []int{-78, -77} {
					tiles = append(tiles, testTile{south: lat, west: lon, areaRaster: c.area})
				}
			}
			ts := newTileServer(t, tiles...)
			s := newTestService(t, ts, nil)
			// Neighbours are used once cached (see TestEdgeDoesNotWaitForNeighbour).
			if n, err := s.Preload(context.Background(), 38, -78, 40, -76); n != 4 || err != nil {
				t.Fatalf("Preload = %d, %v", n, err)
			}
			oneMetreLon := oneMetreLat / math.Cos(39*math.Pi/180)
			for _, p := range []struct {
				what     string
				lat, lon float64
			}{
				{"1 m south of 39°N", 39 - oneMetreLat, -77.5},
				{"1 m north of 39°N", 39 + oneMetreLat, -77.5},
				{"1 m west of 77°W", 38.5, -77 - oneMetreLon},
				{"1 m east of 77°W", 38.5, -77 + oneMetreLon},
				{"1 m south-west of the corner", 39 - oneMetreLat, -77 - oneMetreLon},
				{"1 m north-east of the corner", 39 + oneMetreLat, -77 + oneMetreLon},
				{"on the corner", 39, -77},
			} {
				near(t, p.what, height(t, s, p.lat, p.lon), planeMSL(p.lat, p.lon)+GeoidHeight(p.lat, p.lon), 1e-3)
			}
			for _, tt := range tiles {
				if n := ts.tileRequests(tt.key()); n != 1 {
					t.Errorf("%s fetched %d times, want 1", tt.key(), n)
				}
			}
		})
	}

	t.Run("width changes at 50°N", func(t *testing.T) {
		// Above 50°N tiles have fewer columns, so the neighbour's columns
		// do not line up with this tile's.
		south := testTile{south: 49, west: 10, cols: 120}
		north := testTile{south: 50, west: 10, cols: 80}
		ts := newTileServer(t, south, north)
		s := newTestService(t, ts, nil)
		if n, err := s.Preload(context.Background(), 49, 10, 51, 11); n != 2 || err != nil {
			t.Fatalf("Preload = %d, %v", n, err)
		}
		for _, lat := range []float64{50 - oneMetreLat, 50 + oneMetreLat, 50 + 0.5/120} {
			for _, lon := range []float64{10.5, 10.123, 10.987} {
				near(t, "HeightHAE", height(t, s, lat, lon), planeMSL(lat, lon)+GeoidHeight(lat, lon), 1e-3)
			}
		}
		if n := ts.tileRequests(south.key()); n != 1 {
			t.Errorf("the tile south of 50°N was fetched %d times, want 1", n)
		}
	})
}

func TestEdgeDoesNotWaitForNeighbour(t *testing.T) {
	tt := testTile{south: 38, west: -78}
	below := testTile{south: 37, west: -78}
	ts := newTileServer(t, tt, below)
	s := newTestService(t, ts, nil)
	height(t, s, 38.5, -77.5) // the tile itself is loaded; its neighbour is not
	ts.setDelay(500 * time.Millisecond)

	lat, lon := 38+0.5/120, -77.5 // in the strip past the last row, next to N37
	edge := planeMSL(38+1/120.0, lon) + GeoidHeight(lat, lon)
	truth := planeMSL(lat, lon) + GeoidHeight(lat, lon)
	start := time.Now()
	got := height(t, s, lat, lon)
	if took := time.Since(start); took > 50*time.Millisecond {
		t.Errorf("edge lookup took %v with the neighbour 500 ms away; it must not wait", took)
	}
	near(t, "before the neighbour arrives (edge value)", got, edge, 1e-3)

	deadline := time.Now().Add(5 * time.Second)
	for {
		got = height(t, s, lat, lon)
		if math.Abs(got-truth) <= 1e-3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	near(t, "after the neighbour arrives (interpolated)", got, truth, 1e-3)
	if n := ts.tileRequests(below.key()); n != 1 {
		t.Errorf("neighbour fetched %d times, want 1", n)
	}
}

func TestEdgeNextToSeaRunsDownToSeaLevel(t *testing.T) {
	tt := testTile{south: 38, west: -78}
	ts := newTileServer(t, tt) // N37 is not listed: sea
	s := newTestService(t, ts, nil)
	lastRow := 38 + 1/120.0 // the last row's centre; the edge is at 38
	lat, lon := 38+0.5/120, -77.5
	want := planeMSL(lastRow, lon) / 2
	near(t, "halfway from the last row to a sea edge", height(t, s, lat, lon), want+GeoidHeight(lat, lon), 1e-3)
}

func TestEdgeNextToUnavailableTileUsesEdgeValue(t *testing.T) {
	tt := testTile{south: 38, west: -78}
	below := testTile{south: 37, west: -78}
	ts := newTileServer(t, tt, below)
	ts.setStatus(below.key(), http.StatusInternalServerError)
	s := newTestService(t, ts, nil)
	lat, lon := 38+0.5/120, -77.5
	want := planeMSL(38+1/120.0, lon) // the last row, held to the edge
	near(t, "beside a failed tile", height(t, s, lat, lon), want+GeoidHeight(lat, lon), 1e-3)
}

func TestHighLatitudeNarrowTile(t *testing.T) {
	// Above 50°N Copernicus tiles have fewer columns; at N60 there are 1800.
	steep := func(lat, lon float64) float64 { return (lat-60)*100 + (lon-10)*1000 }
	tt := testTile{south: 60, west: 10, cols: 1800, rows: 3600, block: 1024, msl: steep}
	ts := newTileServer(t, tt)
	s := newTestService(t, ts, nil)
	for _, px := range [][2]int{{900, 1800}, {10, 3000}, {1700, 200}} {
		lat0, lon0 := tt.pixelCentre(px[0], px[1])
		lat1, lon1 := tt.pixelCentre(px[0]+1, px[1]+1)
		lat, lon := (lat0+lat1)/2, (lon0+lon1)/2
		near(t, "HeightHAE", height(t, s, lat, lon), steep(lat, lon)+GeoidHeight(lat, lon), 1e-3)
	}
}

func TestUnlistedTileIsSea(t *testing.T) {
	ts := newTileServer(t, testTile{south: 38, west: -78})
	s := newTestService(t, ts, nil)
	for _, p := range [][2]float64{{0.5, -30.5}, {-45.2, -150.7}, {37.9, -77.5}} {
		near(t, "sea", height(t, s, p[0], p[1]), GeoidHeight(p[0], p[1]), 1e-9)
	}
	if n := ts.allTileRequests(); n != 0 {
		t.Errorf("%d tile requests for sea, want 0", n)
	}
}

func TestKnownGapIsUnavailable(t *testing.T) {
	// The real list has N38 E043 but not N40 E044 (Yerevan).
	listedInGap := testTile{south: 38, west: 43}
	ts := newTileServer(t, listedInGap)
	s := newTestService(t, ts, nil)

	_, err := s.HeightHAE(context.Background(), 40.18, 44.51)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Yerevan: err = %v, want ErrUnavailable", err)
	}
	lat, lon := 38.5, 43.5
	near(t, "listed tile inside the gap box", height(t, s, lat, lon), planeMSL(lat, lon)+GeoidHeight(lat, lon), 1e-3)
}

func TestFetchFailureIsUnavailableNotSea(t *testing.T) {
	tt := testTile{south: 38, west: -78}
	ts := newTileServer(t, tt)
	ts.setStatus(tt.key(), http.StatusInternalServerError)
	s := newTestService(t, ts, nil)

	for i := 0; i < 2; i++ {
		_, err := s.HeightHAE(context.Background(), 38.5, -77.5)
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("call %d: err = %v, want ErrUnavailable", i, err)
		}
	}
	if n := ts.tileRequests(tt.key()); n != 1 {
		t.Errorf("tile fetched %d times, want 1 (the failure is remembered for 30 s)", n)
	}
	if st := s.Stats(); st.Failures != 1 || st.Downloads != 0 {
		t.Errorf("stats = %+v, want 1 failure and no downloads", st)
	}

	// After 30 s it tries again.
	ts.clearStatus(tt.key())
	s.now = func() time.Time { return time.Now().Add(31 * time.Second) }
	near(t, "after the retry", height(t, s, 38.5, -77.5), planeMSL(38.5, -77.5)+GeoidHeight(38.5, -77.5), 1e-3)
}

func TestFailedTileListIsUnavailable(t *testing.T) {
	ts := newTileServer(t)
	ts.setStatus(tileListName, http.StatusServiceUnavailable)
	s := newTestService(t, ts, nil)
	if _, err := s.HeightHAE(context.Background(), 0.5, -30.5); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable: without the list, sea can't be told from land", err)
	}
}

func TestSingleFetchForConcurrentCallers(t *testing.T) {
	tt := testTile{south: 38, west: -78}
	ts := newTileServer(t, tt)
	ts.setDelay(100 * time.Millisecond)
	s := newTestService(t, ts, nil)

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			h, err := s.HeightHAE(context.Background(), 38.5, -77.5)
			if err == nil && math.Abs(h-(planeMSL(38.5, -77.5)+GeoidHeight(38.5, -77.5))) > 1e-3 {
				err = errors.New("wrong height")
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := ts.tileRequests(tt.key()); n != 1 {
		t.Errorf("tile fetched %d times by 50 callers, want 1", n)
	}
	if n := ts.count("/tileList.txt"); n != 1 {
		t.Errorf("tile list fetched %d times by 50 callers, want 1", n)
	}
}

func TestCallerTimeoutDoesNotCancelDownload(t *testing.T) {
	tt := testTile{south: 38, west: -78}
	ts := newTileServer(t, tt)
	s := newTestService(t, ts, nil)
	height(t, s, 0.5, -30.5) // loads the tile list
	ts.setDelay(200 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := s.HeightHAE(ctx, 38.5, -77.5)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want it to wrap the deadline too", err)
	}

	time.Sleep(400 * time.Millisecond) // the download finishes in the background
	ts.setDelay(0)
	near(t, "later", height(t, s, 38.5, -77.5), planeMSL(38.5, -77.5)+GeoidHeight(38.5, -77.5), 1e-3)
	if n := ts.tileRequests(tt.key()); n != 1 {
		t.Errorf("tile fetched %d times, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(s.cfg.CacheDir, tt.key()+".tif")); err != nil {
		t.Errorf("tile not cached on disk: %v", err)
	}
}

func TestCorruptCachedTileRefetched(t *testing.T) {
	tt := testTile{south: 38, west: -78}
	ts := newTileServer(t, tt)
	dir := t.TempDir()
	path := filepath.Join(dir, tt.key()+".tif")
	if err := os.WriteFile(path, bytes.Repeat([]byte("not a tiff "), 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newTestService(t, ts, func(c *Config) { c.CacheDir = dir })

	near(t, "HeightHAE", height(t, s, 38.5, -77.5), planeMSL(38.5, -77.5)+GeoidHeight(38.5, -77.5), 1e-3)
	if n := ts.tileRequests(tt.key()); n != 1 {
		t.Errorf("tile fetched %d times, want 1", n)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, ts.tileBytes(tt.key())) {
		t.Error("the corrupt file was not replaced by the fetched tile")
	}
}

func TestCorruptDownloadIsUnavailable(t *testing.T) {
	tt := testTile{south: 38, west: -78}
	ts := newTileServer(t)
	ts.setTileBytes(tt.key(), []byte("II*\x00garbage"))
	dir := t.TempDir()
	s := newTestService(t, ts, func(c *Config) { c.CacheDir = dir })
	if _, err := s.HeightHAE(context.Background(), 38.5, -77.5); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if _, err := os.Stat(filepath.Join(dir, tt.key()+".tif")); !os.IsNotExist(err) {
		t.Errorf("an undecodable download stayed in the cache: %v", err)
	}
}

func TestPartFileIgnored(t *testing.T) {
	tt := testTile{south: 38, west: -78}
	ts := newTileServer(t, tt)
	dir := t.TempDir()
	// A complete-looking but different tile left behind as .part by a crash.
	stale := tt
	stale.msl = func(lat, lon float64) float64 { return 9999 }
	part := filepath.Join(dir, tt.key()+".tif.part")
	if err := os.WriteFile(part, encodeTestTile(t, stale), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newTestService(t, ts, func(c *Config) { c.CacheDir = dir })

	near(t, "HeightHAE", height(t, s, 38.5, -77.5), planeMSL(38.5, -77.5)+GeoidHeight(38.5, -77.5), 1e-3)
	if n := ts.tileRequests(tt.key()); n != 1 {
		t.Errorf("tile fetched %d times, want 1", n)
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Errorf(".part file still there: %v", err)
	}
}

func cachedTileBytes(t *testing.T, dir string) int64 {
	t.Helper()
	var total int64
	matches, _ := filepath.Glob(filepath.Join(dir, "*.tif"))
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err == nil {
			total += fi.Size()
		}
	}
	return total
}

func TestEvictionKeepsUnderCap(t *testing.T) {
	a := testTile{south: 38, west: -78}
	b := testTile{south: 39, west: -78}
	c := testTile{south: 40, west: -78}
	d := testTile{south: 41, west: -78}
	ts := newTileServer(t, a, b, c, d)
	var largest int64
	for _, tt := range []testTile{a, b, c, d} {
		largest = max(largest, int64(len(ts.tileBytes(tt.key()))))
	}
	limit := 2*largest + largest/2 // room for two tiles
	dir := t.TempDir()
	s := newTestService(t, ts, func(cfg *Config) {
		cfg.CacheDir = dir
		cfg.MaxCacheBytes = limit
		cfg.MemTiles = 1 // so re-reading a tile goes to disk
	})
	exists := func(tt testTile) bool {
		_, err := os.Stat(filepath.Join(dir, tt.key()+".tif"))
		return err == nil
	}
	check := func(when string) {
		t.Helper()
		if got := cachedTileBytes(t, dir); got > limit {
			t.Errorf("%s: %d bytes on disk, cap %d", when, got, limit)
		}
		if got := s.Stats().CacheBytes; got != cachedTileBytes(t, dir) {
			t.Errorf("%s: Stats().CacheBytes = %d, disk has %d", when, got, cachedTileBytes(t, dir))
		}
	}

	height(t, s, 38.5, -77.5) // a
	check("after a")
	height(t, s, 39.5, -77.5) // b
	check("after b")
	height(t, s, 38.5, -77.5) // a again, from disk: now b is least recently used
	height(t, s, 40.5, -77.5) // c evicts b
	check("after c")
	if !exists(a) || exists(b) || !exists(c) {
		t.Errorf("after c: a %v, b %v, c %v; want a and c kept, b evicted", exists(a), exists(b), exists(c))
	}
	height(t, s, 41.5, -77.5) // d evicts a
	check("after d")
	if exists(a) || !exists(c) || !exists(d) {
		t.Errorf("after d: a %v, c %v, d %v; want c and d kept", exists(a), exists(c), exists(d))
	}
	if n := ts.tileRequests(a.key()); n != 1 {
		t.Errorf("a fetched %d times, want 1 (the re-read came from disk)", n)
	}
}

func TestUnwritableCacheServesFromMemory(t *testing.T) {
	a := testTile{south: 38, west: -78}
	b := testTile{south: 39, west: -78}
	ts := newTileServer(t, a, b)
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	core, logs := observer.New(zapcore.WarnLevel)
	s := newTestService(t, ts, func(c *Config) {
		c.CacheDir = notADir
		c.Logger = zap.New(core)
	})

	for _, p := range [][2]float64{{38.5, -77.5}, {38.25, -77.75}, {39.5, -77.5}, {39.2, -77.1}} {
		near(t, "HeightHAE", height(t, s, p[0], p[1]), planeMSL(p[0], p[1])+GeoidHeight(p[0], p[1]), 1e-3)
	}
	if n := ts.allTileRequests(); n != 2 {
		t.Errorf("%d tile requests, want 2 (each tile once, then from memory)", n)
	}
	if n := ts.count("/tileList.txt"); n != 1 {
		t.Errorf("tile list fetched %d times, want 1", n)
	}
	warnings := 0
	for _, e := range logs.All() {
		if strings.Contains(e.Message, "not writable") {
			warnings++
		}
	}
	if warnings != 1 {
		t.Errorf("%d 'not writable' warnings, want exactly 1; logs: %v", warnings, logs.All())
	}
}

func TestPreloadFetchesBox(t *testing.T) {
	var tiles []testTile
	for _, lat := range []int{38, 39} {
		for _, lon := range []int{-78, -77} {
			tiles = append(tiles, testTile{south: lat, west: lon})
		}
	}
	ts := newTileServer(t, tiles...)
	dir := t.TempDir()
	s := newTestService(t, ts, func(c *Config) { c.CacheDir = dir })

	n, err := s.Preload(context.Background(), 38, -78, 40, -76)
	if err != nil || n != 4 {
		t.Fatalf("Preload = %d, %v; want 4, nil", n, err)
	}
	for _, tt := range tiles {
		if _, err := os.Stat(filepath.Join(dir, tt.key()+".tif")); err != nil {
			t.Errorf("%s not on disk: %v", tt.key(), err)
		}
	}
	for _, tt := range tiles {
		lat, lon := tt.pixelCentre(30, 30)
		near(t, "HeightHAE", height(t, s, lat, lon), planeMSL(lat, lon)+GeoidHeight(lat, lon), 1e-3)
	}
	if n := ts.allTileRequests(); n != 4 {
		t.Errorf("%d tile requests, want 4", n)
	}
	// Preloading again finds everything cached.
	if n, err := s.Preload(context.Background(), 38, -78, 40, -76); err != nil || n != 4 {
		t.Errorf("second Preload = %d, %v; want 4, nil", n, err)
	}
	if n := ts.allTileRequests(); n != 4 {
		t.Errorf("%d tile requests after the second preload, want 4", n)
	}
}

func TestCacheSurvivesRestart(t *testing.T) {
	tt := testTile{south: 38, west: -78}
	ts := newTileServer(t, tt)
	dir := t.TempDir()
	first := newTestService(t, ts, func(c *Config) { c.CacheDir = dir })
	height(t, first, 38.5, -77.5)

	second := newTestService(t, ts, func(c *Config) { c.CacheDir = dir })
	near(t, "after restart", height(t, second, 38.5, -77.5), planeMSL(38.5, -77.5)+GeoidHeight(38.5, -77.5), 1e-3)
	if n := ts.count("/tileList.txt"); n != 1 {
		t.Errorf("tile list fetched %d times, want 1 (it is cached on disk)", n)
	}
	if n := ts.tileRequests(tt.key()); n != 1 {
		t.Errorf("tile fetched %d times, want 1", n)
	}
	if st := second.Stats(); st.CacheBytes == 0 || st.Downloads != 0 || st.Misses != 1 {
		t.Errorf("stats after restart = %+v, want cached bytes, no downloads, one miss", st)
	}
}

func TestStatsCountHitsAndMisses(t *testing.T) {
	ts := newTileServer(t, testTile{south: 38, west: -78})
	s := newTestService(t, ts, nil)
	height(t, s, 38.5, -77.5)
	height(t, s, 38.6, -77.4)
	height(t, s, 38.7, -77.3)
	st := s.Stats()
	if st.Misses != 1 || st.Hits != 2 || st.Downloads != 1 || st.Failures != 0 || st.CacheBytes == 0 {
		t.Errorf("stats = %+v, want 1 miss, 2 hits, 1 download, cached bytes", st)
	}
}

func TestHeightRejectsBadPositions(t *testing.T) {
	s := NewStatic(func(lat, lon float64) float64 { return 0 })
	for _, p := range [][2]float64{{math.NaN(), 0}, {0, math.Inf(1)}, {91, 0}, {-90.5, 0}} {
		if _, err := s.HeightHAE(context.Background(), p[0], p[1]); err == nil {
			t.Errorf("HeightHAE(%g, %g) succeeded", p[0], p[1])
		}
	}
}

// sequentialWaits makes n HeightHAE calls one after another, as one sender's
// stream does, each with its own 2 s budget, and returns how long each took
// and what it returned.
func sequentialWaits(s *Service, n int, lat, lon float64) ([]time.Duration, []error) {
	took := make([]time.Duration, n)
	errs := make([]error, n)
	for i := range n {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		start := time.Now()
		_, errs[i] = s.HeightHAE(ctx, lat, lon)
		took[i] = time.Since(start)
		cancel()
	}
	return took, errs
}

// waitForHeight polls until the height arrives or 10 s pass.
func waitForHeight(t *testing.T, s *Service, lat, lon float64) float64 {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		h, err := s.HeightHAE(ctx, lat, lon)
		cancel()
		if err == nil {
			return h
		}
		if time.Now().After(deadline) {
			t.Fatalf("no height after the fetch was released: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A fetch that hangs holds up one sender for one join window, not one wait
// per detection: once the fetch is older than the window, callers give up
// at once while it carries on, and its result serves the next call.
func TestHungFetchHoldsCallersForOneJoinWindow(t *testing.T) {
	for _, c := range []struct {
		name string
		held string
	}{
		{"tile", testTile{south: 38, west: -78}.key()},
		{"tile list", tileListName},
	} {
		t.Run(c.name, func(t *testing.T) {
			tt := testTile{south: 38, west: -78}
			ts := newTileServer(t, tt)
			s := newTestService(t, ts, nil) // JoinWindow left at its default
			if s.cfg.JoinWindow != 2*time.Second {
				t.Fatalf("default JoinWindow = %v, want 2s", s.cfg.JoinWindow)
			}
			if c.held != tileListName {
				height(t, s, 0.5, -30.5) // loads the tile list
			}
			release := ts.hold(t, c.held)

			start := time.Now()
			took, errs := sequentialWaits(s, 10, 38.5, -77.5)
			total := time.Since(start)
			for i, err := range errs {
				if !errors.Is(err, ErrUnavailable) {
					t.Errorf("call %d: err = %v, want ErrUnavailable", i, err)
				}
			}
			if took[0] < s.cfg.JoinWindow-100*time.Millisecond {
				t.Errorf("first call gave up after %v; it should wait out its window (%v)", took[0], s.cfg.JoinWindow)
			}
			for i, d := range took[1:] {
				if d > 50*time.Millisecond {
					t.Errorf("call %d waited %v on a fetch older than the join window; want an immediate ErrUnavailable", i+1, d)
				}
			}
			if limit := s.cfg.JoinWindow + time.Second; total > limit {
				t.Errorf("10 calls took %v, want under %v (per call: %v)", total, limit, took)
			}

			// The fetch carried on; once it lands, the next call gets the value.
			release()
			near(t, "after the fetch lands", waitForHeight(t, s, 38.5, -77.5), planeMSL(38.5, -77.5)+GeoidHeight(38.5, -77.5), 1e-3)
			if n := ts.tileRequests(tt.key()); n != 1 {
				t.Errorf("tile fetched %d times, want 1", n)
			}
			if n := ts.count("/" + tileListName); n != 1 {
				t.Errorf("tile list fetched %d times, want 1", n)
			}
		})
	}
}

// A caller that arrives while the fetch is still young joins it and gets the
// value when it lands, even after the one that started it gave up.
func TestCallerJoiningWithinWindowGetsValue(t *testing.T) {
	tt := testTile{south: 38, west: -78}
	ts := newTileServer(t, tt)
	s := newTestService(t, ts, func(c *Config) { c.JoinWindow = time.Second })
	height(t, s, 0.5, -30.5)
	release := ts.hold(t, tt.key())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := s.HeightHAE(ctx, 38.5, -77.5); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("starter: err = %v, want ErrUnavailable", err)
	}
	time.Sleep(300 * time.Millisecond) // the fetch is 350 ms old: inside the window
	got := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		h, err := s.HeightHAE(ctx, 38.5, -77.5)
		if err == nil && math.Abs(h-(planeMSL(38.5, -77.5)+GeoidHeight(38.5, -77.5))) > 1e-3 {
			err = errors.New("wrong height")
		}
		got <- err
	}()
	time.Sleep(200 * time.Millisecond)
	release()
	if err := <-got; err != nil {
		t.Errorf("joiner: %v", err)
	}
	if n := ts.tileRequests(tt.key()); n != 1 {
		t.Errorf("tile fetched %d times, want 1", n)
	}
}

// The caller that starts a fetch waits on its own context, not the window:
// sensor-sim's start-up lookup gives a cold 48 MB tile minutes.
func TestStarterWaitsOnItsOwnContext(t *testing.T) {
	tt := testTile{south: 38, west: -78}
	ts := newTileServer(t, tt)
	s := newTestService(t, ts, func(c *Config) { c.JoinWindow = 100 * time.Millisecond })
	height(t, s, 0.5, -30.5)
	release := ts.hold(t, tt.key())
	time.AfterFunc(400*time.Millisecond, release)
	near(t, "starter", height(t, s, 38.5, -77.5), planeMSL(38.5, -77.5)+GeoidHeight(38.5, -77.5), 1e-3)
}

// Preload waits for the download it needs however long it has been running.
func TestPreloadWaitsOnOldFetch(t *testing.T) {
	tt := testTile{south: 38, west: -78}
	ts := newTileServer(t, tt)
	s := newTestService(t, ts, func(c *Config) { c.JoinWindow = 100 * time.Millisecond })
	height(t, s, 0.5, -30.5)
	release := ts.hold(t, tt.key())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.HeightHAE(ctx, 38.5, -77.5); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	time.Sleep(200 * time.Millisecond) // the fetch is now older than the window
	time.AfterFunc(200*time.Millisecond, release)
	pctx, pcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pcancel()
	if n, err := s.Preload(pctx, 38, -78, 39, -77); n != 1 || err != nil {
		t.Errorf("Preload = %d, %v; want 1, nil", n, err)
	}
	if n := ts.tileRequests(tt.key()); n != 1 {
		t.Errorf("tile fetched %d times, want 1", n)
	}
}
