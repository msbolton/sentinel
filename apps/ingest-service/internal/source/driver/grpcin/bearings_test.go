package grpcin

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/ingesttoken"
	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/driver"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

// bearingDriver is a driver made by New, on a hand-moved clock, for calling
// the bearing store directly.
func bearingDriver(t *testing.T, cfg map[string]any) (*Driver, *testClock) {
	t.Helper()
	if cfg == nil {
		cfg = map[string]any{}
	}
	cfg["port"] = 20000
	dv, err := New(spec(cfg, nil), driver.Deps{Logger: zap.NewNop()})
	if err != nil {
		t.Fatal(err)
	}
	d := dv.(*Driver)
	clk := newClock()
	clk.tweak(d)
	return d, clk
}

// bearingsOf decodes a site's bearings field; nil when it has none.
func bearingsOf(t *testing.T, o *obs.Observation) []bearing {
	t.Helper()
	raw, ok := o.Generic[emplacement.FieldBearings]
	if !ok {
		return nil
	}
	var out []bearing
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("bearings %q: %v", raw, err)
	}
	return out
}

// airBearing is a direction-only drone detection claiming the camera, which
// becomes an air_target bearing; track is the camera's track id ("" for none).
func airBearing(ref, track string, az float64) *ingestv1.Report {
	rep := claiming(dirReport(ref, az, ptr(5.0)), "cam")
	m := rep.GetObservation()
	m.EntityType = sentinelv1.EntityType_ENTITY_TYPE_DRONE
	if track != "" {
		m.Platform = &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Eoir{Eoir: &sentinelv1.EoIrDetail{CameraTrackId: track}}}
	}
	return rep
}

// cameraRig is a live rig over the one fixed camera at plateau+12 m.
func cameraRig(t *testing.T, cfg map[string]any) (*rig, *testClock, ingestv1.IngestService_StreamReportsClient) {
	t.Helper()
	r, _, clk, st := liveRig(t, cfg, ingesttoken.Token{}, rowsOf(camera(ptr(plateauM+12))))
	return r, clk, st
}

// sitesWithBearings counts the camera's site publishes carrying n bearings.
func sitesWithBearings(t *testing.T, r *rig, n int) int {
	t.Helper()
	c := 0
	for _, o := range sitesOf(r, "cam") {
		if len(bearingsOf(t, o)) == n {
			c++
		}
	}
	return c
}

func mustOK(t *testing.T, ack *ingestv1.Ack) {
	t.Helper()
	if ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatalf("ack %v, want OK", ack)
	}
}

func TestBearingReplacesSameTrack(t *testing.T) {
	d, clk := bearingDriver(t, nil)
	now := clk.now()
	d.addBearing("cam", bearing{TrackID: "c-1", AzimuthDeg: 10, Reason: reasonAirTarget, ObservedAt: now})
	d.addBearing("cam", bearing{TrackID: "c-1", AzimuthDeg: 20, Reason: reasonAirTarget, ObservedAt: now.Add(time.Millisecond)})
	got := d.activeBearings("cam", now)
	if len(got) != 1 || got[0].AzimuthDeg != 20 {
		t.Fatalf("bearings %+v, want only the track's newest (azimuth 20)", got)
	}
	// Another track, and two without a track id, are each their own line.
	d.addBearing("cam", bearing{TrackID: "c-2", AzimuthDeg: 30, ObservedAt: now})
	d.addBearing("cam", bearing{AzimuthDeg: 40, ObservedAt: now})
	d.addBearing("cam", bearing{AzimuthDeg: 50, ObservedAt: now})
	if got := d.activeBearings("cam", now); len(got) != 4 {
		t.Fatalf("bearings %+v, want 4", got)
	}
	// A late, older report of a track does not undo its newer one.
	d.addBearing("cam", bearing{TrackID: "c-1", AzimuthDeg: 5, ObservedAt: now.Add(-time.Second)})
	for _, b := range d.activeBearings("cam", now) {
		if b.TrackID == "c-1" && b.AzimuthDeg != 20 {
			t.Errorf("track c-1 azimuth %v, want the newer 20 kept", b.AzimuthDeg)
		}
	}
	// Other sites are untouched.
	if got := d.activeBearings("other", now); len(got) != 0 {
		t.Errorf("other site bearings %+v", got)
	}
}

// Review focus 5: a site carries at most 50 bearings, the newest.
func TestBearingsCappedNewestFirst(t *testing.T) {
	d, clk := bearingDriver(t, nil)
	now := clk.now()
	// Added out of order, so the newest are not simply the last added.
	for _, i := range perm(120, 7) {
		d.addBearing("cam", bearing{TrackID: fmt.Sprintf("t-%03d", i), AzimuthDeg: float64(i), Reason: reasonAirTarget,
			ObservedAt: now.Add(time.Duration(i-120) * 10 * time.Millisecond)})
	}
	o, ok := d.siteFor(camera(ptr(plateauM+12)), emplacement.HeartbeatOnline, now)
	if !ok {
		t.Fatal("no site")
	}
	got := bearingsOf(t, o)
	if len(got) != maxBearingsPerSite {
		t.Fatalf("%d bearings, want %d", len(got), maxBearingsPerSite)
	}
	for k, b := range got {
		if want := fmt.Sprintf("t-%03d", 119-k); b.TrackID != want {
			t.Fatalf("bearing %d is %s, want %s (newest first)", k, b.TrackID, want)
		}
	}
	if o.Generic[emplacement.FieldBearingTTLSec] != "10" {
		t.Errorf("ttl field %q, want 10", o.Generic[emplacement.FieldBearingTTLSec])
	}
}

// perm is 0..n-1 in a fixed shuffled order.
func perm(n, step int) []int {
	out := make([]int, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, (i*step+3)%n)
	}
	return out
}

func TestBearingsExpireWithoutNewDetection(t *testing.T) {
	r, clk, st := cameraRig(t, nil)
	mustOK(t, send(t, st, heartbeat("cam", 60)))
	mustOK(t, send(t, st, airBearing("d1", "c-1", 90)))
	clk.advance(time.Second)
	waitFor(t, func() bool { return sitesWithBearings(t, r, 1) == 1 })
	n := len(sitesOf(r, "cam"))
	clk.advance(11 * time.Second) // ttl + 1 s; still online, well inside the 25 s re-publish
	waitFor(t, func() bool { return len(sitesOf(r, "cam")) > n })
	o := lastSite(r, "cam")
	if _, ok := o.Generic[emplacement.FieldBearings]; ok {
		t.Fatalf("expired bearings still published: %v", o.Generic)
	}
	if _, ok := o.Generic[emplacement.FieldBearingTTLSec]; ok {
		t.Errorf("ttl published with no bearings: %v", o.Generic)
	}
	if o.Generic[emplacement.FieldHeartbeat] != string(emplacement.HeartbeatOnline) {
		t.Errorf("heartbeat %q, want online", o.Generic[emplacement.FieldHeartbeat])
	}
	// Nothing more goes out until something changes.
	n = len(sitesOf(r, "cam"))
	time.Sleep(50 * time.Millisecond)
	if m := len(sitesOf(r, "cam")); m != n {
		t.Errorf("%d more publishes with nothing changed", m-n)
	}
}

func TestBearingPublishRateLimited(t *testing.T) {
	r, clk, st := cameraRig(t, nil)
	mustOK(t, send(t, st, heartbeat("cam", 60)))
	waitFor(t, func() bool { return sitesIn(r, "cam", emplacement.HeartbeatOnline) == 1 })
	time.Sleep(20 * time.Millisecond) // let a tick that raced the heartbeat settle
	n := len(sitesOf(r, "cam"))
	for i := 0; i < 10; i++ {
		mustOK(t, send(t, st, airBearing(fmt.Sprintf("d%d", i), fmt.Sprintf("c-%d", i), float64(10*i))))
		clk.advance(10 * time.Millisecond)
	}
	// Under a second since the heartbeat's publish: many ticks, no publish.
	time.Sleep(50 * time.Millisecond)
	if m := len(sitesOf(r, "cam")); m != n {
		t.Fatalf("%d site publishes within a second of the last, want none", m-n)
	}
	clk.advance(time.Second)
	waitFor(t, func() bool { return len(sitesOf(r, "cam")) == n+1 })
	time.Sleep(50 * time.Millisecond)
	if m := len(sitesOf(r, "cam")); m != n+1 {
		t.Fatalf("%d site publishes, want one for all ten bearings", m-n)
	}
	if got := bearingsOf(t, lastSite(r, "cam")); len(got) != 10 {
		t.Fatalf("%d bearings published, want 10", len(got))
	}
}

func TestBearingsClearedWhenSiteOffline(t *testing.T) {
	r, clk, st := cameraRig(t, map[string]any{"bearingTtlSec": 300})
	mustOK(t, send(t, st, heartbeat("cam", 5)))
	mustOK(t, send(t, st, airBearing("d1", "c-1", 90)))
	clk.advance(time.Second)
	waitFor(t, func() bool { return sitesWithBearings(t, r, 1) == 1 })
	clk.advance(16 * time.Second) // three intervals silent
	waitFor(t, func() bool { return sitesIn(r, "cam", emplacement.HeartbeatOffline) > 0 })
	if o := lastSite(r, "cam"); len(bearingsOf(t, o)) != 0 {
		t.Fatalf("offline site carries bearings: %v", o.Generic)
	}
	if got := r.d.activeBearings("cam", clk.now()); len(got) != 0 {
		t.Fatalf("bearings kept for an offline site: %+v", got)
	}

	// The offline publish at Stop clears them too.
	r, clk, st = cameraRig(t, map[string]any{"bearingTtlSec": 300})
	mustOK(t, send(t, st, heartbeat("cam", 5)))
	mustOK(t, send(t, st, airBearing("d1", "c-1", 90)))
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
	o := lastSite(r, "cam")
	if o.Generic[emplacement.FieldHeartbeat] != string(emplacement.HeartbeatOffline) || len(bearingsOf(t, o)) != 0 {
		t.Fatalf("site after Stop: %v", o.Generic)
	}
	if got := r.d.activeBearings("cam", clk.now()); len(got) != 0 {
		t.Fatalf("bearings kept after Stop: %+v", got)
	}
}

func TestSiteFieldsKeepHeartbeatAndBearings(t *testing.T) {
	// A fixed camera, published by the ticker.
	r, clk, st := cameraRig(t, nil)
	mustOK(t, send(t, st, heartbeat("cam", 60)))
	mustOK(t, send(t, st, airBearing("d1", "c-1", 90)))
	clk.advance(time.Second)
	waitFor(t, func() bool { return sitesWithBearings(t, r, 1) == 1 })
	o := lastSite(r, "cam")
	if o.Generic[emplacement.FieldHeartbeat] != string(emplacement.HeartbeatOnline) || o.Generic[emplacement.FieldHeartbeatIntervalSec] != "60" ||
		o.Generic[emplacement.FieldBearingTTLSec] != "10" {
		t.Fatalf("fixed site fields %v", o.Generic)
	}
	if b := bearingsOf(t, o); b[0].TrackID != "c-1" || b[0].Reason != reasonAirTarget || b[0].AzimuthDeg != 90 || b[0].LengthM != 30000 {
		t.Errorf("bearing %+v", b[0])
	}

	// A mobile camera, published by its own state reports (the ticker held off).
	ship := emp("cam", "grpc-1", "MOBILE", "EO_IR", "UNCLASSIFIED")
	clk2 := newClock()
	emps := rowsOf(ship)
	r2 := startWith(t, nil, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), emps, &fakeWriter{emps: emps}, nil, func(d *Driver) {
		clk2.tweak(d)
		d.heartbeatsOff = false
		d.republishTick = time.Hour
	})
	st2 := openStream(t, r2, secretA)
	state := func() *ingestv1.Report {
		return &ingestv1.Report{ClientRef: "s", EmplacementId: "cam", Body: &ingestv1.Report_EmplacementState{EmplacementState: &sentinelv1.EmplacementState{
			HeartbeatIntervalSec: 5, Position: &sentinelv1.Coordinate{Latitude: site.Lat, Longitude: site.Lon, AltitudeMeters: ptr(plateauM + 12)}}}}
	}
	mustOK(t, send(t, st2, state()))
	mustOK(t, send(t, st2, airBearing("d1", "c-9", 45)))
	clk2.advance(time.Second)
	mustOK(t, send(t, st2, state()))
	o = lastSite(r2, "cam")
	if o == nil || o.Generic[emplacement.FieldHeartbeat] != string(emplacement.HeartbeatOnline) {
		t.Fatalf("mobile site %+v", o)
	}
	if b := bearingsOf(t, o); len(b) != 1 || b[0].TrackID != "c-9" {
		t.Fatalf("mobile site bearings %+v (fields %v)", b, o.Generic)
	}
}

func TestBearingTtlConfigRange(t *testing.T) {
	for _, bad := range []int{0, 301, -1} {
		if _, err := New(spec(map[string]any{"port": 20000, "bearingTtlSec": bad}, nil), driver.Deps{Logger: zap.NewNop()}); err == nil {
			t.Errorf("bearingTtlSec %d accepted", bad)
		}
	}
	for _, good := range []int{1, 300} {
		d, _ := bearingDriver(t, map[string]any{"bearingTtlSec": good})
		if d.bearingTTL != time.Duration(good)*time.Second {
			t.Errorf("bearingTtlSec %d: ttl %v", good, d.bearingTTL)
		}
	}
	if d, _ := bearingDriver(t, nil); d.bearingTTL != 10*time.Second {
		t.Errorf("default ttl %v, want 10 s", d.bearingTTL)
	}
	// The ttl is what expires them.
	d, clk := bearingDriver(t, map[string]any{"bearingTtlSec": 2})
	d.addBearing("cam", bearing{TrackID: "c-1", ObservedAt: clk.now()})
	if got := d.activeBearings("cam", clk.now().Add(2*time.Second)); len(got) != 1 {
		t.Errorf("at the ttl: %d bearings, want 1", len(got))
	}
	if got := d.activeBearings("cam", clk.now().Add(2*time.Second+time.Millisecond)); len(got) != 0 {
		t.Errorf("past the ttl: %d bearings, want 0", len(got))
	}
}

func TestHealthBearingsByReason(t *testing.T) {
	r, st := terrainRig(t, nil, camera(ptr(plateauM+12)), plateau(), nil)
	if got, ok := r.d.Health().Detail["bearings"].(map[string]int64); !ok || len(got) != 0 {
		t.Fatalf("bearings before any = %#v", r.d.Health().Detail["bearings"])
	}
	mustOK(t, send(t, st, airBearing("d1", "c-1", 90)))
	mustOK(t, send(t, st, airBearing("d2", "c-1", 91)))
	mustOK(t, send(t, st, claiming(dirReport("up", 90, ptr(10.0)), "cam"))) // a ground target looking up
	got := r.d.Health().Detail["bearings"].(map[string]int64)
	if len(got) != 2 || got[reasonAirTarget] != 2 || got[reasonNoGround] != 1 {
		t.Fatalf("bearings = %v, want air_target 2 and no_ground_intersection 1", got)
	}
	if n := r.d.Health().Detail["reportsOk"]; n != int64(3) {
		t.Errorf("reportsOk %v, want 3", n)
	}
}

// C1: a bearing from a report marked above its site's level never reaches
// the site, which is published at the site's own level; it is counted.
func TestBearingAboveSiteClassificationIsWithheld(t *testing.T) {
	r, _, clk, st := liveRig(t, nil, ingesttoken.Token{Classification: "SECRET"}, rowsOf(camera(ptr(plateauM+12))))
	mustOK(t, send(t, st, heartbeat("cam", 60)))
	secret := airBearing("d1", "c-secret", 90)
	secret.GetObservation().Classification = sentinelv1.Classification_CLASSIFICATION_SECRET
	mustOK(t, send(t, st, secret))
	mustOK(t, send(t, st, airBearing("d2", "c-open", 45)))
	clk.advance(time.Second)
	waitFor(t, func() bool { return sitesWithBearings(t, r, 1)+sitesWithBearings(t, r, 2) >= 1 })
	for _, o := range sitesOf(r, "cam") {
		for _, b := range bearingsOf(t, o) {
			if b.TrackID == "c-secret" {
				t.Fatalf("site published at %q carries the SECRET report's bearing %+v", o.Classification, b)
			}
		}
	}
	if got := r.d.activeBearings("cam", clk.now()); len(got) != 1 || got[0].TrackID != "c-open" {
		t.Errorf("held bearings %+v, want only c-open", got)
	}
	got := r.d.Health().Detail["bearings"].(map[string]int64)
	if got[reasonAboveSiteClassification] != 1 || got[reasonAirTarget] != 1 {
		t.Errorf("bearings = %v, want above_site_classification 1 and air_target 1", got)
	}
	if n := len(r.sink.emitted()) - len(sitesOf(r, "cam")); n != 0 {
		t.Errorf("%d observations emitted for bearings", n)
	}
}

// I3: a bearing lives its ttl from when the server took it, so a camera
// whose clock (or video pipeline) runs behind does not lose its lines.
func TestBearingExpiryRunsFromReceipt(t *testing.T) {
	d, clk := bearingDriver(t, nil)
	now := clk.now()
	d.addBearing("cam", bearing{TrackID: "late", ObservedAt: now.Add(-30 * time.Second), ReceivedAt: now})
	if got := d.activeBearings("cam", now.Add(9*time.Second)); len(got) != 1 {
		t.Fatalf("9 s after receipt: %d bearings, want 1 (observed 30 s before it)", len(got))
	}
	if got := d.activeBearings("cam", now.Add(10*time.Second+time.Millisecond)); len(got) != 0 {
		t.Errorf("past the ttl from receipt: %d bearings, want 0", len(got))
	}
	// A bearing made from a detection carries its receipt time on the wire.
	o := dirObs(90, 5)
	o.SourceTimestamp = o.ReceivedAt.Add(-20 * time.Second)
	b := newBearing(o, 90, 5, 0.5, 0.5, 1000, reasonAirTarget)
	if !b.ReceivedAt.Equal(o.ReceivedAt) || !b.ObservedAt.Equal(o.SourceTimestamp) {
		t.Errorf("bearing received %v observed %v, want %v and %v", b.ReceivedAt, b.ObservedAt, o.ReceivedAt, o.SourceTimestamp)
	}
	raw, _ := json.Marshal(b)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if _, ok := m["receivedAt"]; !ok {
		t.Errorf("bearing JSON %s has no receivedAt", raw)
	}
}

// I3, through a report: a detection stamped 30 s late still draws a line.
func TestLateStampedBearingIsPublished(t *testing.T) {
	r, clk, st := cameraRig(t, nil)
	mustOK(t, send(t, st, heartbeat("cam", 60)))
	rep := airBearing("d1", "c-late", 90)
	rep.GetObservation().SourceTimestamp = timestamppb.New(clk.now().Add(-30 * time.Second))
	mustOK(t, send(t, st, rep))
	clk.advance(time.Second)
	waitFor(t, func() bool { return sitesWithBearings(t, r, 1) == 1 })
}
