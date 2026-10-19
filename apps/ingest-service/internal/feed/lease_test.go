package feed

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/sentinel/ingest-service/internal/metrics"
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
	return New(reg, &memPublisher{}, zap.NewNop(), metrics.New(nil), Options{
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
	sup := New(reg, pub, zap.NewNop(), metrics.New(nil), Options{ReplicaID: "r1", Leaser: &replicaLeaser{fakeLeaser: l, me: "r1"}, Preferred: preferAlways("r1"),
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

// A handover to the preferred replica and a lease another replica took each
// count once for their feed; both paths panicked on counters that were never
// created.
func TestLeaseMetricsCountHandoverAndLoss(t *testing.T) {
	ctx := context.Background()
	counts := func(sup *Supervisor) (handovers, lost float64) {
		return testutil.ToFloat64(sup.metrics.LeaseHandovers.WithLabelValues("a")),
			testutil.ToFloat64(sup.metrics.LeasesLost.WithLabelValues("a"))
	}

	t.Run("handover", func(t *testing.T) {
		reg := newFakeRegistry()
		reg.set(feedSpec("a", 1, true))
		l := newFakeLeaser("r1", "r2")
		sup := newLeasedSupervisor(reg, l, "r2", preferAlways("r1"), func(driver.Spec) (driver.Driver, error) { return newMockDriver(), nil })
		sup.Reconcile(ctx) // r1 should own it, so r2 waits
		time.Sleep(l.ttl + 10*time.Millisecond)
		sup.Reconcile(ctx) // r1 did not claim it within a TTL: r2 steps in
		time.Sleep(50 * time.Millisecond)
		sup.Reconcile(ctx) // r1 is live and preferred: r2 hands it over
		if sup.Running("a") {
			t.Fatal("r2 must have handed the feed over")
		}
		if handovers, lost := counts(sup); handovers != 1 || lost != 0 {
			t.Errorf("after a handover: handovers=%v lost=%v, want 1 and 0", handovers, lost)
		}
	})

	t.Run("lost lease", func(t *testing.T) {
		reg := newFakeRegistry()
		reg.set(feedSpec("a", 1, true))
		l := newFakeLeaser("r1")
		sup := newLeasedSupervisor(reg, l, "r1", preferAlways("r1"), func(driver.Spec) (driver.Driver, error) { return newMockDriver(), nil })
		sup.Reconcile(ctx)
		if !sup.Running("a") {
			t.Fatal("feed should run")
		}
		l.steal("a", "r9")
		sup.Reconcile(ctx)
		if sup.Running("a") {
			t.Fatal("a lost lease must stop the feed")
		}
		if handovers, lost := counts(sup); handovers != 0 || lost != 1 {
			t.Errorf("after a lost lease: handovers=%v lost=%v, want 0 and 1", handovers, lost)
		}
	})
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

// stallingLeaser is a leaser whose Acquire hangs, as a database stall does,
// until its context ends.
type stallingLeaser struct {
	*replicaLeaser
	entered chan struct{} // told when an Acquire starts
	release chan struct{} // closed when the test ends, so a regression fails the test instead of leaking the call
}

func (l *stallingLeaser) Acquire(ctx context.Context, _ string) (int64, bool, error) {
	select {
	case l.entered <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return 0, false, ctx.Err()
	case <-l.release:
		return 0, false, errors.New("released by the test")
	}
}

// Reconcile takes a feed's lease with the supervisor's lock held, so a database
// that stalls there must not hold up what needs the lock: the scrape of the
// listener metrics, the status reads and the management API. The acquire gives
// up after one lease TTL, past which the lease would have expired anyway, and
// the feed is skipped this round.
func TestStalledLeaseAcquireDoesNotBlockTheSupervisor(t *testing.T) {
	reg := newFakeRegistry()
	reg.set(feedSpec("a", 1, true))
	l := newFakeLeaser("r1") // a 50 ms TTL
	stall := &stallingLeaser{replicaLeaser: &replicaLeaser{fakeLeaser: l, me: "r1"}, entered: make(chan struct{}, 1), release: make(chan struct{})}
	t.Cleanup(func() { close(stall.release) })
	core, logs := observer.New(zapcore.WarnLevel)
	sup := New(reg, &memPublisher{}, zap.New(core), metrics.New(nil), Options{
		ReplicaID: "r1", Leaser: stall, Preferred: preferAlways("r1"),
		Factory: func(driver.Spec, driver.Deps) (driver.Driver, error) { return newMockDriver(), nil },
	})

	reconciled := make(chan struct{})
	go func() { defer close(reconciled); sup.Reconcile(context.Background()) }()
	select {
	case <-stall.entered: // Reconcile is inside Acquire, holding the lock
	case <-time.After(5 * time.Second):
		t.Fatal("Reconcile never asked for the lease")
	}

	scraped := make(chan struct{})
	go func() { defer close(scraped); sup.ListenerStats() }()
	select {
	case <-scraped:
	case <-time.After(time.Second):
		t.Fatal("ListenerStats was still blocked 1 s into a stalled lease acquire")
	}
	select {
	case <-reconciled:
	case <-time.After(time.Second):
		t.Fatal("Reconcile was still in a stalled lease acquire after 1 s; it must give up after one lease TTL")
	}
	if sup.Running("a") {
		t.Error("a feed whose lease could not be taken must not run")
	}
	failed := logs.FilterMessage("lease acquire failed").All()
	if len(failed) != 1 || failed[0].ContextMap()["error"] != context.DeadlineExceeded.Error() {
		t.Errorf("warnings = %+v, want one \"lease acquire failed\" for the deadline", failed)
	}
}
