package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCustomFeed_JSONRoundTrip(t *testing.T) {
	feed := CustomFeed{
		ID:            uuid.New(),
		Name:          "Test MQTT Feed",
		ConnectorType: "mqtt",
		Format:        "json",
		Config:        json.RawMessage(`{"broker_url":"tcp://localhost:1883","topics":["test/#"],"qos":1}`),
		Enabled:       true,
		CreatedAt:     time.Now().UTC().Truncate(time.Millisecond),
		UpdatedAt:     time.Now().UTC().Truncate(time.Millisecond),
	}

	data, err := json.Marshal(feed)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded CustomFeed
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ID != feed.ID {
		t.Errorf("ID = %v, want %v", decoded.ID, feed.ID)
	}
	if decoded.Name != feed.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, feed.Name)
	}
	if decoded.ConnectorType != feed.ConnectorType {
		t.Errorf("ConnectorType = %q, want %q", decoded.ConnectorType, feed.ConnectorType)
	}
	if decoded.Format != feed.Format {
		t.Errorf("Format = %q, want %q", decoded.Format, feed.Format)
	}
	if decoded.Enabled != feed.Enabled {
		t.Errorf("Enabled = %v, want %v", decoded.Enabled, feed.Enabled)
	}

	// Verify config JSON is preserved.
	var cfg map[string]interface{}
	if err := json.Unmarshal(decoded.Config, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if cfg["broker_url"] != "tcp://localhost:1883" {
		t.Errorf("config broker_url = %v, want tcp://localhost:1883", cfg["broker_url"])
	}
}

func TestCustomFeed_ConnectorTypes(t *testing.T) {
	validTypes := []string{"mqtt", "stomp", "tcp"}
	for _, ct := range validTypes {
		feed := CustomFeed{
			ID:            uuid.New(),
			Name:          "Test " + ct,
			ConnectorType: ct,
			Format:        "json",
			Config:        json.RawMessage(`{}`),
		}
		data, err := json.Marshal(feed)
		if err != nil {
			t.Errorf("marshal %s: %v", ct, err)
			continue
		}
		var decoded CustomFeed
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Errorf("unmarshal %s: %v", ct, err)
			continue
		}
		if decoded.ConnectorType != ct {
			t.Errorf("ConnectorType = %q, want %q", decoded.ConnectorType, ct)
		}
	}
}

func TestCustomFeed_Formats(t *testing.T) {
	validFormats := []string{"json", "nmea", "cot", "ais", "adsb", "link16"}
	for _, f := range validFormats {
		feed := CustomFeed{
			ID:            uuid.New(),
			Name:          "Test " + f,
			ConnectorType: "mqtt",
			Format:        f,
			Config:        json.RawMessage(`{}`),
		}
		data, err := json.Marshal(feed)
		if err != nil {
			t.Errorf("marshal %s: %v", f, err)
			continue
		}
		var decoded CustomFeed
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Errorf("unmarshal %s: %v", f, err)
			continue
		}
		if decoded.Format != f {
			t.Errorf("Format = %q, want %q", decoded.Format, f)
		}
	}
}
