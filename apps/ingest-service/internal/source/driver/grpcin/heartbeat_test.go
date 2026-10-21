package grpcin

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/ingesttoken"
	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/driver"
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
// within three intervals; an older one reads pending through the startup
// grace (its sender may not have reconnected yet) and offline after it.
// Pending is not online: the stale site's fixes are still refused.
func TestLivenessIsSeededFromRecentRows(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC() // newClock's start
	sec := 5
	recent, old := base.Add(-5*time.Second), base.Add(-20*time.Second)
	radar := emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")
	radar.LastHeartbeatAt, radar.HeartbeatIntervalSec = &recent, &sec
	mast := emp("mast", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")
	mast.LastHeartbeatAt, mast.HeartbeatIntervalSec = &old, &sec
	r, _, clk, st := liveRig(t, nil, ingesttoken.Token{}, rowsOf(radar, mast))
	if ack := send(t, st, claiming(observation("T-1", 50.2, -4.1), "radar")); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatalf("a fix from the seeded site: %v", ack)
	}
	if ack := send(t, st, claiming(observation("T-2", 50.2, -4.1), "mast")); ack.GetReason() != "no_heartbeat" {
		t.Fatalf("a fix from the stale site: %v", ack)
	}
	states := func() map[string]emplacement.HeartbeatState {
		out := map[string]emplacement.HeartbeatState{}
		for _, l := range r.d.Health().Emplacements {
			out[l.ID] = l.State
		}
		return out
	}
	if s := states(); s["radar"] != emplacement.HeartbeatOnline || s["mast"] != emplacement.HeartbeatPending {
		t.Fatalf("states at the start = %v", s)
	}
	clk.advance(30 * time.Second) // past the startup grace
	if s := states(); s["radar"] != emplacement.HeartbeatOffline || s["mast"] != emplacement.HeartbeatOffline {
		t.Fatalf("states after the grace = %v", s)
	}
	if ack := send(t, st, claiming(observation("T-3", 50.2, -4.1), "mast")); ack.GetReason() != "no_heartbeat" {
		t.Fatalf("a fix from the stale site after the grace: %v", ack)
	}
}

// I1: after a restart, a silent site's re-publishes are stamped no earlier
// than the new run's start, so they are newer than anything the previous run
// published (its last in-memory beat, its offline publish at Stop) and
// entity-service applies them. Its silent-since stays the row's heartbeat.
func TestASeededSiteIsRepublishedNoEarlierThanTheRunStart(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC() // newClock's start, the run's start
	sec := 5
	old := base.Add(-20 * time.Second)
	radar := emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")
	radar.LastHeartbeatAt, radar.HeartbeatIntervalSec = &old, &sec
	r, _, clk, _ := liveRig(t, nil, ingesttoken.Token{}, rowsOf(radar))
	if !r.d.liveness().Started().Equal(base) {
		t.Fatalf("the run started at %v, want %v", r.d.liveness().Started(), base)
	}
	waitFor(t, func() bool { return sitesIn(r, "radar", emplacement.HeartbeatPending) > 0 })
	o := lastSite(r, "radar")
	if o.SourceTimestamp.Before(base) || o.Generic[emplacement.FieldLastHeartbeatAt] != old.Format(time.RFC3339Nano) {
		t.Fatalf("the pending re-publish = stamp %v lastHeartbeatAt %q; want a stamp >= %v and the row's %v",
			o.SourceTimestamp, o.Generic[emplacement.FieldLastHeartbeatAt], base, old)
	}
	clk.advance(30 * time.Second)
	waitFor(t, func() bool { return sitesIn(r, "radar", emplacement.HeartbeatOffline) > 0 })
	if o := lastSite(r, "radar"); o.SourceTimestamp.Before(base) || o.Generic[emplacement.FieldSilentSince] != old.Format(time.RFC3339Nano) {
		t.Fatalf("the offline re-publish = stamp %v silentSince %q; want a stamp >= %v and silent since %v",
			o.SourceTimestamp, o.Generic[emplacement.FieldSilentSince], base, old)
	}
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
	if o := lastSite(r, "radar"); o.Generic[emplacement.FieldHeartbeat] != "offline" || o.SourceTimestamp.Before(base) {
		t.Fatalf("the stop publish = %+v", o)
	}
}

// When serving fails, the run publishes every site offline, as at Stop.
func TestAServingErrorPublishesEverySiteOffline(t *testing.T) {
	r, _, _, st := liveRig(t, nil, ingesttoken.Token{}, rowsOf(emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")))
	send(t, st, heartbeat("radar", 5))
	waitFor(t, func() bool { return sitesIn(r, "radar", emplacement.HeartbeatOnline) > 0 })
	r.d.gl.Load().Close() // the listener fails under grpc's Serve
	select {
	case err := <-r.done:
		r.done <- err
		if err == nil {
			t.Fatal("Run returned nil after the listener failed")
		}
	case <-time.After(stopGrace + 2*time.Second):
		t.Fatal("Run did not return after the listener failed")
	}
	if o := lastSite(r, "radar"); o.Generic[emplacement.FieldHeartbeat] != "offline" || o.OperationalStatus != "INACTIVE" {
		t.Fatalf("radar after a serving error = %+v", o)
	}
}

// grpc heartbeats its sites itself, so the supervisor runs no SitePublisher
// for it; a silent site is re-published within the RADAR stale default.
func TestGrpcIsAHeartbeatDriver(t *testing.T) {
	def, ok := driver.Lookup(ID)
	if !ok || !def.Heartbeats {
		t.Fatalf("driver.Lookup(%q) = Heartbeats %v, %v; want true", ID, def.Heartbeats, ok)
	}
	if maxRepublish >= 30*time.Second {
		t.Fatalf("maxRepublish = %v, want below the 30 s RADAR stale default", maxRepublish)
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

// Only a state taken as a heartbeat is exempt from the rate limits: refused
// ones are charged, so malformed states cannot flood the dead letters.
func TestRefusedStatesAreRateLimited(t *testing.T) {
	cfg := map[string]any{"maxMessagesPerSec": 1, "maxMessagesPerSecPerSender": 1}
	r, _, _, st := liveRig(t, cfg, ingesttoken.Token{}, rowsOf(emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")))
	if ack := send(t, st, heartbeat("radar", 301)); ack.GetReason() != "bad_state" {
		t.Fatalf("the first malformed state: %v", ack)
	}
	mismatch := heartbeat("radar", 5)
	mismatch.GetEmplacementState().EmplacementId = "mast"
	for i, bad := range []*ingestv1.Report{heartbeat("radar", 301), mismatch, heartbeat("nowhere", 5)} {
		if ack := send(t, st, bad); ack.GetReason() != "rate_limited" {
			t.Fatalf("refused state %d past the limit: %v", i, ack)
		}
	}
	if n := len(r.sink.deadLetters()); n != 1 {
		t.Fatalf("%d dead letters, want 1", n)
	}
	if ack := send(t, st, heartbeat("radar", 5)); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatalf("a good heartbeat past the limit: %v", ack)
	}
}

// The row's heartbeat is written at most every min(interval, touchEvery), and
// a changed interval at most once a second, whatever the report touches do.
func TestHeartbeatWritesAreThrottled(t *testing.T) {
	_, w, clk, st := liveRig(t, nil, ingesttoken.Token{}, rowsOf(emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")))
	writes := func(want int) {
		t.Helper()
		if b := w.heartbeats(); len(b) != want {
			t.Fatalf("%d heartbeat writes, want %d: %+v", len(b), want, b)
		}
	}
	send(t, st, heartbeat("radar", 5))
	writes(1)
	clk.advance(time.Second)
	send(t, st, heartbeat("radar", 5))
	writes(1)
	if ack := send(t, st, claiming(observation("T-1", 50.2, -4.1), "radar")); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatalf("a fix: %v", ack)
	}
	if len(w.touches()) == 0 {
		t.Fatal("the fix touched nothing")
	}
	writes(1)
	clk.advance(5 * time.Second)
	send(t, st, heartbeat("radar", 5))
	writes(2)
	clk.advance(500 * time.Millisecond)
	send(t, st, heartbeat("radar", 6))
	send(t, st, heartbeat("radar", 5))
	send(t, st, heartbeat("radar", 6))
	writes(2)
	clk.advance(time.Second)
	send(t, st, heartbeat("radar", 6))
	writes(3)
	if b := w.heartbeats(); b[2].interval != 6 {
		t.Fatalf("the changed interval written = %d, want 6", b[2].interval)
	}
}

// A mobile site is stamped on its sender's fix clock by every publish, so a
// re-publish never outdates the sender's next move.
func TestAMobileSiteIsStampedOnItsFixClock(t *testing.T) {
	r, _, clk, st := liveRig(t, nil, ingesttoken.Token{}, rowsOf(emp("ship", "grpc-1", "MOBILE", "RF", "UNCLASSIFIED")))
	started := clk.now()
	// A fix behind the run's start is stamped with the start.
	early := stateReport("ship", started.Add(-30*time.Second), 50, -4, nil)
	early.GetEmplacementState().HeartbeatIntervalSec = 5
	if ack := send(t, st, early); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatalf("a state 30 s behind at the start: %v", ack)
	}
	waitFor(t, func() bool { return sitesIn(r, "ship", emplacement.HeartbeatOnline) > 0 })
	if o := lastSite(r, "ship"); !o.SourceTimestamp.Equal(started) {
		t.Fatalf("a fix before the run's start is stamped %v, want the start %v", o.SourceTimestamp, started)
	}
	clk.advance(time.Minute) // the lagging fix clock is now past the start
	behind := func() *ingestv1.Report {
		rep := stateReport("ship", clk.now().Add(-30*time.Second), 50, -4, nil)
		rep.GetEmplacementState().HeartbeatIntervalSec = 5
		return rep
	}
	fixAt := clk.now().Add(-30 * time.Second)
	if ack := send(t, st, behind()); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatalf("a state 30 s behind: %v", ack)
	}
	waitFor(t, func() bool {
		o := lastSite(r, "ship")
		return o.Generic[emplacement.FieldHeartbeat] == "online" && o.SourceTimestamp.Equal(fixAt)
	})
	offBefore := sitesIn(r, "ship", emplacement.HeartbeatOffline)
	clk.advance(16 * time.Second)
	waitFor(t, func() bool { return sitesIn(r, "ship", emplacement.HeartbeatOffline) > offBefore })
	off := lastSite(r, "ship")
	if off.SourceTimestamp.After(fixAt) {
		t.Fatalf("offline stamp %v is past the newest fix %v", off.SourceTimestamp, fixAt)
	}
	clk.advance(time.Second)
	fixAt = clk.now().Add(-30 * time.Second)
	send(t, st, behind())
	waitFor(t, func() bool { return lastSite(r, "ship").Generic[emplacement.FieldHeartbeat] == "online" })
	if o := lastSite(r, "ship"); !o.SourceTimestamp.Equal(fixAt) || o.SourceTimestamp.Before(off.SourceTimestamp) {
		t.Fatalf("the next online stamp = %v, want %v and not before %v", o.SourceTimestamp, fixAt, off.SourceTimestamp)
	}
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
	if o := lastSite(r, "ship"); o.Generic[emplacement.FieldHeartbeat] != "offline" || o.SourceTimestamp.After(fixAt) {
		t.Fatalf("the stop publish = %+v, want offline stamped no later than %v", o, fixAt)
	}
}

// A sender that does not stream heartbeats through Publish.
func TestAHeartbeatThroughPublish(t *testing.T) {
	r, _, _, _ := liveRig(t, nil, ingesttoken.Token{}, rowsOf(emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")))
	ctx, cancel := withToken(secretA)
	defer cancel()
	resp, err := r.client(t).Publish(ctx, &ingestv1.PublishRequest{Reports: []*ingestv1.Report{
		heartbeat("radar", 5), claiming(observation("T-1", 50.2, -4.1), "radar")}})
	if err != nil {
		t.Fatal(err)
	}
	for i, ack := range resp.GetAcks() {
		if ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK || ack.GetEmplacementId() != "radar" {
			t.Fatalf("ack %d: %v", i, ack)
		}
	}
	if n := len(resp.GetAcks()); n != 2 {
		t.Fatalf("%d acks, want 2", n)
	}
}
