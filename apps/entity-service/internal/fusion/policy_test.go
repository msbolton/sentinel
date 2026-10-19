package fusion

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

func obs(feed string, source sentinelv1.EntitySource, icao string, at time.Time, lat, lon float64) *sentinelv1.Observation {
	o := &sentinelv1.Observation{
		FeedId: feed, Source: source, SourceEntityId: "ICAO-" + icao,
		SourceTimestamp: timestamppb.New(at), ReceivedAt: timestamppb.New(at.Add(time.Second)),
		Identities: []*sentinelv1.Identity{{Type: sentinelv1.IdentityType_IDENTITY_TYPE_CALLSIGN, Value: "UAL1"}, {Type: sentinelv1.IdentityType_IDENTITY_TYPE_ICAO24, Value: icao}},
	}
	if lat != 0 || lon != 0 {
		o.Position = &sentinelv1.Coordinate{Latitude: lat, Longitude: lon}
	}
	return o
}

var t0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func TestPrepareCanonicalKeyAndClamp(t *testing.T) {
	r := Prepare(obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t0, 1, 2), nil)
	if r.Key != (Key{Type: "ICAO24", Value: "A1B2C3"}) || !r.Valid || r.Reject != "" || r.Priority != 60 {
		t.Fatalf("report = %+v", r)
	}
	if r.Identities[0].Type != "ICAO24" || r.Identities[1].Type != "CALLSIGN" {
		t.Errorf("identities not canonical-first: %v", r.Identities)
	}

	future := obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t0, 1, 2)
	future.SourceTimestamp = timestamppb.New(t0.Add(time.Hour))
	if got := Prepare(future, nil).ObservedAt; !got.Equal(t0.Add(time.Second + maxFuture)) {
		t.Errorf("future timestamp not clamped: %v", got)
	}

	local := &sentinelv1.Observation{FeedId: "mqtt-1", SourceEntityId: "trk-9", Position: &sentinelv1.Coordinate{Latitude: 1, Longitude: 1}}
	if got := Prepare(local, nil).Key; got != (Key{Type: "FEED_LOCAL", Value: "mqtt-1:trk-9"}) {
		t.Errorf("feed-local key = %v", got)
	}
	if got := Prepare(local, nil).ObservedAt; got.IsZero() {
		t.Error("missing source timestamp must fall back to receipt")
	}
}

func TestPrepareRejectsBadPositions(t *testing.T) {
	bad := obs("f", sentinelv1.EntitySource_ENTITY_SOURCE_AIS, "X", t0, 91, 0)
	if r := Prepare(bad, nil); r.Valid || r.Reject != RejectInvalidPosition {
		t.Errorf("out of range = %+v", r)
	}
	none := obs("f", sentinelv1.EntitySource_ENTITY_SOURCE_AIS, "X", t0, 0, 0)
	if r := Prepare(none, nil); r.Valid || r.Reject != RejectNoPosition {
		t.Errorf("no position = %+v", r)
	}
}

func TestDedupeNewestWinsThenPriority(t *testing.T) {
	a := Prepare(obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t0, 1, 1), nil)
	b := Prepare(obs("adsb-lol", sentinelv1.EntitySource_ENTITY_SOURCE_ADSB_LOL, "A1B2C3", t0.Add(5*time.Second), 1, 1), nil)
	c := Prepare(obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t0.Add(5*time.Second), 1, 1), nil)
	other := Prepare(obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "FFFFFF", t0, 1, 1), nil)
	static := Prepare(obs("ais", sentinelv1.EntitySource_ENTITY_SOURCE_AIS, "A1B2C3", t0.Add(time.Minute), 0, 0), nil)

	out := Dedupe([]*Report{a, b, c, other, static})
	if len(out) != 5 {
		t.Fatalf("len = %d", len(out))
	}
	if a.Valid || a.Reject != RejectDuplicate {
		t.Errorf("older report must be a duplicate: %+v", a)
	}
	if !b.Valid {
		t.Errorf("adsb-lol (priority 80) must beat opensky at the same timestamp: %+v", b)
	}
	if c.Valid || c.Reject != RejectDuplicate {
		t.Errorf("same-timestamp lower priority must lose: %+v", c)
	}
	if !other.Valid {
		t.Error("a different object must keep its report")
	}
	if static.Reject != RejectNoPosition {
		t.Errorf("a static report is untouched by dedupe: %+v", static)
	}
	keys := Keys(out)
	if len(keys) != 2 || keys[0].Value != "A1B2C3" || keys[1].Value != "FFFFFF" {
		t.Errorf("keys = %v", keys)
	}
}

func TestPriorityOverride(t *testing.T) {
	p := Priority{"OPENSKY": 999}
	if p.Of("OPENSKY") != 999 || p.Of("AIS") != 0 {
		t.Errorf("override = %v", p)
	}
	if DefaultPriority.Of("ADS_B") <= DefaultPriority.Of("OPENSKY") {
		t.Error("direct receivers must outrank OpenSky")
	}
}

func TestIntelSkyRanksWithTheAggregators(t *testing.T) {
	if got := DefaultPriority.Of("INTELSKY"); got != 80 || got <= DefaultPriority.Of("OPENSKY") || got >= DefaultPriority.Of("ADS_B") {
		t.Errorf("INTELSKY priority = %d", got)
	}
}

func TestPrepareEmplacementKeyIsCanonical(t *testing.T) {
	o := &sentinelv1.Observation{FeedId: "grpc-1", SourceEntityId: "radar-7",
		Identities: []*sentinelv1.Identity{
			{Type: sentinelv1.IdentityType_IDENTITY_TYPE_CALLSIGN, Value: "RADAR 7"},
			{Type: sentinelv1.IdentityType_IDENTITY_TYPE_EMPLACEMENT, Value: "e-1"},
		},
		Position: &sentinelv1.Coordinate{Latitude: 1, Longitude: 2}}
	if got := Prepare(o, nil).Key; got != (Key{Type: "EMPLACEMENT", Value: "e-1"}) {
		t.Fatalf("key = %v", got)
	}
}
