package grpcin

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/ingesttoken"
	"github.com/sentinel/ingest-service/internal/obs"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

// liveRig runs a driver with the heartbeat model on, a hand-moved clock and
// a fast re-publish tick, over emps.
func liveRig(t *testing.T, cfg map[string]any, tok ingesttoken.Token, emps *fakeEmplacements) (*rig, *fakeWriter, *testClock, ingestv1.IngestService_StreamReportsClient) {
	t.Helper()
	w := &fakeWriter{emps: emps}
	clk := newClock()
	tok.ID, tok.FeedID = "t1", "grpc-1"
	r := startWith(t, cfg, nil, tokensWith(secretA, tok), emps, w, nil, func(d *Driver) {
		clk.tweak(d)
		d.heartbeatsOff = false
		d.republishTick = 5 * time.Millisecond
	})
	return r, w, clk, openStream(t, r, secretA)
}

func rowsOf(rows ...emplacement.Emplacement) *fakeEmplacements { return &fakeEmplacements{rows: rows} }

// heartbeat is a state report with nothing in it but the emplacement and
// its interval: all a fixed site sends.
func heartbeat(id string, intervalSec uint32) *ingestv1.Report {
	return &ingestv1.Report{ClientRef: "hb", EmplacementId: id, Body: &ingestv1.Report_EmplacementState{
		EmplacementState: &sentinelv1.EmplacementState{HeartbeatIntervalSec: intervalSec}}}
}

// sitesOf is every SENSOR observation of emplacement id the driver emitted.
func sitesOf(r *rig, id string) []*obs.Observation {
	var out []*obs.Observation
	for _, o := range r.sink.emitted() {
		if o.EntityType == obs.EntitySensor && o.SourceEntityID == id {
			out = append(out, o)
		}
	}
	return out
}

func lastSite(r *rig, id string) *obs.Observation {
	all := sitesOf(r, id)
	if len(all) == 0 {
		return nil
	}
	return all[len(all)-1]
}

func sitesIn(r *rig, id string, state emplacement.HeartbeatState) int {
	n := 0
	for _, o := range sitesOf(r, id) {
		if o.Generic[emplacement.FieldHeartbeat] == string(state) {
			n++
		}
	}
	return n
}

// Review focus 4: a fixed site's heartbeat needs no position, and its
// sender's clock is not read.
func TestAFixedSiteHeartbeatsWithoutAPosition(t *testing.T) {
	r, w, clk, st := liveRig(t, nil, ingesttoken.Token{}, rowsOf(emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")))
	if ack := send(t, st, heartbeat("radar", 5)); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK || ack.GetEmplacementId() != "radar" {
		t.Fatalf("a fixed site's heartbeat: %v", ack)
	}
	waitFor(t, func() bool { return sitesIn(r, "radar", emplacement.HeartbeatOnline) > 0 })
	o := lastSite(r, "radar")
	if o.OperationalStatus != "OPERATIONAL" || o.Generic[emplacement.FieldHeartbeatIntervalSec] != "5" ||
		o.Generic[emplacement.FieldLastHeartbeatAt] != clk.now().Format(time.RFC3339Nano) ||
		o.Latitude != 50.1 || !o.SourceTimestamp.Equal(clk.now()) || o.EmplacementID != "radar" {
		t.Fatalf("site = %+v", o)
	}
	if b := w.heartbeats(); len(b) != 1 || b[0].id != "radar" || b[0].interval != 5 || !b[0].at.Equal(clk.now()) {
		t.Fatalf("heartbeat writes = %+v", b)
	}
	if c := w.touches(); len(c) != 0 {
		t.Errorf("a fixed site's heartbeat touched its row: %+v", c)
	}
	clk.advance(time.Second)
	off := heartbeat("radar", 5)
	off.GetEmplacementState().Time = timestamppb.New(clk.now().Add(-time.Hour))
	if ack := send(t, st, off); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatalf("a heartbeat stamped an hour off: %v", ack)
	}
	if at, _, ok := r.d.liveness().Last("radar"); !ok || !at.Equal(clk.now()) {
		t.Fatalf("last heartbeat = %v, want the server's time %v", at, clk.now())
	}
}

func TestABoundReportNeedsAHeartbeat(t *testing.T) {
	r, _, clk, st := liveRig(t, nil, ingesttoken.Token{}, rowsOf(emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")))
	if ack := send(t, st, claiming(observation("T-1", 50.2, -4.1), "radar")); ack.GetReason() != "no_heartbeat" || ack.GetEmplacementId() != "radar" {
		t.Fatalf("a fix before any heartbeat: %v", ack)
	}
	send(t, st, heartbeat("radar", 5))
	if ack := send(t, st, claiming(observation("T-1", 50.2, -4.1), "radar")); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatalf("a fix after a heartbeat: %v", ack)
	}
	clk.advance(15 * time.Second) // three intervals
	if ack := send(t, st, claiming(observation("T-1", 50.3, -4.1), "radar")); ack.GetReason() != "no_heartbeat" {
		t.Fatalf("a fix three intervals after the heartbeat: %v", ack)
	}
	if n := r.d.Health().Detail["reportsRejected"].(map[string]int64)["no_heartbeat"]; n != 2 {
		t.Fatalf("reportsRejected[no_heartbeat] = %d, want 2", n)
	}
}

// A silent emplacement's fix is no_heartbeat whatever else is wrong with it.
func TestNoHeartbeatComesBeforeTheOtherRefusals(t *testing.T) {
	_, _, _, st := liveRig(t, nil, ingesttoken.Token{Classification: "UNCLASSIFIED"}, rowsOf(emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")))
	secret := claiming(observation("T-2", 50.2, -4.1), "radar")
	secret.GetObservation().Classification = sentinelv1.Classification_CLASSIFICATION_SECRET
	if ack := send(t, st, secret); ack.GetReason() != "no_heartbeat" {
		t.Fatalf("a silent fix above the token: %v", ack)
	}
	polar := claiming(&ingestv1.Report{ClientRef: "p", Body: &ingestv1.Report_Observation{Observation: &sentinelv1.Observation{
		SourceEntityId: "T-3", Polar: &sentinelv1.PolarMeasurement{RangeM: -1}}}}, "radar")
	if ack := send(t, st, polar); ack.GetReason() != "no_heartbeat" {
		t.Fatalf("a silent fix with a bad polar: %v", ack)
	}
}

func TestAConnectionWithNoEmplacementsIsUnchanged(t *testing.T) {
	r, _, _, st := liveRig(t, nil, ingesttoken.Token{}, rowsOf())
	if ack := send(t, st, observation("EMITTER-1", 50.1, -4.2)); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK || ack.GetEmplacementId() != "" {
		t.Fatalf("an unbound fix: %v", ack)
	}
	if h := r.d.Health(); len(h.Emplacements) != 0 {
		t.Fatalf("Health.Emplacements = %v, want none", h.Emplacements)
	}
}

func TestAHeartbeatIsNeverRateLimited(t *testing.T) {
	cfg := map[string]any{"maxMessagesPerSec": 1, "maxMessagesPerSecPerSender": 1}
	_, _, _, st := liveRig(t, cfg, ingesttoken.Token{}, rowsOf(emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")))
	for i := 0; i < 5; i++ {
		if ack := send(t, st, heartbeat("radar", 5)); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
			t.Fatalf("heartbeat %d: %v", i, ack)
		}
	}
	if ack := send(t, st, claiming(observation("T-1", 50.2, -4.1), "radar")); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatalf("the first fix: %v", ack)
	}
	if ack := send(t, st, claiming(observation("T-2", 50.2, -4.1), "radar")); ack.GetReason() != "rate_limited" {
		t.Fatalf("the second fix in the same second: %v", ack)
	}
}

func TestTheTickerRepublishesASilentSiteAsOffline(t *testing.T) {
	r, _, clk, st := liveRig(t, nil, ingesttoken.Token{}, rowsOf(emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")))
	send(t, st, heartbeat("radar", 5))
	beatAt := clk.now()
	waitFor(t, func() bool { return sitesIn(r, "radar", emplacement.HeartbeatOnline) > 0 })
	clk.advance(16 * time.Second)
	waitFor(t, func() bool { return sitesIn(r, "radar", emplacement.HeartbeatOffline) > 0 })
	o := lastSite(r, "radar")
	if o.OperationalStatus != "INACTIVE" || o.Generic[emplacement.FieldSilentSince] != beatAt.Format(time.RFC3339Nano) || !o.SourceTimestamp.Equal(beatAt) {
		t.Fatalf("offline site = %+v", o)
	}
	// Silent, it goes out again every min(interval, 60 s), stamped with its last heartbeat.
	n := len(sitesOf(r, "radar"))
	clk.advance(5 * time.Second)
	waitFor(t, func() bool { return len(sitesOf(r, "radar")) > n })
	if o := lastSite(r, "radar"); !o.SourceTimestamp.Equal(beatAt) || o.Generic[emplacement.FieldHeartbeat] != "offline" {
		t.Fatalf("the re-publish = %+v", o)
	}
	// Its next heartbeat turns it online again.
	send(t, st, heartbeat("radar", 5))
	waitFor(t, func() bool { return lastSite(r, "radar").Generic[emplacement.FieldHeartbeat] == "online" })
}

func TestStopPublishesEverySiteOffline(t *testing.T) {
	r, _, _, st := liveRig(t, nil, ingesttoken.Token{}, rowsOf(
		emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED"), emp("mast", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")))
	send(t, st, heartbeat("radar", 5))
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"radar", "mast"} {
		if o := lastSite(r, id); o == nil || o.Generic[emplacement.FieldHeartbeat] != "offline" || o.OperationalStatus != "INACTIVE" {
			t.Fatalf("%s after Stop = %+v", id, o)
		}
	}
	if o := lastSite(r, "radar"); o.Generic[emplacement.FieldSilentSince] == "" {
		t.Error("the heard-from site has no silent-since")
	}
	if o := lastSite(r, "mast"); o.Generic[emplacement.FieldSilentSince] != "" {
		t.Error("a site never heard from has a silent-since")
	}
}

// A replica that takes the connection over trusts heartbeats its rows hold
// within three intervals; an older one reads offline, not pending.
func TestLivenessIsSeededFromRecentRows(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC() // newClock's start
	sec := 5
	recent, old := base.Add(-5*time.Second), base.Add(-20*time.Second)
	radar := emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")
	radar.LastHeartbeatAt, radar.HeartbeatIntervalSec = &recent, &sec
	mast := emp("mast", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")
	mast.LastHeartbeatAt, mast.HeartbeatIntervalSec = &old, &sec
	r, _, _, st := liveRig(t, nil, ingesttoken.Token{}, rowsOf(radar, mast))
	if ack := send(t, st, claiming(observation("T-1", 50.2, -4.1), "radar")); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatalf("a fix from the seeded site: %v", ack)
	}
	if ack := send(t, st, claiming(observation("T-2", 50.2, -4.1), "mast")); ack.GetReason() != "no_heartbeat" {
		t.Fatalf("a fix from the stale site: %v", ack)
	}
	states := map[string]emplacement.HeartbeatState{}
	for _, l := range r.d.Health().Emplacements {
		states[l.ID] = l.State
	}
	if states["radar"] != emplacement.HeartbeatOnline || states["mast"] != emplacement.HeartbeatOffline {
		t.Fatalf("states = %v", states)
	}
}

// Review focus 4: an interval out of range and a skewed mobile state are
// refused, and neither counts as a heartbeat.
func TestIntervalOutOfRangeAndSkewAreNoHeartbeat(t *testing.T) {
	ship := emp("ship", "grpc-1", "MOBILE", "RF", "UNCLASSIFIED")
	r, _, clk, st := liveRig(t, nil, ingesttoken.Token{}, rowsOf(emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED"), ship))
	if ack := send(t, st, heartbeat("radar", 301)); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_DEAD_LETTERED || ack.GetReason() != "bad_state" {
		t.Fatalf("an interval of 301 s: %v", ack)
	}
	if _, _, ok := r.d.liveness().Last("radar"); ok {
		t.Fatal("a refused heartbeat counted")
	}
	if ack := send(t, st, heartbeat("radar", 0)); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatalf("an interval of 0 (the default): %v", ack)
	}
	if _, sec, _ := r.d.liveness().Last("radar"); sec != 10 {
		t.Fatalf("the default interval = %d, want 10", sec)
	}
	if ack := send(t, st, stateReport("ship", clk.now().Add(time.Minute), 50, -4, nil)); ack.GetReason() != "clock_skew" {
		t.Fatalf("a state a minute ahead: %v", ack)
	}
	if _, _, ok := r.d.liveness().Last("ship"); ok {
		t.Fatal("a skewed state counted as a heartbeat")
	}
	if ack := send(t, st, stateReport("ship", clk.now(), 50, -4, nil)); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatalf("a good mobile state: %v", ack)
	}
	waitFor(t, func() bool { return sitesIn(r, "ship", emplacement.HeartbeatOnline) > 0 })
}

// Review focus 5: moving an emplacement to another connection drops it from
// this one at once; moved back, it starts again from no heartbeat.
func TestRebindingDropsTheEmplacementFromItsOldConnection(t *testing.T) {
	radar := emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")
	emps := rowsOf(radar)
	r, _, _, st := liveRig(t, nil, ingesttoken.Token{}, emps)
	send(t, st, heartbeat("radar", 5))
	if h := r.d.Health(); len(h.Emplacements) != 1 || h.Emplacements[0].State != emplacement.HeartbeatOnline {
		t.Fatalf("before the move: %+v", h.Emplacements)
	}
	moved := radar
	other := "grpc-2"
	moved.FeedID = &other
	emps.put(moved)
	if h := r.d.Health(); len(h.Emplacements) != 0 {
		t.Fatalf("after the move: %+v", h.Emplacements)
	}
	if _, _, ok := r.d.liveness().Last("radar"); ok {
		t.Fatal("the old connection still holds the heartbeat")
	}
	emps.put(radar)
	if ack := send(t, st, claiming(observation("T-1", 50.2, -4.1), "radar")); ack.GetReason() != "no_heartbeat" {
		t.Fatalf("a fix right after moving back: %v", ack)
	}
}

func TestTheSiteIsPublishedAtMostOncePerSecond(t *testing.T) {
	r, _, clk, st := liveRig(t, nil, ingesttoken.Token{}, rowsOf(emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")))
	send(t, st, heartbeat("radar", 5))
	waitFor(t, func() bool { return sitesIn(r, "radar", emplacement.HeartbeatOnline) == 1 })
	clk.advance(100 * time.Millisecond)
	send(t, st, heartbeat("radar", 5))
	time.Sleep(50 * time.Millisecond) // ten ticks
	if n := sitesIn(r, "radar", emplacement.HeartbeatOnline); n != 1 {
		t.Fatalf("%d online publishes within a second, want 1", n)
	}
	clk.advance(time.Second)
	send(t, st, heartbeat("radar", 5))
	if n := sitesIn(r, "radar", emplacement.HeartbeatOnline); n != 2 {
		t.Fatalf("%d online publishes a second later, want 2", n)
	}
}
