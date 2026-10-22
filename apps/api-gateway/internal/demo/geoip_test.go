package demo

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
)

// buildGeoFixture writes a tiny GeoLite2-shaped database mapping 81.2.69.0/24
// to a point in London and returns its path.
func buildGeoFixture(t *testing.T) string {
	t.Helper()
	return buildGeoFixtureWith(t, mmdbwriter.Options{DatabaseType: "GeoLite2-City", RecordSize: 28})
}

func buildGeoFixtureWith(t *testing.T, opts mmdbwriter.Options) string {
	t.Helper()
	w, err := mmdbwriter.New(opts)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	_, network, err := net.ParseCIDR("81.2.69.0/24")
	if err != nil {
		t.Fatalf("parse cidr: %v", err)
	}
	rec := mmdbtype.Map{
		"location": mmdbtype.Map{
			"latitude":  mmdbtype.Float64(51.5142),
			"longitude": mmdbtype.Float64(-0.0931),
		},
	}
	if err := w.Insert(network, rec); err != nil {
		t.Fatalf("insert: %v", err)
	}
	path := filepath.Join(t.TempDir(), "geo.mmdb")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	if _, err := w.WriteTo(f); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func openGeoFixture(t *testing.T) *MaxMindLocator {
	t.Helper()
	loc, err := OpenMaxMind(buildGeoFixture(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = loc.Close() })
	return loc
}

func TestMaxMindLocatesFixtureIP(t *testing.T) {
	loc := openGeoFixture(t)
	lat, lon, ok := loc.Locate("81.2.69.160")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if lat != 51.5142 || lon != -0.0931 {
		t.Errorf("got (%v, %v), want (51.5142, -0.0931)", lat, lon)
	}
}

func TestMaxMindLocatesIPv4MappedIPv6(t *testing.T) {
	// Without IPv4 aliasing the ::ffff:0:0/96 subtree is empty, so only an
	// unmapped lookup can find the record.
	loc, err := OpenMaxMind(buildGeoFixtureWith(t, mmdbwriter.Options{
		DatabaseType: "GeoLite2-City", RecordSize: 28, DisableIPv4Aliasing: true,
	}))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = loc.Close() })
	lat, lon, ok := loc.Locate("::ffff:81.2.69.160")
	if !ok {
		t.Fatal("expected ok=true for an IPv4-mapped IPv6 address")
	}
	if lat != 51.5142 || lon != -0.0931 {
		t.Errorf("got (%v, %v), want (51.5142, -0.0931)", lat, lon)
	}
}

func TestMaxMindUnknownIP(t *testing.T) {
	loc := openGeoFixture(t)
	if _, _, ok := loc.Locate("10.0.0.1"); ok {
		t.Error("expected ok=false for an address not in the database")
	}
}

func TestMaxMindBadIP(t *testing.T) {
	loc := openGeoFixture(t)
	for _, s := range []string{"not-an-ip", "", "81.2.69"} {
		if _, _, ok := loc.Locate(s); ok {
			t.Errorf("Locate(%q): expected ok=false", s)
		}
	}
}

func TestOpenMaxMindMissingFile(t *testing.T) {
	if _, err := OpenMaxMind(filepath.Join(t.TempDir(), "absent.mmdb")); err == nil {
		t.Error("expected an error for a missing database")
	}
}

func TestNopLocatorNeverLocates(t *testing.T) {
	var l Locator = NopLocator{}
	if _, _, ok := l.Locate("81.2.69.160"); ok {
		t.Error("NopLocator must return ok=false")
	}
}
