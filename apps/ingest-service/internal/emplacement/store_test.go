package emplacement

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests need a database with db/schema applied (72_emplacements.sql
// included). Set TEST_DATABASE_URL to run them; they create emplacements
// named test-* and a feed test-emp-feed and delete them before and after.
func testPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	clean := func(ctx context.Context) {
		_, _ = pool.Exec(ctx, `DELETE FROM sentinel.emplacements WHERE name ILIKE 'test-%'`)
		_, _ = pool.Exec(ctx, `DELETE FROM sentinel.feeds WHERE id = 'test-emp-feed'`)
	}
	clean(ctx)
	t.Cleanup(func() { clean(context.Background()) })
	return pool, ctx
}

// fixed is an inactive fixed emplacement with no connection: an active one
// needs a connection (emplacements_active_needs_feed).
func fixed(name string) Emplacement {
	e := New()
	e.Name, e.Mobility, e.SensorType = name, MobilityFixed, "RADAR"
	e.Position = &Point{Lat: 50.1, Lon: -4.2}
	e.Coverage = Coverage{MaxRangeM: 60000, AzimuthWidthDeg: 120}
	e.CreatedBy = "u1"
	e.Active = false
	return e
}

// listen returns the payloads of the sentinel_emplacements notifications.
func listen(t *testing.T, ctx context.Context, pool *pgxpool.Pool) <-chan string {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw := conn.Hijack()
	lctx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	// Stop the listener and wait for it to leave the connection before closing it.
	t.Cleanup(func() {
		stop()
		<-done
		_ = raw.Close(context.Background())
	})
	if _, err := raw.Exec(ctx, "LISTEN "+Channel); err != nil {
		t.Fatal(err)
	}
	out := make(chan string, 16)
	go func() {
		defer close(done)
		for {
			n, err := raw.WaitForNotification(lctx)
			if err != nil {
				return
			}
			out <- n.Payload
		}
	}()
	return out
}

func expectNotice(t *testing.T, ch <-chan string, id, op string) {
	t.Helper()
	select {
	case p := <-ch:
		var msg struct{ ID, Op string }
		if err := json.Unmarshal([]byte(p), &msg); err != nil || msg.ID != id || msg.Op != op {
			t.Fatalf("notice %q, want id %s op %s", p, id, op)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("no %s notice for %s", op, id)
	}
}

func expectNoNotice(t *testing.T, ch <-chan string) {
	t.Helper()
	select {
	case p := <-ch:
		t.Fatalf("unexpected notice %q", p)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestStoreCRUDAndNotify(t *testing.T) {
	pool, ctx := testPool(t)
	s := NewPGStore(pool)
	notices := listen(t, ctx, pool)

	created, err := s.Create(ctx, fixed("test-coastal"))
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Version != 1 || created.Position == nil || created.Position.Lat != 50.1 || created.Coverage.MaxRangeM != 60000 || created.CreatedBy != "u1" {
		t.Fatalf("created = %+v", created)
	}
	expectNotice(t, notices, created.ID, "upsert")

	got, err := s.Get(ctx, created.ID)
	if err != nil || got.Name != "test-coastal" {
		t.Fatalf("get = %+v %v", got, err)
	}
	list, err := s.List(ctx, Filter{Mobility: MobilityFixed})
	if err != nil || !containsID(list, created.ID) {
		t.Fatalf("list = %v %v", list, err)
	}

	updated, err := s.Update(ctx, created.ID, 1, func(e *Emplacement) error { e.BoresightDeg = 270; return nil })
	if err != nil || updated.Version != 2 || updated.BoresightDeg != 270 || updated.UpdatedAt.Before(created.UpdatedAt) {
		t.Fatalf("update = %+v %v", updated, err)
	}
	expectNotice(t, notices, created.ID, "upsert")

	// Negative control: a refused update writes nothing and announces nothing.
	refuse := errors.New("refused")
	if _, err := s.Update(ctx, created.ID, 0, func(e *Emplacement) error { return refuse }); !errors.Is(err, refuse) {
		t.Fatalf("fn error = %v", err)
	}
	expectNoNotice(t, notices)

	if err := s.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	expectNotice(t, notices, created.ID, "delete")
	if _, err := s.Get(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete = %v", err)
	}
	if err := s.Delete(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
	if _, err := s.Get(ctx, "not-a-uuid"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("malformed id = %v", err)
	}
}

func TestStoreVersionConflict(t *testing.T) {
	pool, ctx := testPool(t)
	s := NewPGStore(pool)
	e, err := s.Create(ctx, fixed("test-conflict"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, e.ID, 1, func(x *Emplacement) error { x.Description = "first"; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, e.ID, 1, func(x *Emplacement) error { x.Description = "second"; return nil }); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale version = %v", err)
	}
}

func TestStoreNamesAreUniqueAmongLiveOnes(t *testing.T) {
	pool, ctx := testPool(t)
	s := NewPGStore(pool)
	a, err := s.Create(ctx, fixed("test-dup"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, fixed("TEST-DUP")); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("case-insensitive duplicate = %v", err)
	}
	if err := s.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, fixed("test-dup")); err != nil {
		t.Fatalf("a deleted emplacement's name is free again: %v", err)
	}
}

func TestStoreFeedBinding(t *testing.T) {
	pool, ctx := testPool(t)
	s := NewPGStore(pool)
	unknown := "test-no-such-feed"
	e := fixed("test-bound")
	e.FeedID = &unknown
	var ve *ValidationError
	if _, err := s.Create(ctx, e); !errors.As(err, &ve) || ve.Problems[0].Path != "/feedId" {
		t.Fatalf("unknown feed = %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sentinel.feeds (id, name, description, driver, config, secret_refs, enabled)
		VALUES ('test-emp-feed', 'test', '', 'tcp', '{"mode":"listen","port":6999}', '{}', false)`); err != nil {
		t.Fatal(err)
	}
	feed := "test-emp-feed"
	e.FeedID = &feed
	bound, err := s.Create(ctx, e)
	if err != nil || bound.FeedID == nil || *bound.FeedID != feed {
		t.Fatalf("bound = %+v %v", bound, err)
	}
	if list, _ := s.List(ctx, Filter{FeedID: feed}); len(list) != 1 {
		t.Fatalf("by feed = %v", list)
	}
	_, err = pool.Exec(ctx, `DELETE FROM sentinel.feeds WHERE id = 'test-emp-feed'`)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23503" {
		t.Fatalf("deleting a feed that still has emplacements = %v, want a foreign key violation (registry.Delete lets them go first)", err)
	}
	if got, _ := s.Get(ctx, bound.ID); got.FeedID == nil {
		t.Fatal("the refused delete unbound the emplacement")
	}
}

func containsID(list []Emplacement, id string) bool {
	for _, e := range list {
		if e.ID == id {
			return true
		}
	}
	return false
}

func TestStoreTouchIsQuiet(t *testing.T) {
	pool, ctx := testPool(t)
	s := NewPGStore(pool)
	e := fixed("test-touch")
	e.Mobility, e.Position = MobilityMobile, &Point{Lat: 50, Lon: -4}
	created, err := s.Create(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	notices := listen(t, ctx, pool)
	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

	alt := 120.5
	if err := s.Touch(ctx, created.ID, &Point{Lat: 50.5, Lon: -4.5}, &alt, at); err != nil {
		t.Fatal(err)
	}
	expectNoNotice(t, notices)
	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != created.Version || got.Position == nil || got.Position.Lat != 50.5 || got.Position.Lon != -4.5 ||
		got.Altitude == nil || *got.Altitude != 120.5 ||
		got.LastReportAt == nil || !got.LastReportAt.Equal(at) {
		t.Fatalf("after touch = %+v", got)
	}

	later := at.Add(time.Minute)
	if err := s.Touch(ctx, created.ID, nil, nil, later); err != nil { // no position or altitude: only the report time moves
		t.Fatal(err)
	}
	got, err = s.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Position.Lat != 50.5 || got.Altitude == nil || *got.Altitude != 120.5 || !got.LastReportAt.Equal(later) || got.Version != created.Version {
		t.Fatalf("touch without position = %+v", got)
	}

	if err := s.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	select { // the delete's own notice
	case <-notices:
	case <-time.After(3 * time.Second):
		t.Fatal("no notice for the delete")
	}
	if err := s.Touch(ctx, created.ID, nil, nil, later); !errors.Is(err, ErrNotFound) {
		t.Fatalf("touch after delete = %v", err)
	}
	if err := s.Touch(ctx, "not-a-uuid", nil, nil, later); !errors.Is(err, ErrNotFound) {
		t.Fatalf("touch malformed id = %v", err)
	}
}

// lastReportAt only moves forward: an older report landing late (another
// replica, a slower write) does not move it back.
func TestStoreTouchNeverMovesLastReportAtBack(t *testing.T) {
	pool, ctx := testPool(t)
	s := NewPGStore(pool)
	created, err := s.Create(ctx, fixed("test-touch-monotonic"))
	if err != nil {
		t.Fatal(err)
	}
	newer := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	if err := s.Touch(ctx, created.ID, nil, nil, newer); err != nil {
		t.Fatal(err)
	}
	if err := s.Touch(ctx, created.ID, nil, nil, newer.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastReportAt == nil || !got.LastReportAt.Equal(newer) {
		t.Fatalf("lastReportAt = %v after an older touch, want %v", got.LastReportAt, newer)
	}
	if err := s.Touch(ctx, created.ID, nil, nil, newer.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Get(ctx, created.ID); got.LastReportAt == nil || !got.LastReportAt.Equal(newer.Add(time.Minute)) {
		t.Fatalf("lastReportAt = %v after a newer touch", got.LastReportAt)
	}
}

// The database refuses an active emplacement with no connection, and the
// store answers it as the API's /feedId problem.
func TestStoreActiveNeedsAConnection(t *testing.T) {
	pool, ctx := testPool(t)
	s := NewPGStore(pool)
	e := fixed("test-active-unbound")
	e.Active = true
	_, err := s.Create(ctx, e)
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Problems) != 1 || ve.Problems[0].Path != "/feedId" {
		t.Fatalf("an active emplacement with no connection = %v, want one /feedId problem", err)
	}
}

func TestMapErrTurnsTheConnectionCheckIntoAProblem(t *testing.T) {
	err := mapErr(&pgconn.PgError{Code: "23514", ConstraintName: "emplacements_active_needs_feed"})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Problems[0].Path != "/feedId" {
		t.Fatalf("mapErr = %v, want a /feedId problem", err)
	}
	other := &pgconn.PgError{Code: "23514", ConstraintName: "emplacements_fixed_has_position"}
	if got := mapErr(other); !errors.Is(got, other) {
		t.Fatalf("another CHECK came back as %v", got)
	}
}

// A heartbeat is written quietly (no version, no notice), never moves
// lastHeartbeatAt back, and an Update keeps it unless the write clears it.
func TestStoreHeartbeat(t *testing.T) {
	pool, ctx := testPool(t)
	s := NewPGStore(pool)
	created, err := s.Create(ctx, fixed("test-heartbeat"))
	if err != nil {
		t.Fatal(err)
	}
	notices := listen(t, ctx, pool)
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	if err := s.Heartbeat(ctx, created.ID, 5, at); err != nil {
		t.Fatal(err)
	}
	expectNoNotice(t, notices)
	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != created.Version || got.HeartbeatIntervalSec == nil || *got.HeartbeatIntervalSec != 5 ||
		got.LastHeartbeatAt == nil || !got.LastHeartbeatAt.Equal(at) {
		t.Fatalf("after a heartbeat: %+v", got)
	}
	if err := s.Heartbeat(ctx, created.ID, 7, at.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Get(ctx, created.ID); !got.LastHeartbeatAt.Equal(at) || *got.HeartbeatIntervalSec != 7 {
		t.Fatalf("after an older heartbeat: last %v, interval %v", got.LastHeartbeatAt, *got.HeartbeatIntervalSec)
	}
	kept, err := s.Update(ctx, created.ID, 0, func(e *Emplacement) error { e.Description = "edited"; return nil })
	if err != nil || kept.LastHeartbeatAt == nil || !kept.LastHeartbeatAt.Equal(at) || kept.HeartbeatIntervalSec == nil {
		t.Fatalf("an edit lost the heartbeat: %+v %v", kept, err)
	}
	cleared, err := s.Update(ctx, created.ID, 0, func(e *Emplacement) error {
		e.LastHeartbeatAt, e.HeartbeatIntervalSec = nil, nil
		return nil
	})
	if err != nil || cleared.LastHeartbeatAt != nil || cleared.HeartbeatIntervalSec != nil {
		t.Fatalf("clearing the heartbeat: %+v %v", cleared, err)
	}
	if err := s.Heartbeat(ctx, "not-a-uuid", 5, at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("heartbeat of a malformed id = %v", err)
	}
}
