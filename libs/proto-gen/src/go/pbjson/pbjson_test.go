package pbjson

import (
	"encoding/json"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

func TestUpperSnake(t *testing.T) {
	cases := map[string]string{
		"EntitySource":         "ENTITY_SOURCE",
		"AgeoutState":          "AGEOUT_STATE",
		"Classification":       "CLASSIFICATION",
		"TrackProcessingState": "TRACK_PROCESSING_STATE",
		"AISData":              "AIS_DATA",
		"Mode5Code":            "MODE5_CODE",
	}
	for in, want := range cases {
		if got := upperSnake(in); got != want {
			t.Errorf("upperSnake(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMarshalStripsEnumPrefixes(t *testing.T) {
	e := &sentinelv1.Entity{
		Id:          "e1",
		EntityType:  sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT,
		Source:      sentinelv1.EntitySource_ENTITY_SOURCE_ADSB_LOL,
		AgeoutState: sentinelv1.AgeoutState_AGEOUT_STATE_LIVE,
		Identities:  []*sentinelv1.Identity{{Type: sentinelv1.IdentityType_IDENTITY_TYPE_ICAO24, Value: "A1B2C3"}},
		Sources:     []*sentinelv1.SourceContribution{{FeedId: "opensky", Source: sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY}},
		Platforms: map[string]*sentinelv1.PlatformDetail{
			"ais": {Detail: &sentinelv1.PlatformDetail_Ais{Ais: &sentinelv1.AisDetail{Mmsi: "123", NavStatus: sentinelv1.NavigationalStatus_NAVIGATIONAL_STATUS_MOORED}}},
		},
		Heading:    proto.Float64(0),
		LastSeenAt: timestamppb.New(timestamppb.Now().AsTime()),
	}
	data, err := Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"entityType": "AIRCRAFT", "source": "ADSB_LOL", "ageoutState": "LIVE"}
	for k, v := range want {
		if obj[k] != v {
			t.Errorf("%s = %v, want %s", k, obj[k], v)
		}
	}
	if obj["identities"].([]any)[0].(map[string]any)["type"] != "ICAO24" {
		t.Errorf("nested list enum not stripped: %v", obj["identities"])
	}
	if obj["sources"].([]any)[0].(map[string]any)["source"] != "OPENSKY" {
		t.Errorf("repeated message enum not stripped: %v", obj["sources"])
	}
	ais := obj["platforms"].(map[string]any)["ais"].(map[string]any)["ais"].(map[string]any)
	if ais["navStatus"] != "MOORED" {
		t.Errorf("map value enum not stripped: %v", ais)
	}
	if _, ok := obj["heading"]; !ok {
		t.Errorf("explicit-presence zero heading must be emitted: %s", data)
	}
	if _, ok := obj["speedKnots"]; ok {
		t.Errorf("unset optional must be omitted: %s", data)
	}
	if _, ok := obj["lastSeenAt"].(string); !ok {
		t.Errorf("timestamp must stay an RFC 3339 string: %v", obj["lastSeenAt"])
	}
}

func TestUnmarshalAcceptsBareAndPrefixedNames(t *testing.T) {
	for _, in := range []string{
		`{"id":"e1","entityType":"VESSEL","source":"AIS","identities":[{"type":"MMSI","value":"1"}]}`,
		`{"id":"e1","entityType":"ENTITY_TYPE_VESSEL","source":"ENTITY_SOURCE_AIS","identities":[{"type":"IDENTITY_TYPE_MMSI","value":"1"}]}`,
		`{"id":"e1","entityType":3,"source":8,"identities":[{"type":2,"value":"1"}],"unknownField":true}`,
	} {
		var e sentinelv1.Entity
		if err := Unmarshal([]byte(in), &e); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if e.EntityType != sentinelv1.EntityType_ENTITY_TYPE_VESSEL || e.Source != sentinelv1.EntitySource_ENTITY_SOURCE_AIS {
			t.Errorf("%s: got type %v source %v", in, e.EntityType, e.Source)
		}
		if len(e.Identities) != 1 || e.Identities[0].Type != sentinelv1.IdentityType_IDENTITY_TYPE_MMSI {
			t.Errorf("%s: identities %v", in, e.Identities)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	in := &sentinelv1.Observation{
		ObservationId: "o1",
		FeedId:        "opensky",
		Source:        sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY,
		EntityType:    sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT,
		Position:      &sentinelv1.Coordinate{Latitude: 1, Longitude: 2, AltitudeMeters: proto.Float64(0)},
		Course:        proto.Float64(90),
		Platform:      &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Adsb{Adsb: &sentinelv1.AdsbDetail{IcaoHex: "ABC"}}},
	}
	data, err := Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out sentinelv1.Observation
	if err := Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(in, &out) {
		t.Errorf("round trip mismatch:\n%s", data)
	}
}

func TestEnumHelpers(t *testing.T) {
	if got := EnumName(sentinelv1.EntitySource_ENTITY_SOURCE_ADSB_LOL); got != "ADSB_LOL" {
		t.Errorf("EnumName = %q", got)
	}
	if got := EnumName(sentinelv1.EntityType_ENTITY_TYPE_UNKNOWN); got != "UNKNOWN" {
		t.Errorf("EnumName zero = %q", got)
	}
	if v, ok := ParseEnum[sentinelv1.EntitySource]("ADSB_LOL"); !ok || v != sentinelv1.EntitySource_ENTITY_SOURCE_ADSB_LOL {
		t.Errorf("ParseEnum bare = %v %v", v, ok)
	}
	if v, ok := ParseEnum[sentinelv1.AgeoutState]("AGEOUT_STATE_STALE"); !ok || v != sentinelv1.AgeoutState_AGEOUT_STATE_STALE {
		t.Errorf("ParseEnum prefixed = %v %v", v, ok)
	}
	if _, ok := ParseEnum[sentinelv1.EntitySource]("NOPE"); ok {
		t.Error("ParseEnum unknown must fail")
	}
}
