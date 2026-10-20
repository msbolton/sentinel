package emplacement

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func f(v float64) *float64 { return &v }

func valid() Emplacement {
	e := New()
	e.Name, e.Mobility, e.SensorType = "Coastal radar", MobilityFixed, "RADAR"
	e.Position = &Point{Lat: 50.1, Lon: -4.2}
	e.Coverage = Coverage{MinRangeM: 0, MaxRangeM: 60000, AzimuthWidthDeg: 120}
	feed := "grpc-1" // an active emplacement needs a connection
	e.FeedID = &feed
	return e
}

func paths(err error) string {
	var ve *ValidationError
	if !errors.As(err, &ve) {
		return ""
	}
	var p []string
	for _, x := range ve.Problems {
		p = append(p, x.Path)
	}
	return strings.Join(p, ",")
}

func TestValidateAcceptsAndNormalises(t *testing.T) {
	e := valid()
	e.BoresightDeg, e.Classification = -90, "secret"
	if err := Validate(&e); err != nil {
		t.Fatalf("valid emplacement refused: %v", err)
	}
	if e.BoresightDeg != 270 || e.Classification != "SECRET" {
		t.Errorf("boresight %v classification %q", e.BoresightDeg, e.Classification)
	}
	e.BoresightDeg = 720
	_ = Validate(&e)
	if e.BoresightDeg != 0 {
		t.Errorf("720 should normalise to 0, got %v", e.BoresightDeg)
	}
}

func TestValidateEmptyCoverageAndMobileWithoutPosition(t *testing.T) {
	e := valid()
	e.Mobility, e.Position, e.Coverage = MobilityMobile, nil, Coverage{}
	if err := Validate(&e); err != nil {
		t.Fatalf("a mobile emplacement with no position and no coverage is valid: %v", err)
	}
}

func TestValidateRefuses(t *testing.T) {
	cases := map[string]struct {
		mut  func(*Emplacement)
		want string
	}{
		"no name":           {func(e *Emplacement) { e.Name = "" }, "/name"},
		"long name":         {func(e *Emplacement) { e.Name = strings.Repeat("x", 256) }, "/name"},
		"mobility":          {func(e *Emplacement) { e.Mobility = "ROLLING" }, "/mobility"},
		"sensor":            {func(e *Emplacement) { e.SensorType = "LIDAR" }, "/sensorType"},
		"azimuth reference": {func(e *Emplacement) { e.AzimuthReference = "MAGNETIC" }, "/azimuthReference"},
		"fixed no position": {func(e *Emplacement) { e.Position = nil }, "/position"},
		"lat":               {func(e *Emplacement) { e.Position.Lat = 91 }, "/position/lat"},
		"lon":               {func(e *Emplacement) { e.Position.Lon = math.Inf(1) }, "/position/lon"},
		"altitude":          {func(e *Emplacement) { e.Altitude = f(math.NaN()) }, "/altitude"},
		"boresight":         {func(e *Emplacement) { e.BoresightDeg = math.NaN() }, "/boresightDeg"},
		"min >= max":        {func(e *Emplacement) { e.Coverage.MinRangeM = 60000 }, "/coverage"},
		"too far":           {func(e *Emplacement) { e.Coverage.MaxRangeM = 2_000_001 }, "/coverage"},
		"negative min":      {func(e *Emplacement) { e.Coverage.MinRangeM = -1 }, "/coverage"},
		"width 0":           {func(e *Emplacement) { e.Coverage.AzimuthWidthDeg = 0 }, "/coverage/azimuthWidthDeg"},
		"width 361":         {func(e *Emplacement) { e.Coverage.AzimuthWidthDeg = 361 }, "/coverage/azimuthWidthDeg"},
		"elevation range":   {func(e *Emplacement) { e.Coverage.ElevationMinDeg = f(-91) }, "/coverage/elevationMinDeg"},
		"elevation order":   {func(e *Emplacement) { e.Coverage.ElevationMinDeg, e.Coverage.ElevationMaxDeg = f(10), f(5) }, "/coverage"},
		"sigma":             {func(e *Emplacement) { e.DefaultSigma.AzimuthDeg = f(0) }, "/defaultSigma/azimuthDeg"},
		"classification":    {func(e *Emplacement) { e.Classification = "RESTRICTED" }, "/classification"},
	}
	for name, c := range cases {
		e := valid()
		c.mut(&e)
		if got := paths(Validate(&e)); !strings.Contains(got, c.want) {
			t.Errorf("%s: problems at %q, want %q", name, got, c.want)
		}
	}
}

func TestInputApply(t *testing.T) {
	e := valid()
	feed := "grpc-1"
	Input{Mobility: strPtr(" mobile "), FeedID: &feed, Name: strPtr("  Ship radar ")}.Apply(&e)
	if e.Mobility != MobilityMobile || e.FeedID == nil || *e.FeedID != "grpc-1" || e.Name != "Ship radar" || e.SensorType != "RADAR" {
		t.Fatalf("applied = %+v", e)
	}
	Input{FeedID: strPtr("")}.Apply(&e)
	if e.FeedID != nil {
		t.Errorf(`feedId "" must unbind`)
	}
}

func strPtr(s string) *string { return &s }
