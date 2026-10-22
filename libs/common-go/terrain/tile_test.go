package terrain

import "testing"

func TestTileKey(t *testing.T) {
	cases := []struct {
		lat, lon float64
		want     string
	}{
		{38.5, -77.5, "Copernicus_DSM_COG_10_N38_00_W078_00_DEM"},
		{-0.5, -0.5, "Copernicus_DSM_COG_10_S01_00_W001_00_DEM"},
		{10, 179.9, "Copernicus_DSM_COG_10_N10_00_E179_00_DEM"},
		{0, 0, "Copernicus_DSM_COG_10_N00_00_E000_00_DEM"},
		{39, -77, "Copernicus_DSM_COG_10_N39_00_W077_00_DEM"}, // an exact corner belongs to the tile to its north-east
		{-90, -180, "Copernicus_DSM_COG_10_S90_00_W180_00_DEM"},
		{90, 0, "Copernicus_DSM_COG_10_N89_00_E000_00_DEM"},      // the pole sits in the last row of tiles
		{45, 180, "Copernicus_DSM_COG_10_N45_00_W180_00_DEM"},    // longitude wraps
		{45, -180.5, "Copernicus_DSM_COG_10_N45_00_E179_00_DEM"}, // longitude wraps
		{60.2, 370.4, "Copernicus_DSM_COG_10_N60_00_E010_00_DEM"},
	}
	for _, c := range cases {
		if got := TileKey(c.lat, c.lon); got != c.want {
			t.Errorf("TileKey(%g, %g) = %q, want %q", c.lat, c.lon, got, c.want)
		}
	}
}

func TestParseTileKeyRoundTrips(t *testing.T) {
	for _, id := range []tileID{{38, -78}, {-1, -1}, {10, 179}, {0, 0}, {-90, -180}, {89, 0}} {
		got, ok := parseTileKey(id.key())
		if !ok || got != id {
			t.Errorf("parseTileKey(%q) = %v, %v; want %v", id.key(), got, ok, id)
		}
	}
	for _, bad := range []string{"", "tileList.txt", "Copernicus_DSM_COG_10_N38_00_W078_00", "Copernicus_DSM_COG_10_X38_00_W078_00_DEM", "Copernicus_DSM_COG_10_N91_00_W078_00_DEM", "Copernicus_DSM_COG_10_N38_00_W181_00_DEM"} {
		if got, ok := parseTileKey(bad); ok {
			t.Errorf("parseTileKey(%q) = %v, want rejection", bad, got)
		}
	}
}

func TestTilesInBox(t *testing.T) {
	cases := []struct {
		s, w, n, e float64
		want       int
	}{
		{38, -78, 40, -76, 4},         // 2 × 2° on whole degrees
		{38.5, -77.5, 38.5, -77.5, 1}, // a point
		{38.5, -77.5, 39.5, -76.5, 4}, // straddles two edges
		{-0.5, -0.5, 0.5, 0.5, 4},     // around null island
		{10, 179.5, 11, -179.5, 2},    // across the antimeridian
		{38.2, -77.8, 38.4, -77.6, 1}, // inside one tile
		{89.5, 0, 90, 1, 1},           // up to the pole
		{40, -78, 38, -76, 4},         // south and north given the wrong way round
	}
	for _, c := range cases {
		if got := tilesInBox(c.s, c.w, c.n, c.e); len(got) != c.want {
			t.Errorf("tilesInBox(%g, %g, %g, %g) = %v, want %d tiles", c.s, c.w, c.n, c.e, got, c.want)
		}
	}
}

func TestKnownGaps(t *testing.T) {
	for _, c := range []struct {
		id   tileID
		want bool
	}{
		{tileID{40, 44}, true}, // Yerevan
		{tileID{40, 49}, true}, // Baku
		{tileID{38, 43}, true},
		{tileID{41, 50}, true},
		{tileID{37, 44}, false},
		{tileID{42, 44}, false},
		{tileID{40, 42}, false},
		{tileID{40, 51}, false},
	} {
		if got := inKnownGap(c.id); got != c.want {
			t.Errorf("inKnownGap(%v) = %v, want %v", c.id, got, c.want)
		}
	}
}
