package terrain

// knownGaps are land areas the public Copernicus GLO-30 release leaves out.
// A tile missing from tileList.txt is normally open sea and reads as 0 m above
// mean sea level; inside one of these boxes a missing tile is land we have no
// heights for, so it is reported unavailable instead.
//
// Bounds are tile south-west corners, inclusive at both ends.
var knownGaps = []struct {
	name                     string
	south, north, west, east int
}{
	// Armenia and Azerbaijan: tiles N38–N41 × E043–E050. The bucket lists
	// only seven of these 32 tiles (N38 E043, E044, E047, E050; N39 E043,
	// E050; N41 E050). The box also holds a few Caspian tiles that are truly
	// sea; reporting those unavailable is the safe side of the trade.
	{name: "Armenia and Azerbaijan", south: 38, north: 41, west: 43, east: 50},
}

// inKnownGap reports whether a tile falls inside a known gap.
func inKnownGap(id tileID) bool {
	for _, g := range knownGaps {
		if id.lat >= g.south && id.lat <= g.north && id.lon >= g.west && id.lon <= g.east {
			return true
		}
	}
	return false
}
