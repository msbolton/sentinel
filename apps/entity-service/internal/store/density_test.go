package store

import (
	"strings"
	"testing"
)

func TestDensityArea(t *testing.T) {
	frag, args := densityArea(DensityParams{North: 60, South: 40, East: 20, West: -10}, 3)
	if !strings.Contains(frag, "ST_MakeEnvelope($3, $4, $5, $6, 4326)") || len(args) != 4 || args[0] != -10.0 || args[3] != 60.0 {
		t.Errorf("box: %s %v", frag, args)
	}
	frag, args = densityArea(DensityParams{North: 10, South: -10, East: -170, West: 170}, 3)
	if !strings.Contains(frag, " OR ") || !strings.Contains(frag, "$7") || len(args) != 8 || args[2] != 180.0 || args[4] != -180.0 {
		t.Errorf("wrapping box: %s %v", frag, args)
	}
	if frag, args := densityArea(DensityParams{North: 90, South: -90, East: 180, West: -180}, 3); frag != "" || args != nil {
		t.Errorf("globe: %s %v", frag, args)
	}
}
