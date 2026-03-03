package sources

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/config"
	"github.com/sentinel/ingest-service/internal/models"
)

// Realistic adsb.lol API response fixture with military aircraft.
const adsbLolFixture = `{
  "ac": [
    {
      "hex": "ae01ce",
      "flight": "RCH204  ",
      "lat": 52.123,
      "lon": -1.456,
      "alt_baro": 38000,
      "gs": 450,
      "track": 245.6,
      "type": "adsb_icao",
      "r": "11-1234",
      "t": "C17A"
    },
    {
      "hex": "ae0987",
      "flight": "DUKE22  ",
      "lat": 51.456,
      "lon": -0.987,
      "alt_baro": null,
      "gs": 320,
      "track": 180.0,
      "type": "adsb_icao",
      "r": "ZZ333",
      "t": "A400"
    },
    {
      "hex": "aabbcc",
      "flight": null,
      "lat": 50.789,
      "lon": 1.234,
      "alt_baro": 25000,
      "gs": null,
      "track": 90.5,
      "type": "mlat",
      "r": null,
      "t": null
    }
  ],
  "now": 1709577600000,
  "total": 216,
  "msg": "No error"
}`

// Response with null lat/lon for one aircraft.
const adsbLolFixtureNulls = `{
  "ac": [
    {
      "hex": "ae01ce",
      "flight": "RCH204  ",
      "lat": null,
      "lon": null,
      "alt_baro": 38000,
      "gs": 450,
      "track": 245.6,
      "type": "adsb_icao"
    },
    {
      "hex": "ae0987",
      "flight": "DUKE22  ",
      "lat": 51.456,
      "lon": -0.987,
      "alt_baro": null,
      "gs": null,
      "track": null,
      "type": "adsb_icao"
    }
  ],
  "now": 1709577600000,
  "total": 2,
  "msg": "No error"
}`

const adsbLolFixtureEmpty = `{
  "ac": [],
  "now": 1709577600000,
  "total": 0,
  "msg": "No error"
}`

const adsbLolFixtureNullAC = `{
  "ac": null,
  "now": 1709577600000,
  "total": 0,
  "msg": "No error"
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
	if aircraft[0].Hex != "ae01ce" {
		t.Errorf("aircraft[0].Hex = %q, want %q", aircraft[0].Hex, "ae01ce")
	}
	if aircraft[0].Flight == nil || *aircraft[0].Flight != "RCH204  " {
		t.Errorf("aircraft[0].Flight = %v, want %q", aircraft[0].Flight, "RCH204  ")
	}
	if aircraft[0].Lat == nil || *aircraft[0].Lat != 52.123 {
		t.Errorf("aircraft[0].Lat = %v, want 52.123", aircraft[0].Lat)
	}
	if aircraft[0].Lon == nil || *aircraft[0].Lon != -1.456 {
		t.Errorf("aircraft[0].Lon = %v, want -1.456", aircraft[0].Lon)
	}
	if aircraft[0].AltBaro == nil || *aircraft[0].AltBaro != 38000 {
		t.Errorf("aircraft[0].AltBaro = %v, want 38000", aircraft[0].AltBaro)
	}
	if aircraft[0].GS == nil || *aircraft[0].GS != 450 {
		t.Errorf("aircraft[0].GS = %v, want 450", aircraft[0].GS)
	}
	if aircraft[0].Track == nil || *aircraft[0].Track != 245.6 {
		t.Errorf("aircraft[0].Track = %v, want 245.6", aircraft[0].Track)
	}

	// Third aircraft has null flight callsign.
	if aircraft[2].Flight != nil {
		t.Errorf("aircraft[2].Flight = %v, want nil", aircraft[2].Flight)
	}
}

func TestADSBLolAircraftToEntityPosition(t *testing.T) {
	aircraft, err := parseADSBLolResponse([]byte(adsbLolFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ep := adsbLolAircraftToEntityPosition(aircraft[0])
	if ep == nil {
		t.Fatal("expected non-nil EntityPosition")
	}

	// Entity ID format.
	if ep.EntityID != "ICAO-AE01CE" {
		t.Errorf("EntityID = %q, want %q", ep.EntityID, "ICAO-AE01CE")
	}

	if ep.EntityType != models.EntityTypeAircraft {
		t.Errorf("EntityType = %q, want %q", ep.EntityType, models.EntityTypeAircraft)
	}

	if ep.Source != models.SourceADSBLol {
		t.Errorf("Source = %q, want %q", ep.Source, models.SourceADSBLol)
	}

	// Name from callsign (trimmed).
	if ep.Name != "RCH204" {
		t.Errorf("Name = %q, want %q (trimmed)", ep.Name, "RCH204")
	}

	// Coordinates.
	if ep.Latitude != 52.123 {
		t.Errorf("Latitude = %f, want 52.123", ep.Latitude)
	}
	if ep.Longitude != -1.456 {
		t.Errorf("Longitude = %f, want -1.456", ep.Longitude)
	}

	// Altitude (feet).
	if ep.Altitude != 38000 {
		t.Errorf("Altitude = %f, want 38000", ep.Altitude)
	}

	// Speed (knots - no conversion needed).
	if ep.SpeedKnots != 450 {
		t.Errorf("SpeedKnots = %f, want 450", ep.SpeedKnots)
	}

	// Heading from track.
	if ep.Heading != 245.6 {
		t.Errorf("Heading = %f, want 245.6", ep.Heading)
	}
	if ep.Course != 245.6 {
		t.Errorf("Course = %f, want 245.6", ep.Course)
	}
}

func TestADSBLolAircraftToEntityPosition_MissingAltitude(t *testing.T) {
	aircraft, err := parseADSBLolResponse([]byte(adsbLolFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second aircraft has nil alt_baro.
	ep := adsbLolAircraftToEntityPosition(aircraft[1])
	if ep == nil {
		t.Fatal("expected non-nil EntityPosition")
	}

	if ep.Altitude != 0 {
		t.Errorf("Altitude = %f, want 0 (null alt_baro)", ep.Altitude)
	}
}

func TestADSBLolAircraftToEntityPosition_EmptyCallsign(t *testing.T) {
	aircraft, err := parseADSBLolResponse([]byte(adsbLolFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Third aircraft has null callsign, name should fall back to hex.
	ep := adsbLolAircraftToEntityPosition(aircraft[2])
	if ep == nil {
		t.Fatal("expected non-nil EntityPosition")
	}

	if ep.Name != "aabbcc" {
		t.Errorf("Name = %q, want %q (hex fallback)", ep.Name, "aabbcc")
	}
}

func TestADSBLolAircraftNullFields(t *testing.T) {
	aircraft, err := parseADSBLolResponse([]byte(adsbLolFixtureNulls))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(aircraft) != 2 {
		t.Fatalf("got %d aircraft, want 2", len(aircraft))
	}

	// First aircraft has null lat/lon — should be skipped.
	ep := adsbLolAircraftToEntityPosition(aircraft[0])
	if ep != nil {
		t.Error("expected nil EntityPosition for aircraft with null lat/lon")
	}

	// Second aircraft has valid lat/lon but null gs/track/altitude.
	ep = adsbLolAircraftToEntityPosition(aircraft[1])
	if ep == nil {
		t.Fatal("expected non-nil EntityPosition")
	}

	if ep.SpeedKnots != 0 {
		t.Errorf("SpeedKnots = %f, want 0 (null gs)", ep.SpeedKnots)
	}
	if ep.Heading != 0 {
		t.Errorf("Heading = %f, want 0 (null track)", ep.Heading)
	}
	if ep.Altitude != 0 {
		t.Errorf("Altitude = %f, want 0 (null alt_baro)", ep.Altitude)
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

func TestADSBLolNullACResponse(t *testing.T) {
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
				ADSBLolEndpoint:    server.URL,
			}
			l := NewADSBLolListener(cfg, input, logger, m)

			// Call poll() to exercise error path.
			err := l.poll()
			if err == nil {
				t.Error("expected error, got nil")
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
		ADSBLolEndpoint:    server.URL,
	}
	l := NewADSBLolListener(cfg, input, logger, m)

	// Call poll() to fetch data.
	err := l.poll()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify messages were sent to input channel.
	// Should have 3 aircraft in fixture.
	if len(input) != 3 {
		t.Errorf("got %d messages in channel, want 3", len(input))
	}
}

func TestADSBLolPollEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(adsbLolFixtureEmpty))
	}))
	defer server.Close()

	logger := zap.NewNop()
	m := newTestMetrics()
	input := make(chan *models.IngestMessage, 100)

	cfg := &config.Config{
		ADSBLolEnabled:     true,
		ADSBLolIntervalSec: 10,
		ADSBLolEndpoint:    server.URL,
	}
	l := NewADSBLolListener(cfg, input, logger, m)

	err := l.poll()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(input) != 0 {
		t.Errorf("got %d messages in channel, want 0", len(input))
	}
}
