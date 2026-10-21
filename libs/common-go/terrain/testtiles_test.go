package terrain

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// testTile describes a synthetic Copernicus-style GeoTIFF: one band of
// Float32, tiled, DEFLATE, georeferenced with ModelTiepoint and
// ModelPixelScale. Zero values take the defaults noted on each field.
type testTile struct {
	south, west int                            // the tile's south-west corner in whole degrees
	cols, rows  int                            // raster size, default 120 × 120
	block       int                            // TIFF tile edge in pixels, default 64 (a multiple of 16)
	predictor   int                            // 1 or 3, default 3
	areaRaster  bool                           // PixelIsArea instead of the PixelIsPoint Copernicus uses
	msl         func(lat, lon float64) float64 // height above mean sea level, default planeMSL
}

// planeMSL is the default synthetic surface: z = lat·100 + lon. Bilinear
// interpolation reproduces a plane exactly, so tests can demand exact values.
func planeMSL(lat, lon float64) float64 { return lat*100 + lon }

func (tt testTile) withDefaults() testTile {
	if tt.cols == 0 {
		tt.cols = 120
	}
	if tt.rows == 0 {
		tt.rows = 120
	}
	if tt.block == 0 {
		tt.block = 64
	}
	if tt.predictor == 0 {
		tt.predictor = 3
	}
	if tt.msl == nil {
		tt.msl = planeMSL
	}
	return tt
}

func (tt testTile) key() string { return tileID{lat: tt.south, lon: tt.west}.key() }

// pixelCentre returns the position of a pixel centre under the tile's
// raster type, matching how the GeoTIFF tags written below describe it.
// Copernicus tiles are PixelIsPoint with the tiepoint on the north-west
// corner: pixel (0, 0) is centred on that corner and the last row and column
// stop one pixel short of the south and east edges, which the neighbouring
// tiles' first row and column sit on. With PixelIsArea the same tiepoint is
// pixel (0, 0)'s corner, shifting every centre by half a pixel.
func (tt testTile) pixelCentre(col, row int) (lat, lon float64) {
	tt = tt.withDefaults()
	sx, sy := 1/float64(tt.cols), 1/float64(tt.rows)
	half := 0.0
	if tt.areaRaster {
		half = 0.5
	}
	return float64(tt.south+1) - (float64(row)+half)*sy, float64(tt.west) + (float64(col)+half)*sx
}

// encodeTestTile writes the tile as a little-endian classic TIFF.
func encodeTestTile(t testing.TB, tt testTile) []byte {
	t.Helper()
	tt = tt.withDefaults()
	w, h, b := tt.cols, tt.rows, tt.block
	across, down := (w+b-1)/b, (h+b-1)/b

	// Compress each block; pixels past the image edge are padding (zero).
	var blocks [][]byte
	plane := make([]byte, b*4)
	for ty := 0; ty < down; ty++ {
		for tx := 0; tx < across; tx++ {
			raw := make([]byte, 0, b*b*4)
			for r := 0; r < b; r++ {
				vals := make([]float32, b)
				for c := 0; c < b; c++ {
					col, row := tx*b+c, ty*b+r
					if col < w && row < h {
						lat, lon := tt.pixelCentre(col, row)
						vals[c] = float32(tt.msl(lat, lon))
					}
				}
				switch tt.predictor {
				case 1:
					for _, v := range vals {
						raw = binary.LittleEndian.AppendUint32(raw, math.Float32bits(v))
					}
				case 3:
					// TIFF Technical Note 3: split into byte planes, most
					// significant first, then difference the whole row.
					for c, v := range vals {
						bits := math.Float32bits(v)
						plane[c] = byte(bits >> 24)
						plane[b+c] = byte(bits >> 16)
						plane[2*b+c] = byte(bits >> 8)
						plane[3*b+c] = byte(bits)
					}
					for i := len(plane) - 1; i > 0; i-- {
						plane[i] -= plane[i-1]
					}
					raw = append(raw, plane...)
				default:
					t.Fatalf("test tile: predictor %d", tt.predictor)
				}
			}
			var z bytes.Buffer
			zw, _ := zlib.NewWriterLevel(&z, zlib.BestSpeed)
			if _, err := zw.Write(raw); err != nil {
				t.Fatal(err)
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			blocks = append(blocks, z.Bytes())
		}
	}

	rasterType := uint16(2) // PixelIsPoint
	if tt.areaRaster {
		rasterType = 1
	}
	scaleX, scaleY := 1/float64(w), 1/float64(h)
	tieX, tieY := float64(tt.west), float64(tt.south+1)

	type entry struct {
		tag, typ uint16
		count    uint32
		data     []byte // little-endian value bytes
	}
	shorts := func(v ...uint16) []byte {
		var out []byte
		for _, x := range v {
			out = binary.LittleEndian.AppendUint16(out, x)
		}
		return out
	}
	longs := func(v ...uint32) []byte {
		var out []byte
		for _, x := range v {
			out = binary.LittleEndian.AppendUint32(out, x)
		}
		return out
	}
	doubles := func(v ...float64) []byte {
		var out []byte
		for _, x := range v {
			out = binary.LittleEndian.AppendUint64(out, math.Float64bits(x))
		}
		return out
	}

	// Lay out: header, IFD, out-of-line values, then the compressed blocks.
	const nEntries = 17
	ifdSize := 2 + nEntries*12 + 4
	offsets := make([]uint32, len(blocks))
	counts := make([]uint32, len(blocks))
	entries := []entry{
		{256, 4, 1, longs(uint32(w))},
		{257, 4, 1, longs(uint32(h))},
		{258, 3, 1, shorts(32)},
		{259, 3, 1, shorts(8)},
		{262, 3, 1, shorts(1)},
		{277, 3, 1, shorts(1)},
		{284, 3, 1, shorts(1)},
		{317, 3, 1, shorts(uint16(tt.predictor))},
		{322, 3, 1, shorts(uint16(b))},
		{323, 3, 1, shorts(uint16(b))},
		{324, 4, uint32(len(blocks)), nil}, // TileOffsets, filled below
		{325, 4, uint32(len(blocks)), nil}, // TileByteCounts, filled below
		{339, 3, 1, shorts(3)},
		{33550, 12, 3, doubles(scaleX, scaleY, 0)},
		{33922, 12, 6, doubles(0, 0, 0, tieX, tieY, 0)},
		{34735, 3, 16, shorts(1, 1, 0, 3, 1024, 0, 1, 2, 1025, 0, 1, rasterType, 2048, 0, 1, 4326)},
		{34736, 12, 1, doubles(0)}, // a GeoDoubleParams placeholder, as real files carry one
	}
	if len(entries) != nEntries {
		t.Fatalf("test tile: %d entries, header says %d", len(entries), nEntries)
	}
	// Out-of-line area size, with placeholders for the two block arrays.
	entries[10].data = longs(make([]uint32, len(blocks))...)
	entries[11].data = longs(make([]uint32, len(blocks))...)
	extra := 0
	for _, e := range entries {
		if len(e.data) > 4 {
			extra += len(e.data)
		}
	}
	pos := uint32(8 + ifdSize + extra)
	for i, blk := range blocks {
		offsets[i] = pos
		counts[i] = uint32(len(blk))
		pos += uint32(len(blk))
	}
	entries[10].data = longs(offsets...)
	entries[11].data = longs(counts...)

	var out bytes.Buffer
	out.WriteString("II")
	out.Write(shorts(42))
	out.Write(longs(8))
	out.Write(shorts(nEntries))
	valuePos := uint32(8 + ifdSize)
	var values bytes.Buffer
	for _, e := range entries {
		out.Write(shorts(e.tag, e.typ))
		out.Write(longs(e.count))
		if len(e.data) <= 4 {
			v := make([]byte, 4)
			copy(v, e.data)
			out.Write(v)
		} else {
			out.Write(longs(valuePos + uint32(values.Len())))
			values.Write(e.data)
		}
	}
	out.Write(longs(0)) // no next IFD
	out.Write(values.Bytes())
	for _, blk := range blocks {
		out.Write(blk)
	}
	return out.Bytes()
}

// writeTestTile writes the encoded tile to dir/<key>.tif and returns the path.
func writeTestTile(t testing.TB, dir string, tt testTile) string {
	t.Helper()
	p := filepath.Join(dir, tt.key()+".tif")
	if err := os.WriteFile(p, encodeTestTile(t, tt), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// tileServer imitates the Copernicus bucket: tileList.txt at the root and
// <key>/<key>.tif for each tile, counting every request by path.
type tileServer struct {
	*httptest.Server

	mu       sync.Mutex
	tiles    map[string][]byte // key → GeoTIFF bytes
	listed   []string          // keys in tileList.txt; tiles' keys by default
	status   map[string]int    // key (or tileListName) → forced HTTP status
	delay    time.Duration     // applied to tile requests
	requests map[string]int    // URL path → count
}

// newTileServer serves the given tiles, all of them listed.
func newTileServer(t testing.TB, tiles ...testTile) *tileServer {
	t.Helper()
	ts := &tileServer{tiles: map[string][]byte{}, status: map[string]int{}, requests: map[string]int{}}
	for _, tt := range tiles {
		ts.addTile(t, tt)
	}
	ts.Server = httptest.NewServer(http.HandlerFunc(ts.serve))
	t.Cleanup(ts.Close)
	return ts
}

func (ts *tileServer) addTile(t testing.TB, tt testTile) {
	t.Helper()
	data := encodeTestTile(t, tt)
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.tiles[tt.key()] = data
	ts.listed = append(ts.listed, tt.key())
}

// list adds keys to tileList.txt without serving a tile for them.
func (ts *tileServer) list(keys ...string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.listed = append(ts.listed, keys...)
}

func (ts *tileServer) setStatus(key string, code int) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.status[key] = code
}

func (ts *tileServer) clearStatus(key string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	delete(ts.status, key)
}

// setTileBytes serves arbitrary bytes, listed, as a tile.
func (ts *tileServer) setTileBytes(key string, data []byte) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if _, ok := ts.tiles[key]; !ok {
		ts.listed = append(ts.listed, key)
	}
	ts.tiles[key] = data
}

func (ts *tileServer) setDelay(d time.Duration) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.delay = d
}

func (ts *tileServer) tileBytes(key string) []byte {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.tiles[key]
}

func (ts *tileServer) serve(w http.ResponseWriter, r *http.Request) {
	ts.mu.Lock()
	ts.requests[r.URL.Path]++
	if r.URL.Path == "/"+tileListName {
		if code, forced := ts.status[tileListName]; forced {
			ts.mu.Unlock()
			http.Error(w, http.StatusText(code), code)
			return
		}
		body := strings.Join(ts.listed, "\r\n") + "\r\n" // the real list uses CRLF
		ts.mu.Unlock()
		_, _ = w.Write([]byte(body))
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) != 2 || parts[1] != parts[0]+".tif" {
		ts.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	key := parts[0]
	data, ok := ts.tiles[key]
	code, forced := ts.status[key]
	delay := ts.delay
	ts.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	switch {
	case forced:
		http.Error(w, http.StatusText(code), code)
	case !ok:
		http.NotFound(w, r)
	default:
		_, _ = w.Write(data)
	}
}

// count counts requests for one URL path.
func (ts *tileServer) count(path string) int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.requests[path]
}

// tileRequests counts requests for one tile's GeoTIFF.
func (ts *tileServer) tileRequests(key string) int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.requests[fmt.Sprintf("/%s/%s.tif", key, key)]
}

// allTileRequests counts requests for any GeoTIFF.
func (ts *tileServer) allTileRequests() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	n := 0
	for p, c := range ts.requests {
		if strings.HasSuffix(p, ".tif") {
			n += c
		}
	}
	return n
}

// newTestService makes a Service over the server with a fresh cache dir.
// mutate, when given, adjusts the config first.
func newTestService(t testing.TB, ts *tileServer, mutate func(*Config)) *Service {
	t.Helper()
	cfg := Config{SourceURL: ts.URL, CacheDir: t.TempDir(), FetchTimeout: 10 * time.Second}
	if mutate != nil {
		mutate(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
