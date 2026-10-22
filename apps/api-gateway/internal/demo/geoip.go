package demo

import (
	"net/netip"

	"github.com/oschwald/maxminddb-golang/v2"
)

// Locator maps a visitor's IP address to a coordinate. It is the fallback for
// visitors who decline browser geolocation.
type Locator interface {
	// Locate returns the coordinate for ip, or ok=false when the address is
	// malformed or the database has no usable location for it.
	Locate(ip string) (lat, lon float64, ok bool)
}

// NopLocator never locates anything. It stands in when no database is configured.
type NopLocator struct{}

// Locate always reports ok=false.
func (NopLocator) Locate(string) (float64, float64, bool) { return 0, 0, false }

// MaxMindLocator reads a GeoLite2-City database. It is safe for concurrent use.
type MaxMindLocator struct {
	db *maxminddb.Reader
}

// OpenMaxMind opens the GeoLite2-City database at path.
func OpenMaxMind(path string) (*MaxMindLocator, error) {
	db, err := maxminddb.Open(path)
	if err != nil {
		return nil, err
	}
	return &MaxMindLocator{db: db}, nil
}

// Close releases the database.
func (m *MaxMindLocator) Close() error { return m.db.Close() }

// geoRecord decodes only the fields we need. Pointers distinguish a missing
// coordinate from a legitimate zero.
type geoRecord struct {
	Location struct {
		Latitude  *float64 `maxminddb:"latitude"`
		Longitude *float64 `maxminddb:"longitude"`
	} `maxminddb:"location"`
}

// Locate implements Locator.
func (m *MaxMindLocator) Locate(ip string) (float64, float64, bool) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return 0, 0, false
	}
	var rec geoRecord
	if err := m.db.Lookup(addr).Decode(&rec); err != nil {
		return 0, 0, false
	}
	if rec.Location.Latitude == nil || rec.Location.Longitude == nil {
		return 0, 0, false
	}
	return *rec.Location.Latitude, *rec.Location.Longitude, true
}
