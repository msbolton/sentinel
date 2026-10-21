package terrain

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

func decodeBytes(data []byte) (*raster, error) {
	return decodeTIFF(bytes.NewReader(data), int64(len(data)))
}

func TestDecodeTIFF(t *testing.T) {
	cases := []struct {
		name string
		tt   testTile
	}{
		{"predictor 3, point (as Copernicus)", testTile{south: 38, west: -78}},
		{"predictor 1, point", testTile{south: 38, west: -78, predictor: 1}},
		{"predictor 3, area", testTile{south: 38, west: -78, areaRaster: true}},
		{"padded blocks, narrow", testTile{south: 60, west: 10, cols: 50, rows: 100, block: 32}},
		{"southern hemisphere", testTile{south: -34, west: 151, cols: 70, rows: 70, block: 16}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tt := c.tt.withDefaults()
			r, err := decodeBytes(encodeTestTile(t, tt))
			if err != nil {
				t.Fatal(err)
			}
			if r.width != tt.cols || r.height != tt.rows {
				t.Fatalf("size = %d × %d, want %d × %d", r.width, r.height, tt.cols, tt.rows)
			}
			for _, px := range [][2]int{{0, 0}, {tt.cols - 1, 0}, {0, tt.rows - 1}, {tt.cols - 1, tt.rows - 1}, {tt.cols / 2, tt.rows / 3}} {
				col, row := px[0], px[1]
				lat, lon := tt.pixelCentre(col, row)
				want := float64(float32(tt.msl(lat, lon)))
				if got := float64(r.data[row*r.width+col]); got != want {
					t.Errorf("pixel (%d, %d) = %v, want %v", col, row, got, want)
				}
				// The georeferencing puts that pixel's centre where the writer did.
				if got := r.at(lat, lon); math.Abs(got-want) > 1e-6 {
					t.Errorf("at(%g, %g) = %v, want %v", lat, lon, got, want)
				}
			}
		})
	}
}

// findTag finds a tag in the first IFD of a little-endian classic TIFF and
// returns the byte offset of its 12-byte entry.
func findTag(t *testing.T, data []byte, tag uint16) int {
	t.Helper()
	ifd := int(binary.LittleEndian.Uint32(data[4:]))
	n := int(binary.LittleEndian.Uint16(data[ifd:]))
	for i := 0; i < n; i++ {
		off := ifd + 2 + i*12
		if binary.LittleEndian.Uint16(data[off:]) == tag {
			return off
		}
	}
	t.Fatalf("tag %d not found", tag)
	return 0
}

func TestDecodeTIFFRejectsWhatCopernicusDoesNotUse(t *testing.T) {
	setShort := func(tag, v uint16) func(*testing.T, []byte) {
		return func(t *testing.T, d []byte) { binary.LittleEndian.PutUint16(d[findTag(t, d, tag)+8:], v) }
	}
	renameTag := func(tag, to uint16) func(*testing.T, []byte) {
		return func(t *testing.T, d []byte) { binary.LittleEndian.PutUint16(d[findTag(t, d, tag):], to) }
	}
	setRasterType := func(v uint16) func(*testing.T, []byte) {
		return func(t *testing.T, d []byte) {
			e := findTag(t, d, 34735)
			keys := d[binary.LittleEndian.Uint32(d[e+8:]):]
			for i := 4; i < 16; i += 4 {
				if binary.LittleEndian.Uint16(keys[2*i:]) == 1025 {
					binary.LittleEndian.PutUint16(keys[2*(i+3):], v)
					return
				}
			}
			t.Fatal("no GTRasterTypeGeoKey")
		}
	}
	cases := []struct {
		want   string
		mutate func(*testing.T, []byte)
	}{
		{"Compression", setShort(259, 5)},
		{"BitsPerSample", setShort(258, 16)},
		{"SampleFormat", setShort(339, 1)},
		{"Predictor", setShort(317, 2)},
		{"SamplesPerPixel", setShort(277, 3)},
		{"PlanarConfiguration", setShort(284, 2)},
		{"StripOffsets", renameTag(324, 273)},
		{"ModelPixelScale", renameTag(33550, 33551)},
		{"ModelTiepoint", renameTag(33922, 33923)},
		{"GTRasterTypeGeoKey", setRasterType(3)},
		{"BigTIFF", func(_ *testing.T, d []byte) { d[2] = 43 }},
		{"TIFF", func(_ *testing.T, d []byte) { d[0], d[1] = 'X', 'X' }},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			d := encodeTestTile(t, testTile{south: 38, west: -78})
			c.mutate(t, d)
			_, err := decodeBytes(d)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one naming %s", err, c.want)
			}
		})
	}
}

func TestDecodeTIFFRejectsDamage(t *testing.T) {
	good := encodeTestTile(t, testTile{south: 38, west: -78})
	for _, d := range [][]byte{
		nil,
		good[:6],
		good[:200],
		good[:len(good)-10],
		bytes.Repeat([]byte{0xAB}, 4096),
	} {
		if _, err := decodeBytes(d); err == nil {
			t.Errorf("%d bytes decoded without error", len(d))
		}
	}
	// A block whose compressed bytes are scrambled must not decode either.
	bad := append([]byte(nil), good...)
	for i := len(bad) - 400; i < len(bad); i++ {
		bad[i] ^= 0x5A
	}
	if _, err := decodeBytes(bad); err == nil {
		t.Error("scrambled block decoded without error")
	}
}
