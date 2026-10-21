// Command geoidgen converts NGA's WW15MGH.GRD (the EGM96 15-arc-minute
// geoid-undulation grid) into the little-endian int16 centimetre binary that
// package terrain embeds as egm96_15.bin. It is a development tool: it is not
// run at build time, and the raw grid is not kept in the repository.
//
//	go run ./terrain/internal/cmd/geoidgen -in WW15MGH.GRD -out terrain/egm96_15.bin
//
// The grid is 721 rows (90N to 90S) by 1441 columns (0E to 360E, so the last
// column repeats the first), 1,038,961 values in metres to the millimetre.
// Each is rounded to the nearest centimetre; the largest EGM96 undulation is
// about 107 m, far inside int16.
package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

const (
	rows = 721
	cols = 1441
)

func main() {
	in := flag.String("in", "", "path to WW15MGH.GRD")
	out := flag.String("out", "egm96_15.bin", "output binary")
	flag.Parse()
	if *in == "" {
		fmt.Fprintln(os.Stderr, "geoidgen: -in is required")
		os.Exit(2)
	}
	if err := run(*in, *out); err != nil {
		fmt.Fprintln(os.Stderr, "geoidgen:", err)
		os.Exit(1)
	}
}

func run(inPath, outPath string) error {
	f, err := os.Open(inPath)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	sc.Split(bufio.ScanWords)

	// Header: south north west east dlat dlon, all in degrees.
	want := []float64{-90, 90, 0, 360, 0.25, 0.25}
	for i, w := range want {
		if !sc.Scan() {
			return fmt.Errorf("header truncated at field %d", i)
		}
		v, err := strconv.ParseFloat(sc.Text(), 64)
		if err != nil {
			return fmt.Errorf("header field %d: %w", i, err)
		}
		if v != w {
			return fmt.Errorf("header field %d = %v, want %v", i, v, w)
		}
	}

	buf := make([]byte, 0, rows*cols*2)
	n := 0
	for sc.Scan() {
		s := strings.TrimSpace(sc.Text())
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return fmt.Errorf("value %d: %w", n, err)
		}
		cm := math.Round(v * 100)
		if cm < math.MinInt16 || cm > math.MaxInt16 {
			return fmt.Errorf("value %d (%v m) does not fit int16 centimetres", n, v)
		}
		buf = binary.LittleEndian.AppendUint16(buf, uint16(int16(cm)))
		n++
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if n != rows*cols {
		return fmt.Errorf("read %d values, want %d", n, rows*cols)
	}
	return os.WriteFile(outPath, buf, 0o644)
}
