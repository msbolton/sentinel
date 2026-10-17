package baseline

import (
	"context"
	"testing"
	"time"
)

type countingGetter struct {
	calls int
	b     *Baseline
}

func (g *countingGetter) Get(context.Context, string) (*Baseline, error) {
	g.calls++
	return g.b, nil
}

func TestCacheTTLAndNegativeCaching(t *testing.T) {
	src := &countingGetter{}
	c := NewCache(src, time.Minute)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	ctx := context.Background()

	if b, _ := c.Get(ctx, "e1"); b != nil {
		t.Fatal("expected no baseline")
	}
	c.Get(ctx, "e1")
	if src.calls != 1 {
		t.Errorf("missing baseline loaded %d times within the TTL, want 1", src.calls)
	}
	now = now.Add(2 * time.Minute)
	src.b = &Baseline{EntityID: "e1"}
	if b, _ := c.Get(ctx, "e1"); b == nil {
		t.Fatal("expired entry not reloaded")
	}
	if src.calls != 2 {
		t.Errorf("calls = %d, want 2", src.calls)
	}
	c.Invalidate("e1")
	c.Get(ctx, "e1")
	if src.calls != 3 {
		t.Errorf("invalidate did not force a reload: calls = %d", src.calls)
	}
	c.Put(&Baseline{EntityID: "e2", PointCount: 5})
	if b, _ := c.Get(ctx, "e2"); b == nil || b.PointCount != 5 {
		t.Error("Put not served from cache")
	}
	if src.calls != 3 || c.Len() != 2 {
		t.Errorf("calls = %d len = %d", src.calls, c.Len())
	}
}
