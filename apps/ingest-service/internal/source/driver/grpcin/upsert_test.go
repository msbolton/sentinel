package grpcin

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/ingesttoken"
	"github.com/sentinel/ingest-service/internal/source/driver"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

func upsertReq(id, name string) *ingestv1.UpsertEmplacementRequest {
	return &ingestv1.UpsertEmplacementRequest{Emplacement: &sentinelv1.Emplacement{
		Id: id, Name: name, Mobility: sentinelv1.Mobility_MOBILITY_MOBILE, SensorType: sentinelv1.SensorType_SENSOR_TYPE_RF,
		Classification: sentinelv1.Classification_CLASSIFICATION_UNCLASSIFIED,
	}}
}

func TestUpsertEmplacement(t *testing.T) {
	emps := &fakeEmplacements{}
	w := &fakeWriter{emps: emps}
	tok := ingesttoken.Token{ID: "t1", FeedID: "grpc-1", Classification: "CONFIDENTIAL"}
	r := startWith(t, map[string]any{"senderManagedEmplacements": true, "maxEmplacements": 2}, nil, tokensWith(secretA, tok), emps, w, nil, nil)
	c := r.client(t)
	ctx, cancel := withToken(secretA)
	defer cancel()

	created, err := c.UpsertEmplacement(ctx, upsertReq("", "Ship ESM"))
	if err != nil {
		t.Fatal(err)
	}
	got := created.GetEmplacement()
	if got.GetId() == "" || got.GetFeedId() != "grpc-1" || !got.GetActive() {
		t.Fatalf("created %v", got)
	}
	stored, _ := w.Get(context.Background(), got.GetId())
	if stored.CreatedBy != emplacement.SenderPrefix+"t1" {
		t.Errorf("createdBy %q", stored.CreatedBy)
	}
	// The connection's sessions see it.
	s, err := c.GetSession(ctx, &ingestv1.GetSessionRequest{})
	if err != nil || len(s.GetEmplacements()) != 1 {
		t.Fatalf("session after create: %v %v", s, err)
	}
	// Update by id.
	upd := upsertReq(got.GetId(), "Ship ESM")
	upd.Emplacement.Description = "aft mast"
	if res, err := c.UpsertEmplacement(ctx, upd); err != nil || res.GetEmplacement().GetDescription() != "aft mast" {
		t.Fatalf("update: %v %v", res, err)
	}
	// An unknown id creates; the cap refuses the third.
	if _, err := c.UpsertEmplacement(ctx, upsertReq("no-such-id", "Second")); err != nil {
		t.Fatalf("an unknown id should create: %v", err)
	}
	if _, err := c.UpsertEmplacement(ctx, upsertReq("", "Third")); status.Code(err) != codes.ResourceExhausted {
		t.Errorf("over maxEmplacements: %v", err)
	}
	// A name taken elsewhere.
	emps.put(emp("Taken", "", "FIXED", "RADAR", "UNCLASSIFIED"))
	w.Delete(context.Background(), got.GetId()) // make room
	if _, err := c.UpsertEmplacement(ctx, upsertReq("", "taken")); status.Code(err) != codes.AlreadyExists {
		t.Errorf("a taken name: %v", err)
	}
	// Above the token's ceiling.
	secret := upsertReq("", "Secret ESM")
	secret.Emplacement.Classification = sentinelv1.Classification_CLASSIFICATION_SECRET
	if _, err := c.UpsertEmplacement(ctx, secret); status.Code(err) != codes.InvalidArgument {
		t.Errorf("above the ceiling: %v", err)
	}
}

func TestUpsertTouchesOnlyThisConnectionsSenderRows(t *testing.T) {
	operator := emp("op-1", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")
	operator.CreatedBy = "u1"
	theirs := emp("their-1", "grpc-2", "MOBILE", "RF", "UNCLASSIFIED")
	theirs.CreatedBy = emplacement.SenderPrefix + "t9"
	emps := &fakeEmplacements{rows: []emplacement.Emplacement{operator, theirs}}
	w := &fakeWriter{emps: emps}
	r := startWith(t, map[string]any{"senderManagedEmplacements": true}, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), emps, w, nil, nil)
	ctx, cancel := withToken(secretA)
	defer cancel()
	for _, id := range []string{"op-1", "their-1"} {
		if _, err := r.client(t).UpsertEmplacement(ctx, upsertReq(id, "x")); status.Code(err) != codes.NotFound {
			t.Errorf("updating %s: %v, want NotFound", id, err)
		}
	}
}

func TestUpsertNeedsTheConnectionsConsent(t *testing.T) {
	r := startWith(t, nil, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), &fakeEmplacements{}, &fakeWriter{emps: &fakeEmplacements{}}, nil, nil)
	ctx, cancel := withToken(secretA)
	defer cancel()
	if _, err := r.client(t).UpsertEmplacement(ctx, upsertReq("", "x")); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("%v, want PermissionDenied", err)
	}
}

// senderRow is an emplacement a sender of token t1 made on grpc-1.
func senderRow(id string) emplacement.Emplacement {
	e := emp(id, "grpc-1", "MOBILE", "RF", "UNCLASSIFIED")
	e.CreatedBy = emplacement.SenderPrefix + "t1"
	return e
}

// consentRig is a connection that lets senders manage emplacements, with the
// given rows and token, and the writer over them.
func consentRig(t *testing.T, cfg map[string]any, tok ingesttoken.Token, rows ...emplacement.Emplacement) (*rig, *fakeWriter, *fakeEmplacements) {
	t.Helper()
	if cfg == nil {
		cfg = map[string]any{}
	}
	cfg["senderManagedEmplacements"] = true
	tok.ID, tok.FeedID = "t1", "grpc-1"
	emps := &fakeEmplacements{rows: rows}
	w := &fakeWriter{emps: emps}
	return startWith(t, cfg, nil, tokensWith(secretA, tok), emps, w, nil, nil), w, emps
}

// A sender's emplacement is never below the connection's own level: what it
// wrote was authored over the connection.
func TestUpsertKeepsTheConnectionsLevel(t *testing.T) {
	r, w, _ := consentRig(t, map[string]any{"classification": "CONFIDENTIAL"}, ingesttoken.Token{Classification: "CONFIDENTIAL"})
	ctx, cancel := withToken(secretA)
	defer cancel()
	c := r.client(t)
	res, err := c.UpsertEmplacement(ctx, upsertReq("", "Ship ESM"))
	if err != nil {
		t.Fatal(err)
	}
	id := res.GetEmplacement().GetId()
	if res.GetEmplacement().GetClassification() != sentinelv1.Classification_CLASSIFICATION_CONFIDENTIAL {
		t.Errorf("response level %v", res.GetEmplacement().GetClassification())
	}
	if e, _ := w.Get(context.Background(), id); e.Classification != "CONFIDENTIAL" {
		t.Errorf("created at %q, want CONFIDENTIAL", e.Classification)
	}
	// An update asking for less, or saying nothing, keeps it too.
	for _, level := range []sentinelv1.Classification{sentinelv1.Classification_CLASSIFICATION_UNCLASSIFIED, sentinelv1.Classification_CLASSIFICATION_UNSPECIFIED} {
		upd := upsertReq(id, "Ship ESM")
		upd.Emplacement.Classification = level
		if _, err := c.UpsertEmplacement(ctx, upd); err != nil {
			t.Fatal(err)
		}
		if e, _ := w.Get(context.Background(), id); e.Classification != "CONFIDENTIAL" {
			t.Errorf("updated with %v to %q, want CONFIDENTIAL", level, e.Classification)
		}
	}
}

func TestUpsertRefusesATokenBelowTheConnection(t *testing.T) {
	r, _, _ := consentRig(t, map[string]any{"classification": "CONFIDENTIAL"}, ingesttoken.Token{Classification: "UNCLASSIFIED"})
	ctx, cancel := withToken(secretA)
	defer cancel()
	if _, err := r.client(t).UpsertEmplacement(ctx, upsertReq("", "x")); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("%v, want PermissionDenied", err)
	}
}

func TestUpsertWithoutAWriterIsUnavailable(t *testing.T) {
	tok := ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}
	r := startWith(t, map[string]any{"senderManagedEmplacements": true}, nil, tokensWith(secretA, tok), &fakeEmplacements{}, nil, nil, nil)
	ctx, cancel := withToken(secretA)
	defer cancel()
	if _, err := r.client(t).UpsertEmplacement(ctx, upsertReq("", "x")); status.Code(err) != codes.Unavailable {
		t.Fatalf("%v, want Unavailable", err)
	}
}

// Whether a row is active is Sentinel's to say: an update keeps it.
func TestUpsertKeepsActive(t *testing.T) {
	off := senderRow("off-1")
	off.Active = false
	r, w, _ := consentRig(t, nil, ingesttoken.Token{}, off)
	ctx, cancel := withToken(secretA)
	defer cancel()
	req := upsertReq("off-1", "renamed")
	req.Emplacement.Active = true
	res, err := r.client(t).UpsertEmplacement(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if e, _ := w.Get(context.Background(), "off-1"); e.Active || e.Name != "renamed" || res.GetEmplacement().GetActive() {
		t.Errorf("stored %+v, response active %v: want renamed and still inactive", e, res.GetEmplacement().GetActive())
	}
}

// A row above the token's ceiling is not the token's to see, so NotFound.
func TestUpsertAboveTheCeilingIsNotFound(t *testing.T) {
	high := senderRow("high-1")
	high.Classification = "SECRET"
	r, w, _ := consentRig(t, nil, ingesttoken.Token{Classification: "CONFIDENTIAL"}, high)
	ctx, cancel := withToken(secretA)
	defer cancel()
	if _, err := r.client(t).UpsertEmplacement(ctx, upsertReq("high-1", "x")); status.Code(err) != codes.NotFound {
		t.Fatalf("%v, want NotFound", err)
	}
	if e, _ := w.Get(context.Background(), "high-1"); e.Name != "high-1" {
		t.Errorf("the row was changed: %+v", e)
	}
}

// The connection is the server's to set: a sender cannot move a row to
// another one, on create or on update.
func TestUpsertBindsToThisConnection(t *testing.T) {
	r, w, _ := consentRig(t, nil, ingesttoken.Token{}, senderRow("mine-1"))
	ctx, cancel := withToken(secretA)
	defer cancel()
	c := r.client(t)
	create := upsertReq("", "Elsewhere")
	create.Emplacement.FeedId = "grpc-2"
	res, err := c.UpsertEmplacement(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	if e, _ := w.Get(context.Background(), res.GetEmplacement().GetId()); e.FeedID == nil || *e.FeedID != "grpc-1" {
		t.Errorf("created on %v, want grpc-1", e.FeedID)
	}
	upd := upsertReq("mine-1", "mine-1")
	upd.Emplacement.FeedId = "grpc-2"
	if _, err := c.UpsertEmplacement(ctx, upd); err != nil {
		t.Fatal(err)
	}
	if e, _ := w.Get(context.Background(), "mine-1"); e.FeedID == nil || *e.FeedID != "grpc-1" {
		t.Errorf("updated onto %v, want grpc-1", e.FeedID)
	}
}

// The cap counts the senders' rows, not the operator's.
func TestUpsertCapIgnoresOperatorRows(t *testing.T) {
	operator := emp("op-1", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")
	operator.CreatedBy = "u1"
	r, _, _ := consentRig(t, map[string]any{"maxEmplacements": 1}, ingesttoken.Token{}, operator)
	ctx, cancel := withToken(secretA)
	defer cancel()
	c := r.client(t)
	if _, err := c.UpsertEmplacement(ctx, upsertReq("", "First")); err != nil {
		t.Fatalf("the operator's row counted against the cap: %v", err)
	}
	if _, err := c.UpsertEmplacement(ctx, upsertReq("", "Second")); status.Code(err) != codes.ResourceExhausted {
		t.Errorf("%v, want ResourceExhausted", err)
	}
}

func TestMaxEmplacementsConfig(t *testing.T) {
	deps := driver.Deps{Logger: zap.NewNop()}
	for _, tc := range []struct {
		cfg  map[string]any
		want int
		ok   bool
	}{
		{map[string]any{"port": 5000}, 16, true},
		{map[string]any{"port": 5000, "maxEmplacements": 0}, 16, true},
		{map[string]any{"port": 5000, "maxEmplacements": 1}, 1, true},
		{map[string]any{"port": 5000, "maxEmplacements": 256}, 256, true},
		{map[string]any{"port": 5000, "maxEmplacements": -1}, 0, false},
		{map[string]any{"port": 5000, "maxEmplacements": 257}, 0, false},
	} {
		d, err := New(spec(tc.cfg, nil), deps)
		if (err == nil) != tc.ok {
			t.Errorf("%v: err %v", tc.cfg, err)
			continue
		}
		if tc.ok && d.(*Driver).maxEmplacements != tc.want {
			t.Errorf("%v: maxEmplacements %d, want %d", tc.cfg, d.(*Driver).maxEmplacements, tc.want)
		}
	}
}

// A row rebound between the driver's read and the locked update is not
// written.
func TestUpsertRechecksUnderTheLock(t *testing.T) {
	r, w, emps := consentRig(t, nil, ingesttoken.Token{}, senderRow("mine-1"))
	w.beforeUpdate = func(id string) {
		e, _ := emps.Get(id)
		other := "grpc-2"
		e.FeedID = &other
		emps.put(e)
	}
	ctx, cancel := withToken(secretA)
	defer cancel()
	if _, err := r.client(t).UpsertEmplacement(ctx, upsertReq("mine-1", "taken over")); status.Code(err) != codes.NotFound {
		t.Fatalf("%v, want NotFound", err)
	}
	if e, _ := emps.Get("mine-1"); e.Name != "mine-1" {
		t.Errorf("the rebound row was written: %+v", e)
	}
}

// An update replaces the name, description and boresight, keeps what the
// message leaves unset, and never clears the exercise flag (see
// TestUpsertExerciseIsSticky).
func TestUpsertUpdateSemantics(t *testing.T) {
	e := senderRow("mine-1")
	e.Description, e.BoresightDeg, e.Exercise = "aft mast", 90, true
	e.Position, e.Altitude = &emplacement.Point{Lat: 50, Lon: -4}, ptr(12.0)
	e.Coverage = emplacement.Coverage{MaxRangeM: 5000, AzimuthWidthDeg: 360}
	e.DefaultSigma = emplacement.Sigma{RangeM: ptr(30.0)}
	e.AzimuthReference = emplacement.AzimuthPlatform
	e.SensorType = "RADAR"
	r, w, _ := consentRig(t, nil, ingesttoken.Token{}, e)
	ctx, cancel := withToken(secretA)
	defer cancel()
	// Only a name: the replaced fields clear (not the exercise flag), the rest is kept.
	req := &ingestv1.UpsertEmplacementRequest{Emplacement: &sentinelv1.Emplacement{Id: "mine-1", Name: "Renamed"}}
	if _, err := r.client(t).UpsertEmplacement(ctx, req); err != nil {
		t.Fatal(err)
	}
	got, _ := w.Get(context.Background(), "mine-1")
	if got.Name != "Renamed" || got.Description != "" || got.BoresightDeg != 0 || !got.Exercise {
		t.Errorf("replaced fields: %+v", got)
	}
	if got.Mobility != "MOBILE" || got.SensorType != "RADAR" || got.AzimuthReference != emplacement.AzimuthPlatform || got.Classification != "UNCLASSIFIED" {
		t.Errorf("unset enums were not kept: %+v", got)
	}
	if got.Position == nil || got.Position.Lat != 50 || got.Altitude == nil || *got.Altitude != 12 ||
		got.Coverage.MaxRangeM != 5000 || got.DefaultSigma.RangeM == nil || *got.DefaultSigma.RangeM != 30 {
		t.Errorf("absent sub-messages were not kept: %+v", got)
	}
}

// A position sent without an altitude has none: the stored one is cleared.
func TestUpsertPositionWithoutAltitudeClearsIt(t *testing.T) {
	e := senderRow("mine-1")
	e.Position, e.Altitude = &emplacement.Point{Lat: 50, Lon: -4}, ptr(12.0)
	r, w, _ := consentRig(t, nil, ingesttoken.Token{}, e)
	ctx, cancel := withToken(secretA)
	defer cancel()
	c := r.client(t)
	req := upsertReq("mine-1", "mine-1")
	req.Emplacement.Position = &sentinelv1.Coordinate{Latitude: 51, Longitude: -5}
	if _, err := c.UpsertEmplacement(ctx, req); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.Get(context.Background(), "mine-1"); got.Position.Lat != 51 || got.Altitude != nil {
		t.Errorf("position %+v altitude %v, want 51 and none", got.Position, got.Altitude)
	}
	req.Emplacement.Position.AltitudeMeters = ptr(7.0)
	if _, err := c.UpsertEmplacement(ctx, req); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.Get(context.Background(), "mine-1"); got.Altitude == nil || *got.Altitude != 7 {
		t.Errorf("altitude %v, want 7", got.Altitude)
	}
}

// The mounting height travels both ways: an upsert stores it and
// ListEmplacements reads it back; an upsert that leaves it unset keeps it.
func TestUpsertCarriesHeightAboveGround(t *testing.T) {
	emps := &fakeEmplacements{}
	w := &fakeWriter{emps: emps}
	tok := ingesttoken.Token{ID: "t1", FeedID: "grpc-1", Classification: "CONFIDENTIAL"}
	r := startWith(t, map[string]any{"senderManagedEmplacements": true, "maxEmplacements": 2}, nil, tokensWith(secretA, tok), emps, w, nil, nil)
	c := r.client(t)
	ctx, cancel := withToken(secretA)
	defer cancel()
	heightOf := func() *float64 {
		t.Helper()
		l, err := c.ListEmplacements(ctx, &ingestv1.ListEmplacementsRequest{})
		if err != nil || len(l.GetEmplacements()) != 1 {
			t.Fatalf("ListEmplacements = %v, %v", l, err)
		}
		return l.GetEmplacements()[0].HeightAboveGroundM
	}

	req := upsertReq("", "Mast camera")
	req.Emplacement.HeightAboveGroundM = ptr(12.0)
	res, err := c.UpsertEmplacement(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if h := heightOf(); h == nil || *h != 12 {
		t.Fatalf("height after create = %v, want 12", h)
	}
	id := res.GetEmplacement().GetId()
	if stored, _ := w.Get(context.Background(), id); stored.HeightAboveGroundM == nil || *stored.HeightAboveGroundM != 12 {
		t.Errorf("stored height = %v, want 12", stored.HeightAboveGroundM)
	}
	// Unset on a later upsert: kept.
	if _, err := c.UpsertEmplacement(ctx, upsertReq(id, "Mast camera")); err != nil {
		t.Fatal(err)
	}
	if h := heightOf(); h == nil || *h != 12 {
		t.Errorf("height after an upsert without one = %v, want 12", h)
	}
	// Set again: replaced.
	req = upsertReq(id, "Mast camera")
	req.Emplacement.HeightAboveGroundM = ptr(20.0)
	if _, err := c.UpsertEmplacement(ctx, req); err != nil {
		t.Fatal(err)
	}
	if h := heightOf(); h == nil || *h != 20 {
		t.Errorf("height after an upsert with 20 = %v, want 20", h)
	}
	// Out of range: refused.
	req.Emplacement.HeightAboveGroundM = ptr(-1.0)
	if _, err := c.UpsertEmplacement(ctx, req); status.Code(err) != codes.InvalidArgument {
		t.Errorf("a negative height: %v, want InvalidArgument", err)
	}
}
