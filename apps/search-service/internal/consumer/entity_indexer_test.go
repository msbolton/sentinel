package consumer

import (
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

func TestDocumentFromEntity(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	en := &sentinelv1.Entity{
		Id: "e1", Name: "UAL123", EntityType: sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT, Source: sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY,
		Classification: sentinelv1.Classification_CLASSIFICATION_UNCLASSIFIED, FeedId: "opensky", AgeoutState: sentinelv1.AgeoutState_AGEOUT_STATE_LIVE,
		Position: &sentinelv1.Coordinate{Latitude: 41.9, Longitude: -87.9}, Metadata: map[string]string{"k": "v"}, CreatedAt: timestamppb.New(now), LastSeenAt: timestamppb.New(now),
		Identities: []*sentinelv1.Identity{{Type: sentinelv1.IdentityType_IDENTITY_TYPE_ICAO24, Value: "A1B2C3"}, {Type: sentinelv1.IdentityType_IDENTITY_TYPE_CALLSIGN, Value: "UAL123"}},
		Platforms:  map[string]*sentinelv1.PlatformDetail{"adsb": {Detail: &sentinelv1.PlatformDetail_Adsb{Adsb: &sentinelv1.AdsbDetail{IcaoHex: "A1B2C3", Squawk: "1200"}}}},
	}
	doc := DocumentFromEntity(en)
	if doc == nil || doc.EntityType != "AIRCRAFT" || *doc.Source != "OPENSKY" || *doc.Classification != "UNCLASSIFIED" || *doc.FeedID != "opensky" || *doc.AgeoutState != "LIVE" {
		t.Fatalf("doc = %+v", doc)
	}
	if doc.Position == nil || doc.Position.Lat != 41.9 || doc.Metadata["k"] != "v" || *doc.CreatedAt != "2026-10-15T12:00:00Z" || doc.Affiliation != nil {
		t.Errorf("fields = %+v", doc)
	}
	if len(doc.Identities) != 2 || doc.Identities[0] != "ICAO24:A1B2C3" {
		t.Errorf("identities = %v", doc.Identities)
	}
	if doc.PlatformIdentifiers == nil || *doc.PlatformIdentifiers.ICAOHex != "A1B2C3" || *doc.PlatformIdentifiers.Squawk != "1200" || *doc.PlatformIdentifiers.Callsign != "UAL123" {
		t.Errorf("platform identifiers = %+v", doc.PlatformIdentifiers)
	}
	if want := []string{"UAL123", "A1B2C3", "1200"}; !reflect.DeepEqual(doc.Identifiers, want) {
		t.Errorf("identifiers = %v, want %v", doc.Identifiers, want)
	}
	if DocumentFromEntity(nil) != nil || DocumentFromEntity(&sentinelv1.Entity{}) != nil {
		t.Error("nil or id-less entities must yield no document")
	}
}

func TestDocumentFromEntityExercise(t *testing.T) {
	doc := DocumentFromEntity(&sentinelv1.Entity{Id: "e1", Name: "SIM1", Exercise: true})
	if doc == nil || !doc.Exercise {
		t.Fatalf("exercise entity doc = %+v", doc)
	}
	if live := DocumentFromEntity(&sentinelv1.Entity{Id: "e2", Name: "UAL1"}); live == nil || live.Exercise {
		t.Fatalf("live entity doc = %+v", live)
	}
}

func TestIdentifiersCoverEveryPlatform(t *testing.T) {
	en := &sentinelv1.Entity{
		Id: "v1", Name: "EVER GIVEN", SourceEntityId: "ais-353136000",
		Platforms: map[string]*sentinelv1.PlatformDetail{
			"ais": {Detail: &sentinelv1.PlatformDetail_Ais{Ais: &sentinelv1.AisDetail{Mmsi: "353136000", Imo: "9811000", Callsign: "H3RC"}}},
			"tle": {Detail: &sentinelv1.PlatformDetail_Tle{Tle: &sentinelv1.TleDetail{NoradId: 25544}}},
		},
		Identities: []*sentinelv1.Identity{{Type: sentinelv1.IdentityType_IDENTITY_TYPE_MMSI, Value: "353136000"}},
	}
	doc := DocumentFromEntity(en)
	want := []string{"H3RC", "353136000", "9811000", "25544", "ais-353136000"}
	if !reflect.DeepEqual(doc.Identifiers, want) {
		t.Errorf("identifiers = %v, want %v", doc.Identifiers, want)
	}
	if bare := DocumentFromEntity(&sentinelv1.Entity{Id: "x", Name: "n"}); bare.Identifiers != nil {
		t.Errorf("no identifiers should stay nil, got %v", bare.Identifiers)
	}
}
