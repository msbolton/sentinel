package outbound

import (
	"testing"
	"time"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/ingest-service/internal/source/driver"
)

func upsert(id string, n int) driver.EntityUpdate {
	return driver.EntityUpdate{Kind: driver.UpsertEntity, EntityID: id, Entity: &sentinelv1.Entity{Id: id, Name: string(rune('a' + n))}}
}

func remove(id string) driver.EntityUpdate {
	return driver.EntityUpdate{Kind: driver.RemoveEntity, EntityID: id, Reason: "deleted"}
}

func TestQueueCoalescesAndKeepsOrder(t *testing.T) {
	q := NewQueue(100, 0)
	now := time.Now()
	q.Push(upsert("a", 0))
	q.Push(upsert("b", 0))
	q.Push(upsert("a", 1)) // replaces a's pending snapshot
	if q.Len() != 2 || q.Coalesced != 1 {
		t.Fatalf("len=%d coalesced=%d", q.Len(), q.Coalesced)
	}
	u, ok := q.Pop(now)
	if !ok || u.EntityID != "a" || u.Entity.Name != "b" {
		t.Fatalf("first pop = %+v %v", u, ok)
	}
	u, _ = q.Pop(now)
	if u.EntityID != "b" {
		t.Fatalf("second pop = %+v", u)
	}
	if _, ok := q.Pop(now); ok {
		t.Fatal("empty queue popped")
	}
}

func TestQueueRemovalWinsAndSurvivesOverflow(t *testing.T) {
	q := NewQueue(2, 0)
	q.Push(upsert("a", 0))
	q.Push(remove("a"))
	q.Push(upsert("a", 1)) // late snapshot after a removal is ignored
	q.Push(upsert("b", 0))
	q.Push(upsert("c", 0)) // overflow: the oldest upsert (b) goes, the removal stays
	if q.Len() != 2 || q.DroppedOldest != 1 {
		t.Fatalf("len=%d dropped=%d", q.Len(), q.DroppedOldest)
	}
	u, _ := q.Pop(time.Now())
	if u.Kind != driver.RemoveEntity || u.EntityID != "a" {
		t.Fatalf("pop = %+v", u)
	}
	u, _ = q.Pop(time.Now())
	if u.EntityID != "c" {
		t.Fatalf("pop = %+v", u)
	}
}

func TestQueuePerEntitySpacing(t *testing.T) {
	q := NewQueue(10, 5*time.Second)
	t0 := time.Now()
	q.Push(upsert("a", 0))
	if _, ok := q.Pop(t0); !ok {
		t.Fatal("first upsert must deliver")
	}
	q.Push(upsert("a", 1))
	q.Push(upsert("b", 0))
	if u, ok := q.Pop(t0.Add(time.Second)); !ok || u.EntityID != "b" {
		t.Fatalf("spacing should skip a and deliver b: %+v %v", u, ok)
	}
	if _, ok := q.Pop(t0.Add(time.Second)); ok {
		t.Fatal("a delivered before its gap elapsed")
	}
	if due := q.NextDue(t0.Add(time.Second)); due != 4*time.Second {
		t.Fatalf("next due = %v", due)
	}
	if u, ok := q.Pop(t0.Add(5 * time.Second)); !ok || u.EntityID != "a" {
		t.Fatalf("a after gap: %+v %v", u, ok)
	}
	// A removal ignores spacing.
	q.Push(remove("a"))
	if u, ok := q.Pop(t0.Add(6 * time.Second)); !ok || u.Kind != driver.RemoveEntity {
		t.Fatalf("removal delayed: %+v %v", u, ok)
	}
}
