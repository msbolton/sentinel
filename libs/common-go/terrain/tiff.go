package terrain

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// This is a minimal GeoTIFF reader for exactly what the Copernicus GLO-30
// cloud-optimised GeoTIFFs use: classic TIFF, one band of 32-bit IEEE floats,
// tiled, DEFLATE, predictor 1 (none) or 3 (floating point), georeferenced with
// ModelTiepoint and ModelPixelScale. Only the first image (full resolution) is
// read; the overviews after it are ignored. Anything else is refused with an
// error naming the tag.

// raster is a decoded grid of heights with its georeferencing.
type raster struct {
	width, height int
	data          []float32 // row-major, northernmost row first
	lon0, lat0    float64   // centre of pixel (0, 0)
	dLon, dLat    float64   // pixel spacing in degrees; rows run south
}

const (
	tagImageWidth          = 256
	tagImageLength         = 257
	tagBitsPerSample       = 258
	tagCompression         = 259
	tagStripOffsets        = 273
	tagSamplesPerPixel     = 277
	tagPlanarConfiguration = 284
	tagPredictor           = 317
	tagTileWidth           = 322
	tagTileLength          = 323
	tagTileOffsets         = 324
	tagTileByteCounts      = 325
	tagSampleFormat        = 339
	tagModelPixelScale     = 33550
	tagModelTiepoint       = 33922
	tagModelTransformation = 34264
	tagGeoKeyDirectory     = 34735

	geoKeyRasterType = 1025 // GTRasterTypeGeoKey
	rasterPixelArea  = 1
	rasterPixelPoint = 2

	maxPixels = 64 << 20 // far above the 3600 × 3600 of a GLO-30 tile
)

var errTIFF = errors.New("terrain: tiff")

func tiffErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errTIFF, fmt.Sprintf(format, args...))
}

type ifdEntry struct {
	typ   uint16
	count uint32
	raw   [4]byte // the value, or its offset when it does not fit
}

type tiffReader struct {
	r     io.ReaderAt
	size  int64
	order binary.ByteOrder
	tags  map[uint16]ifdEntry
}

// decodeTIFF reads the full-resolution image of a GLO-30 GeoTIFF.
func decodeTIFF(r io.ReaderAt, size int64) (*raster, error) {
	t := &tiffReader{r: r, size: size}
	var hdr [8]byte
	if err := t.readAt(hdr[:], 0); err != nil {
		return nil, tiffErr("not a TIFF: %v", err)
	}
	switch string(hdr[:2]) {
	case "II":
		t.order = binary.LittleEndian
	case "MM":
		t.order = binary.BigEndian
	default:
		return nil, tiffErr("not a TIFF: bad byte-order mark")
	}
	switch t.order.Uint16(hdr[2:]) {
	case 42:
	case 43:
		return nil, tiffErr("BigTIFF is not supported")
	default:
		return nil, tiffErr("not a TIFF: bad magic number")
	}
	if err := t.readIFD(int64(t.order.Uint32(hdr[4:]))); err != nil {
		return nil, err
	}

	width, err := t.one(tagImageWidth, "ImageWidth")
	if err != nil {
		return nil, err
	}
	height, err := t.one(tagImageLength, "ImageLength")
	if err != nil {
		return nil, err
	}
	if width < 2 || height < 2 || width*height > maxPixels {
		return nil, tiffErr("ImageWidth × ImageLength %d × %d is out of range", width, height)
	}
	for _, c := range []struct {
		tag  uint16
		name string
		def  uint64
		ok   func(uint64) bool
	}{
		{tagBitsPerSample, "BitsPerSample", 1, func(v uint64) bool { return v == 32 }},
		{tagSampleFormat, "SampleFormat", 1, func(v uint64) bool { return v == 3 }},
		{tagSamplesPerPixel, "SamplesPerPixel", 1, func(v uint64) bool { return v == 1 }},
		{tagPlanarConfiguration, "PlanarConfiguration", 1, func(v uint64) bool { return v == 1 }},
		{tagCompression, "Compression", 1, func(v uint64) bool { return v == 8 || v == 32946 }},
		{tagPredictor, "Predictor", 1, func(v uint64) bool { return v == 1 || v == 3 }},
	} {
		v, err := t.optional(c.tag, c.name, c.def)
		if err != nil {
			return nil, err
		}
		if !c.ok(v) {
			return nil, tiffErr("%s %d is not supported", c.name, v)
		}
	}
	predictor, _ := t.optional(tagPredictor, "Predictor", 1)

	if _, tiled := t.tags[tagTileOffsets]; !tiled {
		if _, ok := t.tags[tagStripOffsets]; ok {
			return nil, tiffErr("StripOffsets: striped images are not supported, only tiled")
		}
		return nil, tiffErr("missing TileOffsets")
	}
	tw, err := t.one(tagTileWidth, "TileWidth")
	if err != nil {
		return nil, err
	}
	th, err := t.one(tagTileLength, "TileLength")
	if err != nil {
		return nil, err
	}
	if tw == 0 || th == 0 || tw*th > maxPixels {
		return nil, tiffErr("TileWidth × TileLength %d × %d is out of range", tw, th)
	}
	offsets, err := t.uints(tagTileOffsets, "TileOffsets")
	if err != nil {
		return nil, err
	}
	counts, err := t.uints(tagTileByteCounts, "TileByteCounts")
	if err != nil {
		return nil, err
	}
	across, down := (width+tw-1)/tw, (height+th-1)/th
	if uint64(len(offsets)) != across*down || len(counts) != len(offsets) {
		return nil, tiffErr("TileOffsets/TileByteCounts have %d/%d entries, want %d", len(offsets), len(counts), across*down)
	}

	ras := &raster{width: int(width), height: int(height)}
	if err := t.georeference(ras); err != nil {
		return nil, err
	}

	ras.data = make([]float32, width*height)
	tileBytes := make([]byte, tw*th*4)
	row := make([]float32, tw)
	var src []byte
	var zr io.ReadCloser
	for i := range offsets {
		off, n := offsets[i], counts[i]
		if n == 0 || off > uint64(t.size) || n > uint64(t.size)-off {
			return nil, tiffErr("tile %d lies outside the file", i)
		}
		if uint64(cap(src)) < n {
			src = make([]byte, n)
		}
		src = src[:n]
		if err := t.readAt(src, int64(off)); err != nil {
			return nil, tiffErr("tile %d: %v", i, err)
		}
		if zr == nil {
			zr, err = zlib.NewReader(bytes.NewReader(src))
		} else {
			err = zr.(zlib.Resetter).Reset(bytes.NewReader(src), nil)
		}
		if err != nil {
			return nil, tiffErr("tile %d: deflate: %v", i, err)
		}
		if _, err := io.ReadFull(zr, tileBytes); err != nil {
			return nil, tiffErr("tile %d: deflate: %v", i, err)
		}
		// Reading on to the end makes zlib check the stream's checksum.
		if _, err := zr.Read(make([]byte, 1)); err != io.EOF {
			return nil, tiffErr("tile %d: deflate: trailing data or bad checksum (%v)", i, err)
		}

		tx, ty := uint64(i)%across, uint64(i)/across
		for r := uint64(0); r < th; r++ {
			y := ty*th + r
			if y >= height {
				break
			}
			line := tileBytes[r*tw*4 : (r+1)*tw*4]
			if predictor == 3 {
				undoFloatPredictor(line, row)
			} else {
				for c := range row {
					row[c] = math.Float32frombits(t.order.Uint32(line[4*c:]))
				}
			}
			x0 := tx * tw
			n := min(tw, width-x0)
			copy(ras.data[y*width+x0:y*width+x0+n], row[:n])
		}
	}
	return ras, nil
}

// undoFloatPredictor reverses TIFF predictor 3 (Technical Note 3) on one row
// of a tile: the row is horizontally differenced byte by byte, and its bytes
// are split into planes, most significant byte first, whatever the file's
// byte order.
func undoFloatPredictor(line []byte, out []float32) {
	for i := 1; i < len(line); i++ {
		line[i] += line[i-1]
	}
	w := len(out)
	for c := range out {
		bits := uint32(line[c])<<24 | uint32(line[w+c])<<16 | uint32(line[2*w+c])<<8 | uint32(line[3*w+c])
		out[c] = math.Float32frombits(bits)
	}
}

// georeference fills in where pixel centres lie, honouring the raster type:
// with PixelIsArea (the default, and what Copernicus uses) the tiepoint is a
// pixel's corner; with PixelIsPoint it is the pixel's centre.
func (t *tiffReader) georeference(ras *raster) error {
	if _, ok := t.tags[tagModelPixelScale]; !ok {
		if _, ok := t.tags[tagModelTransformation]; ok {
			return tiffErr("ModelTransformation is not supported; need ModelPixelScale and ModelTiepoint")
		}
		return tiffErr("missing ModelPixelScale")
	}
	scale, err := t.doubles(tagModelPixelScale, "ModelPixelScale")
	if err != nil {
		return err
	}
	if _, ok := t.tags[tagModelTiepoint]; !ok {
		return tiffErr("missing ModelTiepoint")
	}
	tie, err := t.doubles(tagModelTiepoint, "ModelTiepoint")
	if err != nil {
		return err
	}
	if len(scale) < 2 || !(scale[0] > 0) || !(scale[1] > 0) || math.IsInf(scale[0], 0) || math.IsInf(scale[1], 0) {
		return tiffErr("ModelPixelScale %v is not usable", scale)
	}
	if len(tie) < 6 {
		return tiffErr("ModelTiepoint has %d values, want 6", len(tie))
	}

	rasterType := uint64(rasterPixelArea)
	if _, ok := t.tags[tagGeoKeyDirectory]; ok {
		keys, err := t.uints(tagGeoKeyDirectory, "GeoKeyDirectory")
		if err != nil {
			return err
		}
		if len(keys) < 4 || uint64(len(keys)) < 4+4*keys[3] {
			return tiffErr("GeoKeyDirectory is truncated")
		}
		for k := uint64(0); k < keys[3]; k++ {
			e := keys[4+4*k:]
			if e[0] == geoKeyRasterType {
				if e[1] != 0 || e[2] != 1 {
					return tiffErr("GTRasterTypeGeoKey is not an inline short")
				}
				rasterType = e[3]
			}
		}
	}
	// Tiepoint (I, J, K, X, Y, Z): raster position (I, J) is at (X, Y).
	ras.dLon, ras.dLat = scale[0], scale[1]
	ras.lon0 = tie[3] - tie[0]*ras.dLon
	ras.lat0 = tie[4] + tie[1]*ras.dLat
	switch rasterType {
	case rasterPixelArea:
		ras.lon0 += ras.dLon / 2
		ras.lat0 -= ras.dLat / 2
	case rasterPixelPoint:
	default:
		return tiffErr("GTRasterTypeGeoKey %d is not supported", rasterType)
	}
	return nil
}

func (t *tiffReader) readAt(p []byte, off int64) error {
	if off < 0 || off > t.size || int64(len(p)) > t.size-off {
		return io.ErrUnexpectedEOF
	}
	_, err := t.r.ReadAt(p, off)
	if err == io.EOF {
		err = nil // ReadAt may report EOF alongside a full read at the end
	}
	return err
}

func (t *tiffReader) readIFD(off int64) error {
	var n [2]byte
	if err := t.readAt(n[:], off); err != nil {
		return tiffErr("IFD: %v", err)
	}
	count := int(t.order.Uint16(n[:]))
	buf := make([]byte, count*12)
	if err := t.readAt(buf, off+2); err != nil {
		return tiffErr("IFD: %v", err)
	}
	t.tags = make(map[uint16]ifdEntry, count)
	for i := 0; i < count; i++ {
		b := buf[i*12:]
		e := ifdEntry{typ: t.order.Uint16(b[2:]), count: t.order.Uint32(b[4:])}
		copy(e.raw[:], b[8:12])
		t.tags[t.order.Uint16(b)] = e
	}
	return nil
}

// value returns the bytes of a tag's value, following the offset if needed.
func (t *tiffReader) value(e ifdEntry, elemSize int, name string) ([]byte, error) {
	n := int64(e.count) * int64(elemSize)
	if n > t.size {
		return nil, tiffErr("%s is larger than the file", name)
	}
	if n <= 4 {
		return e.raw[:n], nil
	}
	b := make([]byte, n)
	if err := t.readAt(b, int64(t.order.Uint32(e.raw[:]))); err != nil {
		return nil, tiffErr("%s: %v", name, err)
	}
	return b, nil
}

// uints reads a SHORT or LONG tag.
func (t *tiffReader) uints(tag uint16, name string) ([]uint64, error) {
	e, ok := t.tags[tag]
	if !ok {
		return nil, tiffErr("missing %s", name)
	}
	var size int
	switch e.typ {
	case 3: // SHORT
		size = 2
	case 4: // LONG
		size = 4
	default:
		return nil, tiffErr("%s has TIFF type %d, want SHORT or LONG", name, e.typ)
	}
	b, err := t.value(e, size, name)
	if err != nil {
		return nil, err
	}
	out := make([]uint64, e.count)
	for i := range out {
		if size == 2 {
			out[i] = uint64(t.order.Uint16(b[2*i:]))
		} else {
			out[i] = uint64(t.order.Uint32(b[4*i:]))
		}
	}
	return out, nil
}

// doubles reads a DOUBLE tag.
func (t *tiffReader) doubles(tag uint16, name string) ([]float64, error) {
	e := t.tags[tag]
	if e.typ != 12 {
		return nil, tiffErr("%s has TIFF type %d, want DOUBLE", name, e.typ)
	}
	b, err := t.value(e, 8, name)
	if err != nil {
		return nil, err
	}
	out := make([]float64, e.count)
	for i := range out {
		out[i] = math.Float64frombits(t.order.Uint64(b[8*i:]))
	}
	return out, nil
}

// one reads a required single-valued integer tag.
func (t *tiffReader) one(tag uint16, name string) (uint64, error) {
	v, err := t.uints(tag, name)
	if err != nil {
		return 0, err
	}
	if len(v) != 1 {
		return 0, tiffErr("%s has %d values, want 1", name, len(v))
	}
	return v[0], nil
}

// optional reads an integer tag that may be absent, giving def if it is.
// A per-sample tag such as BitsPerSample must agree across its values.
func (t *tiffReader) optional(tag uint16, name string, def uint64) (uint64, error) {
	if _, ok := t.tags[tag]; !ok {
		return def, nil
	}
	v, err := t.uints(tag, name)
	if err != nil {
		return 0, err
	}
	if len(v) == 0 {
		return 0, tiffErr("%s is empty", name)
	}
	for _, x := range v[1:] {
		if x != v[0] {
			return 0, tiffErr("%s values differ", name)
		}
	}
	return v[0], nil
}
