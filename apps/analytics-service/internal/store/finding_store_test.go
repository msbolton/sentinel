package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFindingLifecycle(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := NewFindingStore(pool)
	entityID := "00000000-0000-4000-8000-00000000f001"
	secretEntity := "00000000-0000-4000-8000-00000000f002"
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM sentinel.findings WHERE "entityId" IN ($1, $2)`, entityID, secretEntity)
	})
	t0 := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

	open := func(sev string, at time.Time, class string) (*Finding, bool) {
		f, created, err := s.UpsertOpen(ctx, OpenParams{
			EntityID: entityID, Kind: "UNUSUAL_PLACE", Severity: sev, Score: 2, Summary: "far away " + sev,
			Expected: map[string]any{"thresholdM": 2000}, Actual: map[string]any{"distanceM": 5000, "at": at},
			Thresholds: map[string]any{"placeRangeFactor": 1.5}, Position: &GeoPoint{Lat: 40.1, Lon: -74},
			EvidenceStart: at, EvidenceEnd: at, Classification: class,
		})
		if err != nil {
			t.Fatal(err)
		}
		return f, created
	}
	first, created := open("MEDIUM", t0, "UNCLASSIFIED")
	if !created || first.Hits != 1 || first.Status != StatusOpen || first.Position == nil || first.Position.Lat != 40.1 {
		t.Fatalf("first = %+v created=%v", first, created)
	}
	// A repeat with a higher severity is absorbed: same id, hits 2, severity raised, evidence extended.
	second, created := open("HIGH", t0.Add(5*time.Minute), "UNCLASSIFIED")
	if created || second.ID != first.ID || second.Hits != 2 || second.Severity != "HIGH" || !second.EvidenceEnd.Equal(t0.Add(5*time.Minute)) || second.Summary != "far away HIGH" {
		t.Fatalf("second = %+v created=%v", second, created)
	}
	// A later lower-severity hit keeps HIGH.
	third, _ := open("LOW", t0.Add(6*time.Minute), "UNCLASSIFIED")
	if third.Severity != "HIGH" || third.Hits != 3 {
		t.Errorf("third = %+v", third)
	}
	if got, err := s.Get(ctx, first.ID); err != nil || got.Hits != 3 || got.Expected["thresholdM"] != float64(2000) || got.Thresholds["placeRangeFactor"] != 1.5 {
		t.Errorf("Get = %+v %v", got, err)
	}
	if _, err := s.Get(ctx, "00000000-0000-4000-8000-00000000ffff"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing finding error = %v", err)
	}

	// A secret entity's finding is hidden from an unclassified caller.
	if _, _, err := s.UpsertOpen(ctx, OpenParams{EntityID: secretEntity, Kind: "WENT_DARK", Severity: "MEDIUM", Summary: "dark", EvidenceStart: t0, EvidenceEnd: t0, Classification: "SECRET"}); err != nil {
		t.Fatal(err)
	}
	stats, err := s.Stats(ctx, "TOP_SECRET")
	if err != nil || stats["open"].(int64) < 2 || stats["openByKind"].(map[string]int64)["WENT_DARK"] != 1 {
		t.Errorf("stats = %v %v", stats, err)
	}
	if low, _ := s.Stats(ctx, "UNCLASSIFIED"); low["openByKind"].(map[string]int64)["WENT_DARK"] != 0 {
		t.Errorf("unclassified stats count a secret finding: %v", low)
	}
	items, total, err := s.List(ctx, Query{Status: StatusOpen, Ceiling: "UNCLASSIFIED", PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range items {
		if f.EntityID == secretEntity {
			t.Error("secret finding visible to an unclassified caller")
		}
	}
	_ = total
	items, _, err = s.List(ctx, Query{EntityID: secretEntity, Ceiling: "SECRET", PageSize: 50})
	if err != nil || len(items) != 1 {
		t.Errorf("secret caller sees %d secret findings (%v), want 1", len(items), err)
	}
	items, _, _ = s.List(ctx, Query{EntityID: entityID, Kinds: []string{"UNUSUAL_PLACE"}, Severity: "HIGH", Ceiling: "TOP_SECRET", PageSize: 50})
	if len(items) != 1 {
		t.Errorf("kind+severity filter returned %d", len(items))
	}

	// Alert id, review, and the transitions.
	if err := s.SetAlertID(ctx, first.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Review(ctx, first.ID, "CLOSED", "a"); !errors.Is(err, ErrBadTransition) {
		t.Errorf("review to CLOSED: %v", err)
	}
	confirmed, err := s.Review(ctx, first.ID, "CONFIRMED", "analyst-1")
	if err != nil || confirmed.Status != StatusConfirmed || confirmed.ReviewedBy == nil || *confirmed.ReviewedBy != "analyst-1" || confirmed.AlertID == nil {
		t.Fatalf("confirmed = %+v %v", confirmed, err)
	}
	if _, err := s.Review(ctx, first.ID, "CONFIRMED", "b"); !errors.Is(err, ErrBadTransition) {
		t.Errorf("confirm twice: %v", err)
	}
	if d, err := s.Review(ctx, first.ID, "DISMISSED", "b"); err != nil || d.Status != StatusDismissed {
		t.Errorf("confirmed -> dismissed: %+v %v", d, err)
	}
	// With the finding no longer OPEN, a new hit opens a fresh finding.
	fresh, created := open("MEDIUM", t0.Add(time.Hour), "UNCLASSIFIED")
	if !created || fresh.ID == first.ID {
		t.Errorf("fresh = %+v created=%v", fresh, created)
	}
	// CloseOpen and CloseStale.
	if err := s.CloseOpen(ctx, entityID, "UNUSUAL_PLACE"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, fresh.ID); got.Status != StatusClosed || got.ClosedAt == nil {
		t.Errorf("CloseOpen: %+v", got)
	}
	stale, _ := open("LOW", t0.Add(2*time.Hour), "UNCLASSIFIED")
	n, err := s.CloseStale(ctx, t0.Add(3*time.Hour))
	if err != nil || n < 1 {
		t.Errorf("CloseStale closed %d (%v)", n, err)
	}
	if got, _ := s.Get(ctx, stale.ID); got.Status != StatusClosed {
		t.Errorf("stale finding status = %s", got.Status)
	}
}
