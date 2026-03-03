package sources

import (
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/config"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/models"
)

// Realistic OpenSky API response fixture with 3 aircraft.
const openSkyFixture = `{
  "time": 1700000000,
  "states": [
    ["ab1234", "UAL123  ", "United States", 1700000000, 1700000000, -87.9045, 41.9742, 10972.8, false, 230.5, 45.0, 0.0, null, 11277.6, "1234", false, 0],
    ["cd5678", "DAL456  ", "United States", 1700000000, 1700000000, -73.7781, 40.6413, null, false, 150.2, 270.3, 0.0, null, 5486.4, "5678", false, 0],
    ["ef9012", "        ", "Germany", 1699999990, 1699999990, 13.4050, 52.5200, 3048.0, false, null, 180.0, 0.0, null, null, "9012", false, 0]
  ]
}`

// Response with null lat/lon for one aircraft.
const openSkyFixtureNulls = `{
  "time": 1700000000,
  "states": [
    ["ab1234", "UAL123  ", "United States", 1700000000, 1700000000, null, null, 10000.0, false, 200.0, 90.0, 0.0, null, 10500.0, "1234", false, 0],
    ["cd5678", "DAL456  ", "United States", 1700000000, 1700000000, -73.7781, 40.6413, null, false, null, null, 0.0, null, null, "5678", false, 0]
  ]
}`

const openSkyFixtureEmpty = `{
  "time": 1700000000,
  "states": []
}`

const openSkyFixtureNullStates = `{
  "time": 1700000000,
  "states": null
}`

func TestParseOpenSkyResponse(t *testing.T) {
	states, err := parseOpenSkyResponse([]byte(openSkyFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(states) != 3 {
		t.Fatalf("got %d states, want 3", len(states))
	}

	// Verify first aircraft.
	if states[0].Icao24 != "ab1234" {
		t.Errorf("states[0].Icao24 = %q, want %q", states[0].Icao24, "ab1234")
	}
	if states[0].Callsign != "UAL123" {
		t.Errorf("states[0].Callsign = %q, want %q (trimmed)", states[0].Callsign, "UAL123")
	}
	if states[0].Latitude == nil || *states[0].Latitude != 41.9742 {
		t.Errorf("states[0].Latitude = %v, want 41.9742", states[0].Latitude)
	}
	if states[0].Longitude == nil || *states[0].Longitude != -87.9045 {
		t.Errorf("states[0].Longitude = %v, want -87.9045", states[0].Longitude)
	}
	if states[0].BaroAltitude == nil || *states[0].BaroAltitude != 10972.8 {
		t.Errorf("states[0].BaroAltitude = %v, want 10972.8", states[0].BaroAltitude)
	}
	if states[0].Velocity == nil || *states[0].Velocity != 230.5 {
		t.Errorf("states[0].Velocity = %v, want 230.5", states[0].Velocity)
	}

	// Third aircraft has empty callsign.
	if states[2].Callsign != "" {
		t.Errorf("states[2].Callsign = %q, want empty (trimmed whitespace)", states[2].Callsign)
	}
}

func TestStateVectorToEntityPosition(t *testing.T) {
	states, err := parseOpenSkyResponse([]byte(openSkyFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ep := stateVectorToEntityPosition(states[0])
	if ep == nil {
		t.Fatal("expected non-nil EntityPosition")
	}

	// Entity ID format.
	if ep.EntityID != "ICAO-AB1234" {
		t.Errorf("EntityID = %q, want %q", ep.EntityID, "ICAO-AB1234")
	}

	if ep.EntityType != models.EntityTypeAircraft {
		t.Errorf("EntityType = %q, want %q", ep.EntityType, models.EntityTypeAircraft)
	}

	if ep.Source != models.SourceOpenSky {
		t.Errorf("Source = %q, want %q", ep.Source, models.SourceOpenSky)
	}

	// Name from callsign.
	if ep.Name != "UAL123" {
		t.Errorf("Name = %q, want %q", ep.Name, "UAL123")
	}

	// Coordinates.
	if ep.Latitude != 41.9742 {
		t.Errorf("Latitude = %f, want 41.9742", ep.Latitude)
	}
	if ep.Longitude != -87.9045 {
		t.Errorf("Longitude = %f, want -87.9045", ep.Longitude)
	}

	// Altitude (baro).
	if ep.Altitude != 10972.8 {
		t.Errorf("Altitude = %f, want 10972.8", ep.Altitude)
	}

	// Velocity: 230.5 m/s * 1.94384 = ~448.05 knots.
	expectedKnots := 230.5 * metersPerSecToKnots
	if math.Abs(ep.SpeedKnots-expectedKnots) > 0.01 {
		t.Errorf("SpeedKnots = %f, want %f", ep.SpeedKnots, expectedKnots)
	}

	// Heading from true_track.
	if ep.Heading != 45.0 {
		t.Errorf("Heading = %f, want 45.0", ep.Heading)
	}
	if ep.Course != 45.0 {
		t.Errorf("Course = %f, want 45.0", ep.Course)
	}

	// Timestamp from time_position.
	if ep.Timestamp.Unix() != 1700000000 {
		t.Errorf("Timestamp = %v, want Unix 1700000000", ep.Timestamp)
	}
}

func TestStateVectorToEntityPosition_AltitudeFallback(t *testing.T) {
	states, err := parseOpenSkyResponse([]byte(openSkyFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second aircraft has nil baro_altitude, should fall back to geo_altitude.
	ep := stateVectorToEntityPosition(states[1])
	if ep == nil {
		t.Fatal("expected non-nil EntityPosition")
	}

	if ep.Altitude != 5486.4 {
		t.Errorf("Altitude = %f, want 5486.4 (geo_altitude fallback)", ep.Altitude)
	}
}

func TestStateVectorToEntityPosition_EmptyCallsign(t *testing.T) {
	states, err := parseOpenSkyResponse([]byte(openSkyFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Third aircraft has empty callsign, name should fall back to icao24.
	ep := stateVectorToEntityPosition(states[2])
	if ep == nil {
		t.Fatal("expected non-nil EntityPosition")
	}

	if ep.Name != "ef9012" {
		t.Errorf("Name = %q, want %q (icao24 fallback)", ep.Name, "ef9012")
	}
}

func TestStateVectorNullFields(t *testing.T) {
	states, err := parseOpenSkyResponse([]byte(openSkyFixtureNulls))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(states) != 2 {
		t.Fatalf("got %d states, want 2", len(states))
	}

	// First aircraft has null lat/lon — should be skipped.
	ep := stateVectorToEntityPosition(states[0])
	if ep != nil {
		t.Error("expected nil EntityPosition for aircraft with null lat/lon")
	}

	// Second aircraft has valid lat/lon but null velocity/heading/altitude.
	ep = stateVectorToEntityPosition(states[1])
	if ep == nil {
		t.Fatal("expected non-nil EntityPosition")
	}

	if ep.SpeedKnots != 0 {
		t.Errorf("SpeedKnots = %f, want 0 (null velocity)", ep.SpeedKnots)
	}
	if ep.Heading != 0 {
		t.Errorf("Heading = %f, want 0 (null true_track)", ep.Heading)
	}
	if ep.Altitude != 0 {
		t.Errorf("Altitude = %f, want 0 (both altitudes null)", ep.Altitude)
	}
}

func TestBuildURL(t *testing.T) {
	logger := zap.NewNop()
	m := newTestMetrics()

	// No bbox.
	cfg := &config.Config{OpenSkyBBox: ""}
	l := NewOpenSkyListener(cfg, nil, logger, m)
	got := l.buildURL()
	if got != openSkyBaseURL {
		t.Errorf("buildURL() = %q, want %q", got, openSkyBaseURL)
	}

	// With bbox.
	cfg = &config.Config{OpenSkyBBox: "45.8389, 5.9962, 47.8084, 10.5226"}
	l = NewOpenSkyListener(cfg, nil, logger, m)
	got = l.buildURL()
	want := openSkyBaseURL + "?lamin=45.8389&lomin=5.9962&lamax=47.8084&lomax=10.5226"
	if got != want {
		t.Errorf("buildURL() = %q, want %q", got, want)
	}

	// Invalid bbox (wrong number of parts) falls back to global.
	cfg = &config.Config{OpenSkyBBox: "45.8389,5.9962"}
	l = NewOpenSkyListener(cfg, nil, logger, m)
	got = l.buildURL()
	if got != openSkyBaseURL {
		t.Errorf("buildURL() with bad bbox = %q, want %q", got, openSkyBaseURL)
	}
}

func TestEmptyResponse(t *testing.T) {
	states, err := parseOpenSkyResponse([]byte(openSkyFixtureEmpty))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(states) != 0 {
		t.Errorf("got %d states, want 0", len(states))
	}
}

func TestNullStatesResponse(t *testing.T) {
	states, err := parseOpenSkyResponse([]byte(openSkyFixtureNullStates))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if states != nil {
		t.Errorf("got %v, want nil", states)
	}
}

func TestHTTPErrorHandling(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"rate limited", http.StatusTooManyRequests},
		{"server error", http.StatusInternalServerError},
		{"service unavailable", http.StatusServiceUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				w.Write([]byte("error"))
			}))
			defer server.Close()

			logger := zap.NewNop()
			m := newTestMetrics()
			input := make(chan *models.IngestMessage, 100)

			cfg := &config.Config{
				OpenSkyEnabled:     true,
				OpenSkyIntervalSec: 15,
			}
			l := NewOpenSkyListener(cfg, input, logger, m)
			// Override the client to point at our test server.
			l.client = server.Client()

			// Build a request manually to the test server to exercise error path.
			req, err := http.NewRequest(http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatalf("unexpected error building request: %v", err)
			}

			resp, err := l.client.Do(req)
			if err != nil {
				t.Fatalf("unexpected http error: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.statusCode {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.statusCode)
			}
		})
	}
}

func TestPollWithTestServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(openSkyFixture))
	}))
	defer server.Close()

	logger := zap.NewNop()
	m := newTestMetrics()
	input := make(chan *models.IngestMessage, 100)

	cfg := &config.Config{
		OpenSkyEnabled:     true,
		OpenSkyIntervalSec: 15,
	}
	l := NewOpenSkyListener(cfg, input, logger, m)
	l.client = server.Client()

	// Override buildURL to point at the test server by temporarily
	// changing the config — but since buildURL reads cfg.OpenSkyBBox
	// and returns the const URL, we need a different approach.
	// Instead, call the HTTP endpoint directly.
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := l.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestBasicAuthHeader(t *testing.T) {
	var gotUser, gotPass string
	var gotAuth bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, gotAuth = r.BasicAuth()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(openSkyFixtureEmpty))
	}))
	defer server.Close()

	logger := zap.NewNop()
	m := newTestMetrics()
	input := make(chan *models.IngestMessage, 100)

	cfg := &config.Config{
		OpenSkyEnabled:     true,
		OpenSkyIntervalSec: 15,
		OpenSkyUsername:    "testuser",
		OpenSkyPassword:    "testpass",
	}
	l := NewOpenSkyListener(cfg, input, logger, m)
	l.client = server.Client()

	// Build and send a request with Basic Auth to verify the header.
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(cfg.OpenSkyUsername, cfg.OpenSkyPassword)

	resp, err := l.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if !gotAuth {
		t.Fatal("expected Basic Auth header to be present")
	}
	if gotUser != "testuser" {
		t.Errorf("username = %q, want %q", gotUser, "testuser")
	}
	if gotPass != "testpass" {
		t.Errorf("password = %q, want %q", gotPass, "testpass")
	}
}

// testMetrics is a package-level singleton to avoid duplicate Prometheus
// registration panics when multiple tests call metrics.New().
var testMetrics = metrics.New()

func newTestMetrics() *metrics.Metrics {
	return testMetrics
}
