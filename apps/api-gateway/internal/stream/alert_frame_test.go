package stream

import (
	"testing"
	"time"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// An acknowledged alert's frame carries who and when, and moves updatedAt,
// so a client that merges by id keeps the acknowledgement.
func TestAlertFrameCarriesAcknowledgement(t *testing.T) {
	created := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	acked := created.Add(5 * time.Minute)
	ev := &sentinelv1.AlertEvent{
		EventType: sentinelv1.AlertEventType_ALERT_EVENT_TYPE_ACKNOWLEDGED,
		Alert: &sentinelv1.Alert{
			AlertId: "a1", Title: "Geofence breach", CreatedAt: timestamppb.New(created),
			AcknowledgedAt: timestamppb.New(acked), AcknowledgedBy: "w.operator",
		},
	}
	a := alertFromProto(ev)
	if !a.Acknowledged || a.AcknowledgedBy != "w.operator" || a.AcknowledgedAt != "2026-11-01T12:05:00Z" || a.UpdatedAt != "2026-11-01T12:05:00Z" {
		t.Errorf("frame = %+v", a)
	}

	fresh := alertFromProto(&sentinelv1.AlertEvent{Alert: &sentinelv1.Alert{AlertId: "a2", CreatedAt: timestamppb.New(created)}})
	if fresh.Acknowledged || fresh.AcknowledgedBy != "" || fresh.AcknowledgedAt != "" || fresh.UpdatedAt != fresh.CreatedAt {
		t.Errorf("fresh frame = %+v", fresh)
	}
}
