package store

import (
	"encoding/json"
	"testing"
)

// The REST shape is the client's Alert model and the gateway's WebSocket
// frame: id, message, acknowledged, position.latitude/longitude, updatedAt.
func TestAlertRecordJSONMatchesClientModel(t *testing.T) {
	desc := "Entered zone"
	ack := "2026-11-01T12:05:00Z"
	by := "w.operator"
	lat, lng := 25.2, 55.3
	rec := AlertRecord{ID: "a1", Title: "Geofence breach", Description: &desc, EntityID: "e1", CreatedAt: "2026-11-01T12:00:00Z", AcknowledgedAt: &ack, AcknowledgedBy: &by}
	rec.finish(&lat, &lng)

	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"id": "a1", "message": "Entered zone", "acknowledged": true, "acknowledgedBy": "w.operator",
		"updatedAt": "2026-11-01T12:05:00Z", "createdAt": "2026-11-01T12:00:00Z",
	} {
		if got[key] != want {
			t.Errorf("%s = %v, want %v", key, got[key], want)
		}
	}
	pos, _ := got["position"].(map[string]any)
	if pos["latitude"] != 25.2 || pos["longitude"] != 55.3 {
		t.Errorf("position = %v", got["position"])
	}
	for _, gone := range []string{"alertId", "description", "lat", "lon"} {
		if _, ok := got[gone]; ok {
			t.Errorf("legacy key %q still present", gone)
		}
	}
	if _, ok := got["metadata"].(map[string]any); !ok {
		t.Errorf("metadata should be an object, got %v", got["metadata"])
	}
}

func TestAlertRecordFinishRewritesPostgresTimestamps(t *testing.T) {
	ack := "2026-10-22 00:30:00.5+00"
	rec := AlertRecord{CreatedAt: "2026-10-22 00:23:59.130352+00", AcknowledgedAt: &ack}
	rec.finish(nil, nil)
	if rec.CreatedAt != "2026-10-22T00:23:59.130352Z" || *rec.AcknowledgedAt != "2026-10-22T00:30:00.5Z" || rec.UpdatedAt != "2026-10-22T00:30:00.5Z" {
		t.Errorf("rec = %+v", rec)
	}
}

func TestAlertRecordFinishUnacknowledged(t *testing.T) {
	rec := AlertRecord{CreatedAt: "2026-11-01T12:00:00Z"}
	rec.finish(nil, nil)
	if rec.Acknowledged || rec.Position != nil || rec.UpdatedAt != rec.CreatedAt {
		t.Errorf("rec = %+v", rec)
	}
}
