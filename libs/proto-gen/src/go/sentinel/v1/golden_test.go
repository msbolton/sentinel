package sentinelv1_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sentinel/proto-gen/pbjson"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

// The golden fixtures under libs/proto-gen/testdata are the cross-language
// contract test: this file writes them (go test ./... -update) and the
// TypeScript and Python suites decode them and assert the same values.
var update = flag.Bool("update", false, "rewrite the golden fixtures")

const testdataDir = "../../../../testdata"

func fixtureTime() *timestamppb.Timestamp {
	return timestamppb.New(time.Date(2026, 10, 15, 12, 0, 0, 500_000_000, time.UTC))
}

func goldenObservation() *sentinelv1.Observation {
	return &sentinelv1.Observation{
		ObservationId:   "3b2a7c1e-4f55-5a6b-8c9d-0e1f2a3b4c5d",
		FeedId:          "opensky",
		Driver:          "opensky",
		Decoder:         "opensky-states",
		SourceEntityId:  "ICAO-A1B2C3",
		Source:          sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY,
		SourceTimestamp: fixtureTime(),
		ReceivedAt:      timestamppb.New(fixtureTime().AsTime().Add(2 * time.Second)),
		EntityType:      sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT,
		TrackEnvironment: sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_AIR,
		DisplayName:     "UAL123",
		Identities: []*sentinelv1.Identity{
			{Type: sentinelv1.IdentityType_IDENTITY_TYPE_ICAO24, Value: "A1B2C3"},
			{Type: sentinelv1.IdentityType_IDENTITY_TYPE_CALLSIGN, Value: "UAL123"},
		},
		Position:   &sentinelv1.Coordinate{Latitude: 41.9742, Longitude: -87.9073, AltitudeMeters: proto.Float64(10668)},
		Heading:    proto.Float64(270.5),
		SpeedKnots: proto.Float64(450),
		Velocity:   &sentinelv1.GeodeticVelocity{North: 1.5, East: -231.4, Up: 0},
		Platform: &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Adsb{Adsb: &sentinelv1.AdsbDetail{
			IcaoHex: "A1B2C3", Squawk: "1200", Category: "A3", OnGround: false,
		}}},
		Raw:       &sentinelv1.RawPayload{ContentType: "application/json", Data: []byte(`["a1b2c3","UAL123 "]`)},
		ReplicaId: "ingest-1",
		FencingToken: 7,
	}
}

func goldenEntityEvent() *sentinelv1.EntityEvent {
	obs := goldenObservation()
	return &sentinelv1.EntityEvent{
		EventId:   "6f1d2c3b-4a5e-5f60-9718-293a4b5c6d7e",
		EventTime: obs.ReceivedAt,
		EntityId:  "0f8fad5b-d9cb-469f-a165-70867728950e",
		Producer:  "entity-service",
		Event: &sentinelv1.EntityEvent_Position{Position: &sentinelv1.EntityPosition{
			Entity: &sentinelv1.Entity{
				Id:             "0f8fad5b-d9cb-469f-a165-70867728950e",
				EntityType:     sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT,
				Name:           "UAL123",
				Source:         sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY,
				Classification: sentinelv1.Classification_CLASSIFICATION_UNCLASSIFIED,
				Position:       obs.Position,
				Heading:        obs.Heading,
				SpeedKnots:     obs.SpeedKnots,
				FeedId:         "opensky",
				LastFeedId:     "opensky",
				AgeoutState:    sentinelv1.AgeoutState_AGEOUT_STATE_LIVE,
				Identities:     obs.Identities[:1],
				Sources:        []*sentinelv1.SourceContribution{{FeedId: "opensky", Source: obs.Source, SourceEntityId: obs.SourceEntityId, LastObservedAt: obs.SourceTimestamp, LastReceivedAt: obs.ReceivedAt, ObservationCount: 42}},
				LastObservedAt: obs.SourceTimestamp,
				LastSeenAt:     obs.ReceivedAt,
				Platforms:      map[string]*sentinelv1.PlatformDetail{"adsb": obs.Platform},
			},
			Observation: &sentinelv1.ObservationRef{ObservationId: obs.ObservationId, FeedId: obs.FeedId, Source: obs.Source, SourceEntityId: obs.SourceEntityId, SourceTimestamp: obs.SourceTimestamp},
		}},
	}
}

func goldenAlertEvent() *sentinelv1.AlertEvent {
	return &sentinelv1.AlertEvent{
		Alert: &sentinelv1.Alert{
			AlertId:   "a1b2c3d4-e5f6-5a7b-8c9d-0e1f2a3b4c5d",
			AlertType: sentinelv1.AlertType_ALERT_TYPE_SPEED_ANOMALY,
			Severity:  sentinelv1.AlertSeverity_ALERT_SEVERITY_MEDIUM,
			Title:     "Speed anomaly",
			EntityId:  "0f8fad5b-d9cb-469f-a165-70867728950e",
			Position:  &sentinelv1.Coordinate{Latitude: 41.9742, Longitude: -87.9073},
			CreatedAt: fixtureTime(),
			Metadata:  map[string]string{"score": "0.93"},
		},
		EventType:     sentinelv1.AlertEventType_ALERT_EVENT_TYPE_TRIGGERED,
		Producer:      "analytics-service",
		AnomalyType:   "SPEED",
		Score:         0.93,
		ExpectedValue: 420,
		ActualValue:   450,
		EventTime:     fixtureTime(),
	}
}

func TestGoldenFixtures(t *testing.T) {
	cases := []struct {
		name string
		msg  proto.Message
	}{
		{"observation", goldenObservation()},
		{"entity_event", goldenEntityEvent()},
		{"alert_event", goldenAlertEvent()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bin, err := proto.MarshalOptions{Deterministic: true}.Marshal(tc.msg)
			if err != nil {
				t.Fatal(err)
			}
			js, err := pbjson.MarshalIndent(tc.msg, "  ")
			if err != nil {
				t.Fatal(err)
			}
			binPath := filepath.Join(testdataDir, tc.name+".bin")
			jsonPath := filepath.Join(testdataDir, tc.name+".json")
			if *update {
				if err := os.MkdirAll(testdataDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(binPath, bin, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(jsonPath, append(js, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(binPath)
			if err != nil {
				t.Fatalf("%v (run with -update to create the fixtures)", err)
			}
			// Decode and compare rather than comparing bytes: the fixture
			// must still describe the same message after fields are added.
			decoded := proto.Clone(tc.msg)
			proto.Reset(decoded)
			if err := proto.Unmarshal(want, decoded); err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(decoded, tc.msg) {
				t.Errorf("fixture %s no longer matches the golden message; run with -update if the change is intended", binPath)
			}
			wantJSON, err := os.ReadFile(jsonPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(bytes.TrimSpace(wantJSON), bytes.TrimSpace(js)) {
				t.Errorf("JSON fixture %s differs from pbjson output; run with -update if the change is intended", jsonPath)
			}
		})
	}
}
