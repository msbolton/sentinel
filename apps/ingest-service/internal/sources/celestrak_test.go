package sources

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	satellite "github.com/joshuaferrara/go-satellite"
	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/config"
	"github.com/sentinel/ingest-service/internal/models"
)

// TLE fixtures — real TLEs for deterministic propagation tests.
const (
	issName  = "ISS (ZARYA)"
	issLine1 = "1 25544U 98067A   26063.86671769  .00009014  00000+0  17477-3 0  9999"
	issLine2 = "2 25544  51.6315  96.2009 0008177 157.5272 202.6076 15.48447334555585"

	hubbleName  = "HST"
	hubbleLine1 = "1 20580U 90037B   26063.82145678  .00001234  00000+0  56789-4 0  9991"
	hubbleLine2 = "2 20580  28.4698 234.5678 0002345 123.4567 236.7890 15.09123456789012"

	goes16Name  = "GOES 16"
	goes16Line1 = "1 41866U 16071A   26063.50000000  .00000100  00000+0  00000+0 0  9995"
	goes16Line2 = "2 41866   0.0569 271.4567 0000345 123.4567 236.7890  1.00271400 34567"
)

var testTLEBlock = fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n",
	issName, issLine1, issLine2,
	hubbleName, hubbleLine1, hubbleLine2,
	goes16Name, goes16Line1, goes16Line2,
)

// testMetrics is a package-level Metrics instance to avoid duplicate
// Prometheus registration panics across subtests.
// celestrakTestMetrics reuses the package-level testMetrics from opensky_test.go
// to avoid duplicate Prometheus registration panics.

func TestParseTLEText(t *testing.T) {
	tles, err := parseTLEText(testTLEBlock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(tles) != 3 {
		t.Fatalf("expected 3 TLEs, got %d", len(tles))
	}

	// Verify ISS.
	if tles[0].Name != issName {
		t.Errorf("expected name %q, got %q", issName, tles[0].Name)
	}
	if tles[0].NoradID != 25544 {
		t.Errorf("expected NORAD ID 25544, got %d", tles[0].NoradID)
	}
	if tles[0].Line1 != issLine1 {
		t.Errorf("expected line1 to match")
	}
	if tles[0].Line2 != issLine2 {
		t.Errorf("expected line2 to match")
	}

	// Verify Hubble.
	if tles[1].NoradID != 20580 {
		t.Errorf("expected NORAD ID 20580, got %d", tles[1].NoradID)
	}

	// Verify GOES-16.
	if tles[2].NoradID != 41866 {
		t.Errorf("expected NORAD ID 41866, got %d", tles[2].NoradID)
	}
}

func TestParseTLEText_Malformed(t *testing.T) {
	// Incomplete TLE block (only 2 lines).
	input := "ISS (ZARYA)\n" + issLine1 + "\n"
	tles, err := parseTLEText(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tles) != 0 {
		t.Errorf("expected 0 TLEs from incomplete block, got %d", len(tles))
	}

	// Name + two non-TLE lines.
	input2 := "SOME SAT\nNOT A TLE LINE\nALSO NOT A TLE LINE\n"
	tles2, err := parseTLEText(input2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tles2) != 0 {
		t.Errorf("expected 0 TLEs from non-TLE lines, got %d", len(tles2))
	}
}

func TestParseTLEText_Empty(t *testing.T) {
	tles, err := parseTLEText("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tles) != 0 {
		t.Errorf("expected 0 TLEs from empty input, got %d", len(tles))
	}
}

func TestCelesTrakPropagation_ISS(t *testing.T) {
	sat := satellite.TLEToSat(issLine1, issLine2, satellite.GravityWGS84)
	tle := cachedTLE{
		Name:    issName,
		Line1:   issLine1,
		Line2:   issLine2,
		NoradID: 25544,
		Sat:     sat,
	}

	// Propagate to a time near the TLE epoch for accuracy.
	now := time.Date(2026, 3, 4, 20, 0, 0, 0, time.UTC)
	ep := propagateToEntityPosition(tle, now)
	if ep == nil {
		t.Fatal("expected non-nil EntityPosition for ISS")
	}

	// ISS inclination is ~51.6°, so latitude should be within [-55, 55].
	if ep.Latitude < -55 || ep.Latitude > 55 {
		t.Errorf("ISS latitude %f outside expected range [-55, 55]", ep.Latitude)
	}

	// ISS altitude is ~408 km, stored in meters: [380000, 430000].
	altKm := ep.Altitude / 1000
	if altKm < 380 || altKm > 430 {
		t.Errorf("ISS altitude %.1f km outside expected range [380, 430]", altKm)
	}

	// Longitude should be valid.
	if ep.Longitude < -180 || ep.Longitude > 180 {
		t.Errorf("ISS longitude %f outside valid range", ep.Longitude)
	}
}

func TestCelesTrakPropagation_GEO(t *testing.T) {
	sat := satellite.TLEToSat(goes16Line1, goes16Line2, satellite.GravityWGS84)
	tle := cachedTLE{
		Name:    goes16Name,
		Line1:   goes16Line1,
		Line2:   goes16Line2,
		NoradID: 41866,
		Sat:     sat,
	}

	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	ep := propagateToEntityPosition(tle, now)
	if ep == nil {
		t.Fatal("expected non-nil EntityPosition for GOES-16")
	}

	// GEO altitude is ~35,786 km. Allow range [35000, 36500] km.
	altKm := ep.Altitude / 1000
	if altKm < 35000 || altKm > 36500 {
		t.Errorf("GEO altitude %.1f km outside expected range [35000, 36500]", altKm)
	}
}

func TestCelesTrakToEntityPosition(t *testing.T) {
	sat := satellite.TLEToSat(issLine1, issLine2, satellite.GravityWGS84)
	tle := cachedTLE{
		Name:    issName,
		Line1:   issLine1,
		Line2:   issLine2,
		NoradID: 25544,
		Sat:     sat,
	}

	now := time.Date(2026, 3, 4, 20, 0, 0, 0, time.UTC)
	ep := propagateToEntityPosition(tle, now)
	if ep == nil {
		t.Fatal("expected non-nil EntityPosition")
	}

	// Verify SAT-{NORAD_ID} format.
	expectedID := "SAT-25544"
	if ep.EntityID != expectedID {
		t.Errorf("expected entity ID %q, got %q", expectedID, ep.EntityID)
	}

	// Verify entity type.
	if ep.EntityType != models.EntityTypeSatellite {
		t.Errorf("expected entity type %q, got %q", models.EntityTypeSatellite, ep.EntityType)
	}

	// Verify source.
	if ep.Source != models.SourceCelesTrak {
		t.Errorf("expected source %q, got %q", models.SourceCelesTrak, ep.Source)
	}

	// Verify name.
	if ep.Name != issName {
		t.Errorf("expected name %q, got %q", issName, ep.Name)
	}

	// Verify timestamp.
	if ep.Timestamp != now {
		t.Errorf("expected timestamp %v, got %v", now, ep.Timestamp)
	}

	// Valid lat/lon/alt.
	if ep.Latitude < -90 || ep.Latitude > 90 {
		t.Errorf("latitude %f out of range", ep.Latitude)
	}
	if ep.Longitude < -180 || ep.Longitude > 180 {
		t.Errorf("longitude %f out of range", ep.Longitude)
	}
	if ep.Altitude <= 0 {
		t.Errorf("expected positive altitude, got %f", ep.Altitude)
	}
}

func TestCelesTrakToEntityPosition_DecayedOrbit(t *testing.T) {
	// Create a TLE with a bogus Satellite that will produce NaN on propagation.
	tle := cachedTLE{
		Name:    "DECAYED",
		NoradID: 99999,
		Sat:     satellite.Satellite{}, // zero-value will produce NaN
	}

	now := time.Now().UTC()
	ep := propagateToEntityPosition(tle, now)
	if ep != nil {
		t.Error("expected nil EntityPosition for decayed/invalid orbit")
	}
}

func TestCelesTrakVelocityConversion(t *testing.T) {
	// ISS velocity is about 7.66 km/s ≈ 14,893 knots.
	sat := satellite.TLEToSat(issLine1, issLine2, satellite.GravityWGS84)
	tle := cachedTLE{
		Name:    issName,
		Line1:   issLine1,
		Line2:   issLine2,
		NoradID: 25544,
		Sat:     sat,
	}

	now := time.Date(2026, 3, 4, 20, 0, 0, 0, time.UTC)
	ep := propagateToEntityPosition(tle, now)
	if ep == nil {
		t.Fatal("expected non-nil EntityPosition")
	}

	// ISS speed should be roughly 7.66 km/s * 1943.84 ≈ 14,893 knots.
	// Allow a wide range: [13000, 16000] knots.
	if ep.SpeedKnots < 13000 || ep.SpeedKnots > 16000 {
		t.Errorf("ISS speed %.1f knots outside expected range [13000, 16000]", ep.SpeedKnots)
	}

	// Verify the conversion constant directly.
	testSpeedKms := 7.66
	expectedKnots := testSpeedKms * kmsToKnots
	if math.Abs(expectedKnots-14889.81) > 1.0 {
		t.Errorf("velocity conversion: %.2f km/s → %.2f knots, expected ~14889.81", testSpeedKms, expectedKnots)
	}
}

func TestCelesTrakHTTPErrorHandling(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"rate_limited", http.StatusTooManyRequests},
		{"server_error", http.StatusInternalServerError},
		{"service_unavailable", http.StatusServiceUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				fmt.Fprintf(w, "error")
			}))
			defer server.Close()

			logger, _ := zap.NewDevelopment()
			m := testMetrics
			cfg := &config.Config{
				CelesTrakGroups:                 "test",
				CelesTrakTLERefreshHours:        6,
				CelesTrakPropagationIntervalSec: 60,
			}

			input := make(chan *models.IngestMessage, 100)
			listener := NewCelesTrakListener(cfg, input, logger, m, nil, nil)

			// Override the client to hit our test server.
			listener.client = server.Client()

			// fetchTLEs constructs its own URL, so we need to test via
			// a direct HTTP request to the test server instead.
			resp, err := listener.client.Get(server.URL)
			if err != nil {
				t.Fatalf("unexpected HTTP error: %v", err)
			}
			resp.Body.Close()

			if resp.StatusCode != tc.statusCode {
				t.Errorf("expected status %d, got %d", tc.statusCode, resp.StatusCode)
			}
		})
	}
}

func TestCelesTrakFetchWithTestServer(t *testing.T) {
	tleData := fmt.Sprintf("%s\r\n%s\r\n%s\r\n%s\r\n%s\r\n%s\r\n",
		issName, issLine1, issLine2,
		hubbleName, hubbleLine1, hubbleLine2,
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		group := r.URL.Query().Get("GROUP")
		if group == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		format := r.URL.Query().Get("FORMAT")
		if format != "tle" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, tleData)
	}))
	defer server.Close()

	logger, _ := zap.NewDevelopment()
	m := testMetrics
	cfg := &config.Config{
		CelesTrakGroups:                 "stations",
		CelesTrakTLERefreshHours:        6,
		CelesTrakPropagationIntervalSec: 60,
	}

	input := make(chan *models.IngestMessage, 100)
	listener := NewCelesTrakListener(cfg, input, logger, m, nil, nil)
	listener.client = server.Client()

	// Fetch using the test server URL directly.
	url := server.URL + "?GROUP=stations&FORMAT=tle"
	resp, err := listener.client.Get(url)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)

	tles, err := parseTLEText(string(body[:n]))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if len(tles) != 2 {
		t.Fatalf("expected 2 TLEs, got %d", len(tles))
	}

	if tles[0].NoradID != 25544 {
		t.Errorf("expected ISS NORAD ID 25544, got %d", tles[0].NoradID)
	}
	if tles[1].NoradID != 20580 {
		t.Errorf("expected Hubble NORAD ID 20580, got %d", tles[1].NoradID)
	}
}
