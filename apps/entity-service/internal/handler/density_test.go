package handler

import (
	"net/url"
	"testing"

	"github.com/sentinel/entity-service/internal/store"
)

func TestParseDensityQuery(t *testing.T) {
	q := func(s string) url.Values { v, _ := url.ParseQuery(s); return v }
	p, problem := parseDensityQuery(q("north=60&south=40&east=20&west=-10"))
	if problem != "" || p.North != 60 || p.West != -10 || p.CellDegrees != 5 {
		t.Errorf("default cell: %+v %q", p, problem)
	}
	p, problem = parseDensityQuery(q("north=10&south=-10&east=-170&west=170&cell=2"))
	if problem != "" || p.CellDegrees != 2 || p.East != -170 {
		t.Errorf("wrapping box: %+v %q", p, problem)
	}
	for _, bad := range []string{
		"", "north=60&south=40&east=20", "north=91&south=40&east=20&west=-10",
		"north=40&south=60&east=20&west=-10", "north=60&south=40&east=20&west=-10&cell=0.1",
		"north=60&south=40&east=20&west=-10&cell=31", "north=x&south=40&east=20&west=-10",
	} {
		if _, problem := parseDensityQuery(q(bad)); problem == "" {
			t.Errorf("%q: expected a problem", bad)
		}
	}
}

func TestDensityResponse(t *testing.T) {
	body := densityResponse(5, []store.DensityCell{{EntityType: "VESSEL", Lat: 50.5, Lon: -1.5, Count: 12}, {EntityType: "AIRCRAFT", Lat: 51, Lon: 0, Count: 3}})
	cells := body["cells"].([][4]any)
	if body["total"] != 15 || body["cellDegrees"] != 5.0 || len(cells) != 2 || cells[0][0] != "VESSEL" || cells[0][3] != 12 {
		t.Errorf("body = %+v", body)
	}
	if empty := densityResponse(5, nil); len(empty["cells"].([][4]any)) != 0 {
		t.Error("no cells must be an empty list, not null")
	}
}
