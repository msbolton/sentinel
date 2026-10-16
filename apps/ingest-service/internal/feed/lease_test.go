package feed

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/registry"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

// fakeLeaser is an in-memory lease table shared by several supervisors.
type fakeLeaser struct {
	mu       sync.Mutex
	ttl      time.Duration
	live     []string
	owners   map[string]string
	tokens   map[string]int64
	released []string
	left     []string
}

func newFakeLeaser(live ...string) *fakeLeaser {
	return &fakeLeaser{ttl: 50 * time.Millisecond, live: live, owners: map[string]string{}, tokens: map[string]int64{}}
}

type replicaLeaser struct {
	*fakeLeaser
	me string
}

func (l *replicaLeaser) Heartbeat(context.Context) error { return nil }
func (l *replicaLeaser) Live(context.Context) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.live...), nil
}
func (l *replicaLeaser) Acquire(_ context.Context, feed string) (int64, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if o, held := l.owners[feed]; held && o != l.me {
		return 0, false, nil
	}
	l.owners[feed] = l.me
	l.tokens[feed]++
	return l.tokens[feed], true, nil
}
func (l *replicaLeaser) Renew(_ context.Context, feed string, token int64) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.owners[feed] == l.me && l.tokens[feed] == token, nil
}
func (l *replicaLeaser) Release(_ context.Context, feed string, token int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.owners[feed] == l.me && l.tokens[feed] == token {
		delete(l.owners, feed)
		l.released = append(l.released, l.me+":"+feed)
	}
	return nil
}
func (l *replicaLeaser) Leave(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for f, o := range l.owners {
		if o == l.me {
			delete(l.owners, f)
		}
	}
	l.left = append(l.left, l.me)
	return nil
}
func (l *replicaLeaser) TTL() time.Duration { return l.ttl }

func (l *fakeLeaser) owner(feed string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.owners[feed]
}

func (l *fakeLeaser) steal(feed, by string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.owners[feed] = by
	l.tokens[feed]++
}

func (l *fakeLeaser) setLive(live ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.live = live
}

// preferAlways names a fixed owner for every feed.
func preferAlways(owner string) Preferred {
	return func(feed, pinned string, live []string) string {
		if pinned != "" {
			return pinned
		}
		for _, r := range live {
			if r == owner {
				return owner
			}
		}
		if len(live) > 0 {
			return live[0]
		}
		return ""
	}
}

func newLeasedSupervisor(reg *fakeRegistry, l *fakeLeaser, me string, prefer Preferred, factory func(driver.Spec) (driver.Driver, error)) *Supervisor {
	return New(reg, &memPublisher{}, zap.NewNop(), nil, Options{
		ReplicaID: me, RetryMin: 10 * time.Millisecond, RetryMax: 20 * time.Millisecond,
		Leaser: &replicaLeaser{fakeLeaser: l, me: me}, Preferred: prefer, RebalanceAfter: 40 * time.Millisecond,
		Factory: func(spec driver.Spec, _ driver.Deps) (driver.Driver, error) { return factory(spec) },
	})
}

func TestPreferredOwnerAcquiresAndStampsToken(t *testing.T) {
	reg := newFakeRegistry()
	reg.set(feedSpec("a", 1, true))
	l := newFakeLeaser("r1", "r2")
	pub := &memPublisher{}
	d := newMockDriver()
	sup := New(reg, pub, zap.NewNop(), nil, Options{ReplicaID: "r1", Leaser: &replicaLeaser{fakeLeaser: l, me: "r1"}, Preferred: preferAlways("r1"),
		Factory: func(driver.Spec, driver.Deps) (driver.Driver, error) { return d, nil }})
	ctx := context.Background()
	sup.Reconcile(ctx)
	if !sup.Running("a") || l.owner("a") != "r1" {
		t.Fatalf("preferred owner must take the feed: running=%v owner=%s", sup.Running("a"), l.owner("a"))
	}
	st, _ := sup.Snapshot("a")
	if st.FencingToken != 1 || st.FencedBy != "r1" {
		t.Errorf("status must be fenced: %+v", st)
	}
	var detail map[string]any
	_ = json.Unmarshal(st.Detail, &detail)
	if detail["leaseToken"] != float64(1) {
		t.Errorf("detail = %v", detail)
	}
}

func TestNonPreferredWaitsThenSteps_in(t *testing.T) {
	reg := newFakeRegistry()
	reg.set(feedSpec("a", 1, true))
	l := newFakeLeaser("r1", "r2")
	sup := newLeasedSupervisor(reg, l, "r2", preferAlways("r1"), func(driver.Spec) (driver.Driver, error) { return newMockDriver(), nil })
	ctx := context.Background()
	sup.Reconcile(ctx)
	sup.Reconcile(ctx)
	if sup.Running("a") {
		t.Fatal("r2 must leave the feed to r1 at first")
	}
	time.Sleep(l.ttl + 10*time.Millisecond)
	sup.Reconcile(ctx)
	if !sup.Running("a") || l.owner("a") != "r2" {
		t.Fatalf("after a TTL without r1 claiming it, r2 steps in: running=%v owner=%s", sup.Running("a"), l.owner("a"))
	}
	// r1 is live and preferred: after RebalanceAfter r2 hands over.
	time.Sleep(50 * time.Millisecond)
	sup.Reconcile(ctx)
	time.Sleep(5 * time.Millisecond)
	sup.Reconcile(ctx)
	if sup.Running("a") || l.owner("a") != "" || len(l.released) != 1 {
		t.Fatalf("handover: running=%v owner=%q released=%v", sup.Running("a"), l.owner("a"), l.released)
	}
	if reg.status("a").State != registry.StateStopped {
		t.Errorf("handover writes a stopped status while still holding the lease: %+v", reg.status("a"))
	}
}

func TestLostLeaseStopsWithoutStatusWrite(t *testing.T) {
	reg := newFakeRegistry()
	reg.set(feedSpec("a", 1, true))
	l := newFakeLeaser("r1")
	d := newMockDriver()
	sup := newLeasedSupervisor(reg, l, "r1", preferAlways("r1"), func(driver.Spec) (driver.Driver, error) { return d, nil })
	ctx := context.Background()
	sup.Reconcile(ctx)
	sup.flushStatus(ctx)
	if !sup.Running("a") || reg.status("a").State != registry.StateRunning {
		t.Fatal("feed should run")
	}
	l.steal("a", "r9")
	sup.Reconcile(ctx)
	if sup.Running("a") || d.stopped.Load() != 1 {
		t.Fatalf("lost lease must stop the driver at once: running=%v stopped=%d", sup.Running("a"), d.stopped.Load())
	}
	if reg.status("a").State != registry.StateRunning {
		t.Errorf("a replica that lost its lease must not write the status row (new owner's): %+v", reg.status("a"))
	}
	if len(l.released) != 0 {
		t.Errorf("must not release a lease it no longer holds: %v", l.released)
	}
}

func TestPinnedFeedRunsOnlyThere(t *testing.T) {
	reg := newFakeRegistry()
	pinned := feedSpec("p", 1, true)
	pinned.Config = json.RawMessage(`{"pinnedReplica":"r2"}`)
	reg.set(pinned)
	l := newFakeLeaser("r1", "r2")
	s1 := newLeasedSupervisor(reg, l, "r1", preferAlways("r1"), func(driver.Spec) (driver.Driver, error) { return newMockDriver(), nil })
	s2 := newLeasedSupervisor(reg, l, "r2", preferAlways("r1"), func(driver.Spec) (driver.Driver, error) { return newMockDriver(), nil })
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		s1.Reconcile(ctx)
		s2.Reconcile(ctx)
		time.Sleep(l.ttl)
	}
	if s1.Running("p") || !s2.Running("p") || l.owner("p") != "r2" {
		t.Fatalf("pinned: r1=%v r2=%v owner=%s", s1.Running("p"), s2.Running("p"), l.owner("p"))
	}
	// Re-pinning to r1 moves it: r2 stops on the next reconcile, r1 takes it.
	pinned.Config = json.RawMessage(`{"pinnedReplica":"r1"}`)
	reg.set(pinned)
	s2.Reconcile(ctx)
	s1.Reconcile(ctx)
	if s2.Running("p") || !s1.Running("p") {
		t.Fatalf("re-pin: r1=%v r2=%v", s1.Running("p"), s2.Running("p"))
	}
}

func TestTwoReplicasSplitFeedsAndShutdownLeaves(t *testing.T) {
	reg := newFakeRegistry()
	reg.set(feedSpec("a", 1, true), feedSpec("b", 1, true), feedSpec("c", 1, true), feedSpec("d", 1, true))
	l := newFakeLeaser("r1", "r2")
	mk := func(driver.Spec) (driver.Driver, error) { return newMockDriver(), nil }
	s1 := newLeasedSupervisor(reg, l, "r1", nil, mk)
	s2 := newLeasedSupervisor(reg, l, "r2", nil, mk)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		s1.Reconcile(ctx)
		s2.Reconcile(ctx)
		time.Sleep(l.ttl)
	}
	owners := map[string]int{}
	for _, f := range []string{"a", "b", "c", "d"} {
		if s1.Running(f) == s2.Running(f) {
			t.Errorf("feed %s: r1=%v r2=%v, want exactly one", f, s1.Running(f), s2.Running(f))
		}
		owners[l.owner(f)]++
	}
	if owners["r1"] == 0 || owners["r2"] == 0 {
		t.Errorf("feeds should spread over both replicas: %v", owners)
	}
	// r2 goes away: its feeds become free and r1 takes them after a TTL.
	s2.shutdown()
	l.setLive("r1")
	for i := 0; i < 3; i++ {
		s1.Reconcile(ctx)
		time.Sleep(l.ttl)
	}
	for _, f := range []string{"a", "b", "c", "d"} {
		if !s1.Running(f) {
			t.Errorf("after r2 left, r1 must run %s", f)
		}
	}
	if len(l.left) != 1 || l.left[0] != "r2" {
		t.Errorf("shutdown must leave the replica set: %v", l.left)
	}
}
