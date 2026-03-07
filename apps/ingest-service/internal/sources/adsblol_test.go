package sources

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/config"
	"github.com/sentinel/ingest-service/internal/models"
)

var fixedTime = time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

const adsbLolFixture = `{
  "ac": [
    {
      "hex": "ae63ed",
      "flight": "ROPER23 ",
      "r": "67-14828",
      "t": "UH60L",
      "lat": 38.859155,
      "lon": -121.938660,
      "alt_baro": 3400,
      "alt_geom": 3550,
      "gs": 346.1,
      "track": 50.75,
      "squawk": "4540"
    },
    {
      "hex": "ae1234",
      "flight": "EVAC01  ",
      "r": "90-0100",
      "t": "C17",
      "lat": 34.052235,
      "lon": -118.243683,
      "alt_baro": 35000,
      "alt_geom": 35200,
      "gs": 450.0,
      "track": 270.0,
      "squawk": "1200"
    },
    {
      "hex": "a12345",
      "flight": "TOPGUN  ",
      "lat": 36.778259,
      "lon": -119.417931,
      "alt_baro": 25000,
      "gs": 500.5,
      "track": 180.0
    }
  ]
}`

const adsbLolFixtureNullPosition = `{
  "ac": [
    {
      "hex": "ae63ed",
      "flight": "ROPER23 ",
      "lat": null,
      "lon": null,
      "alt_baro": 3400,
      "gs": 346.1,
      "track": 50.75
    },
    {
      "hex": "ae1234",
      "flight": "EVAC01  ",
      "lat": 34.052235,
      "lon": -118.243683,
      "alt_baro": 35000,
      "gs": 450.0,
      "track": 270.0
    }
  ]
}`

const adsbLolFixtureGroundAlt = `{
  "ac": [
    {
      "hex": "ae63ed",
      "flight": "ROPER23 ",
      "lat": 38.859155,
      "lon": -121.938660,
      "alt_baro": "ground",
      "alt_geom": 50,
      "gs": 5.0,
      "track": 90.0
    }
  ]
}`

const adsbLolFixtureEmptyCallsign = `{
  "ac": [
    {
      "hex": "ae63ed",
      "flight": "        ",
      "lat": 38.859155,
      "lon": -121.938660,
      "alt_baro": 3400
    },
    {
      "hex": "bf5678",
      "lat": 40.0,
      "lon": -74.0,
      "alt_baro": 1000
    }
  ]
}`

const adsbLolFixtureEmpty = `{
  "ac": []
}`

const adsbLolFixtureNullAC = `{
  "ac": null
}`

func TestParseADSBLolResponse(t *testing.T) {
	aircraft, err := parseADSBLolResponse([]byte(adsbLolFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(aircraft) != 3 {
		t.Fatalf("got %d aircraft, want 3", len(aircraft))
	}

	// Verify first aircraft.
	ac := aircraft[0]
	if ac.Hex != "ae63ed" {
		t.Errorf("Hex = %q, want %q", ac.Hex, "ae63ed")
	}
	if ac.Flight == nil || *ac.Flight != "ROPER23 " {
		t.Errorf("Flight = %v, want %q", ac.Flight, "ROPER23 ")
	}
	if ac.Lat == nil || *ac.Lat != 38.859155 {
		t.Errorf("Lat = %v, want 38.859155", ac.Lat)
	}
	if ac.Lon == nil || *ac.Lon != -121.938660 {
		t.Errorf("Lon = %v, want -121.938660", ac.Lon)
	}
	if ac.AltBaro == nil || ac.AltBaro.Value != 3400 || ac.AltBaro.OnGround {
		t.Errorf("AltBaro = %v, want {3400, false}", ac.AltBaro)
	}
	if ac.GroundSpeed == nil || *ac.GroundSpeed != 346.1 {
		t.Errorf("GroundSpeed = %v, want 346.1", ac.GroundSpeed)
	}
	if ac.Track == nil || *ac.Track != 50.75 {
		t.Errorf("Track = %v, want 50.75", ac.Track)
	}
}

func TestADSBLolAircraftToEntityPosition(t *testing.T) {
	aircraft, err := parseADSBLolResponse([]byte(adsbLolFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ep := adsbLolAircraftToEntityPosition(aircraft[0], fixedTime)
	if ep == nil {
		t.Fatal("expected non-nil EntityPosition")
	}

	// Entity ID: ICAO- prefix with uppercased hex.
	if ep.EntityID != "ICAO-AE63ED" {
		t.Errorf("EntityID = %q, want %q", ep.EntityID, "ICAO-AE63ED")
	}

	if ep.EntityType != models.EntityTypeAircraft {
		t.Errorf("EntityType = %q, want %q", ep.EntityType, models.EntityTypeAircraft)
	}

	if ep.Source != models.SourceADSBLol {
		t.Errorf("Source = %q, want %q", ep.Source, models.SourceADSBLol)
	}

	// Name from trimmed callsign.
	if ep.Name != "ROPER23" {
		t.Errorf("Name = %q, want %q", ep.Name, "ROPER23")
	}

	// Coordinates.
	if ep.Latitude != 38.859155 {
		t.Errorf("Latitude = %f, want 38.859155", ep.Latitude)
	}
	if ep.Longitude != -121.938660 {
		t.Errorf("Longitude = %f, want -121.938660", ep.Longitude)
	}

	// Altitude from baro.
	if ep.Altitude != 3400 {
		t.Errorf("Altitude = %f, want 3400", ep.Altitude)
	}

	// Speed: gs is already in knots, no conversion.
	if ep.SpeedKnots != 346.1 {
		t.Errorf("SpeedKnots = %f, want 346.1 (no conversion)", ep.SpeedKnots)
	}

	// Heading from track.
	if ep.Heading != 50.75 {
		t.Errorf("Heading = %f, want 50.75", ep.Heading)
	}
	if ep.Course != 50.75 {
		t.Errorf("Course = %f, want 50.75", ep.Course)
	}
}

func TestADSBLolAircraftToEntityPosition_GroundAltitude(t *testing.T) {
	aircraft, err := parseADSBLolResponse([]byte(adsbLolFixtureGroundAlt))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ep := adsbLolAircraftToEntityPosition(aircraft[0], fixedTime)
	if ep == nil {
		t.Fatal("expected non-nil EntityPosition")
	}

	// alt_baro is "ground" — altitude should be 0 (not fallback to alt_geom).
	if ep.Altitude != 0 {
		t.Errorf("Altitude = %f, want 0 (on ground)", ep.Altitude)
	}
}

func TestADSBLolAircraftToEntityPosition_AltitudeFallback(t *testing.T) {
	// Construct an aircraft with nil alt_baro but valid alt_geom.
	fixture := `{
  "ac": [
    {
      "hex": "ae63ed",
      "flight": "TEST01  ",
      "lat": 38.0,
      "lon": -121.0,
      "alt_geom": 5000,
      "gs": 200.0,
      "track": 90.0
    }
  ]
}`
	aircraft, err := parseADSBLolResponse([]byte(fixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ep := adsbLolAircraftToEntityPosition(aircraft[0], fixedTime)
	if ep == nil {
		t.Fatal("expected non-nil EntityPosition")
	}

	if ep.Altitude != 5000 {
		t.Errorf("Altitude = %f, want 5000 (alt_geom fallback)", ep.Altitude)
	}
}

func TestADSBLolAircraftToEntityPosition_EmptyCallsign(t *testing.T) {
	aircraft, err := parseADSBLolResponse([]byte(adsbLolFixtureEmptyCallsign))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// First aircraft: whitespace-only callsign — name should fall back to hex.
	ep := adsbLolAircraftToEntityPosition(aircraft[0], fixedTime)
	if ep == nil {
		t.Fatal("expected non-nil EntityPosition")
	}
	if ep.Name != "ae63ed" {
		t.Errorf("Name = %q, want %q (hex fallback for whitespace callsign)", ep.Name, "ae63ed")
	}

	// Second aircraft: nil flight field — name should fall back to hex.
	ep = adsbLolAircraftToEntityPosition(aircraft[1], fixedTime)
	if ep == nil {
		t.Fatal("expected non-nil EntityPosition")
	}
	if ep.Name != "bf5678" {
		t.Errorf("Name = %q, want %q (hex fallback for nil callsign)", ep.Name, "bf5678")
	}
}

func TestADSBLolNullPosition(t *testing.T) {
	aircraft, err := parseADSBLolResponse([]byte(adsbLolFixtureNullPosition))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(aircraft) != 2 {
		t.Fatalf("got %d aircraft, want 2", len(aircraft))
	}

	// First aircraft has null lat/lon — should be skipped.
	ep := adsbLolAircraftToEntityPosition(aircraft[0], fixedTime)
	if ep != nil {
		t.Error("expected nil EntityPosition for aircraft with null lat/lon")
	}

	// Second aircraft is valid.
	ep = adsbLolAircraftToEntityPosition(aircraft[1], fixedTime)
	if ep == nil {
		t.Fatal("expected non-nil EntityPosition for aircraft with valid position")
	}
}

func TestADSBLolEmptyResponse(t *testing.T) {
	aircraft, err := parseADSBLolResponse([]byte(adsbLolFixtureEmpty))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(aircraft) != 0 {
		t.Errorf("got %d aircraft, want 0", len(aircraft))
	}
}

func TestADSBLolNullAC(t *testing.T) {
	aircraft, err := parseADSBLolResponse([]byte(adsbLolFixtureNullAC))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if aircraft != nil {
		t.Errorf("got %v, want nil", aircraft)
	}
}

func TestADSBLolHTTPErrorHandling(t *testing.T) {
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
				ADSBLolEnabled:     true,
				ADSBLolIntervalSec: 10,
			}
			l := NewADSBLolListener(cfg, input, logger, m, nil, nil)
			l.client = server.Client()

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

func TestADSBLolPollWithTestServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(adsbLolFixture))
	}))
	defer server.Close()

	logger := zap.NewNop()
	m := newTestMetrics()
	input := make(chan *models.IngestMessage, 100)

	cfg := &config.Config{
		ADSBLolEnabled:     true,
		ADSBLolIntervalSec: 10,
	}
	l := NewADSBLolListener(cfg, input, logger, m, nil, nil)
	l.client = server.Client()

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
