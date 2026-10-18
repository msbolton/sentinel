package feed

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/registry"
	_ "github.com/sentinel/ingest-service/internal/source/all"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

// listenerLeaser adds the listener set to the fake leaser used by lease_test.go:
// every live listener replica, and the dedicated ones among them.
type listenerLeaser struct {
	*replicaLeaser
	listeners []string
	dedicated []string
}

func (l *listenerLeaser) Listeners(context.Context) (all, dedicated []string, err error) {
	return l.listeners, l.dedicated, nil
}

func udpFeed(id string) registry.Feed {
	return registry.Feed{ID: id, Name: id, Driver: "stream", Enabled: true, Version: 1,
		Config: json.RawMessage(`{"transport":{"kind":"udp","address":"127.0.0.1:0"},"decoder":{"name":"nmea-ais"}}`)}
}

// pinnedTo returns the feed with its config pinned to a replica.
func pinnedTo(t *testing.T, spec registry.Feed, replica string) registry.Feed {
	t.Helper()
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(spec.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	pin, _ := json.Marshal(replica)
	cfg["pinnedReplica"] = pin
	config, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	spec.Config = config
	return spec
}

// newListenerTestSupervisor builds a supervisor that has already seen the
// live set {r-listen, r-plain} and the given listeners, all of them dedicated.
func newListenerTestSupervisor(t *testing.T, me string, listener bool, listeners []string) *Supervisor {
	t.Helper()
	l := newFakeLeaser("r-listen", "r-plain")
	sup := New(newFakeRegistry(), &memPublisher{}, zap.NewNop(), metrics.New(nil), Options{
		ReplicaID: me, Listener: listener, Preferred: defaultPreferred,
		Leaser: &listenerLeaser{replicaLeaser: &replicaLeaser{fakeLeaser: l, me: me}, listeners: listeners, dedicated: listeners},
	})
	sup.live = []string{"r-listen", "r-plain"}
	sup.listeners, sup.dedicated = listeners, listeners
	return sup
}

// unplaced reads the feeds_unplaced gauge for one reason.
func unplaced(sup *Supervisor, reason string) float64 {
	return testutil.ToFloat64(sup.metrics.FeedsUnplaced.WithLabelValues(reason))
}

// listenerFixture is what the replicas of one Reconcile test share: the
// registry, the lease table and the listener set the lease table reports. A
// replica named in listeners runs as a listener, and as a shared one unless
// it is also in dedicated, which is how the coordinator reads the table.
type listenerFixture struct {
	reg       *fakeRegistry
	l         *fakeLeaser
	listeners []string          // every live listener replica
	dedicated []string          // the listeners among them that run listening connections only
	hosts     map[string]string // "replica/feed" -> the ListenerHost the feed's driver was built with
}

func newListenerFixture(live, listeners, dedicated []string, feeds ...registry.Feed) *listenerFixture {
	reg := newFakeRegistry()
	reg.set(feeds...)
	return &listenerFixture{reg: reg, l: newFakeLeaser(live...), listeners: listeners, dedicated: dedicated, hosts: map[string]string{}}
}

func (f *listenerFixture) supervisor(me string) *Supervisor {
	listener := slices.Contains(f.listeners, me)
	host := ""
	if listener {
		host = "ingest.example"
	}
	return New(f.reg, &memPublisher{}, zap.NewNop(), metrics.New(nil), Options{
		ReplicaID: me, Listener: listener, ListenerShared: listener && !slices.Contains(f.dedicated, me), ListenerHost: host,
		Preferred: defaultPreferred,
		Leaser:    &listenerLeaser{replicaLeaser: &replicaLeaser{fakeLeaser: f.l, me: me}, listeners: f.listeners, dedicated: f.dedicated},
		Factory: func(spec driver.Spec, deps driver.Deps) (driver.Driver, error) {
			f.hosts[me+"/"+spec.FeedID] = deps.ListenerHost
			return newMockDriver(), nil
		},
	})
}

// rounds reconciles every supervisor in turn n times, a lease TTL apart.
func (f *listenerFixture) rounds(n int, sups ...*Supervisor) {
	for i := 0; i < n; i++ {
		for _, sup := range sups {
			sup.Reconcile(context.Background())
		}
		time.Sleep(f.l.ttl)
	}
}

func TestListeningFeedsRunOnlyOnListeners(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	for _, tc := range []struct {
		name      string
		me        string
		listener  bool
		listeners []string
		want      bool
	}{
		{"listener runs it", "r-listen", true, []string{"r-listen"}, true},
		{"plain replica never does", "r-plain", false, []string{"r-listen"}, false},
		{"plain replica even with no listener", "r-plain", false, nil, false},
	} {
		sup := newListenerTestSupervisor(t, tc.me, tc.listener, tc.listeners)
		_, ok := sup.owns(ctx, udpFeed("pier"), now.Add(time.Hour))
		if ok != tc.want {
			t.Errorf("%s: owns = %v, want %v", tc.name, ok, tc.want)
		}
		if len(sup.waitingSince) != 0 {
			t.Errorf("%s: waitingSince = %v, want no wait bookkeeping for a listening feed", tc.name, sup.waitingSince)
		}
	}
	// A pin still wins.
	sup := newListenerTestSupervisor(t, "r-plain", false, []string{"r-listen"})
	pinned := udpFeed("pinned")
	pinned.Config = json.RawMessage(`{"transport":{"kind":"udp","address":"127.0.0.1:0"},"decoder":{"name":"nmea-ais"},"pinnedReplica":"r-plain"}`)
	if _, ok := sup.owns(ctx, pinned, now.Add(time.Hour)); !ok {
		t.Error("an explicit pin wins over listener placement")
	}
}

// A dedicated listener (the default) runs the listening connections and the
// feeds pinned to it, and nothing else: the ordinary feeds all go to the
// ordinary replica, however the rendezvous hash would have split them.
func TestDedicatedListenerRunsOnlyListeningAndPinnedFeeds(t *testing.T) {
	fx := newListenerFixture([]string{"r-listen", "r-plain"}, []string{"r-listen"}, []string{"r-listen"},
		udpFeed("pier"), feedSpec("a", 1, true), feedSpec("b", 1, true), feedSpec("c", 1, true), feedSpec("d", 1, true),
		pinnedTo(t, feedSpec("p", 1, true), "r-listen"))
	listen, plain := fx.supervisor("r-listen"), fx.supervisor("r-plain")

	// The first round places them: the ordinary replica is their preferred
	// owner, so none waits a lease TTL for a listener to claim it.
	ctx := context.Background()
	listen.Reconcile(ctx)
	plain.Reconcile(ctx)
	for _, id := range []string{"a", "b", "c", "d"} {
		if !plain.Running(id) {
			t.Errorf("feed %s is not running on the ordinary replica after the first round", id)
		}
	}
	fx.rounds(3, listen, plain)

	for _, id := range []string{"pier", "p"} {
		if !listen.Running(id) || plain.Running(id) || fx.l.owner(id) != "r-listen" {
			t.Errorf("feed %s: listener=%v plain=%v owner=%q, want it on the listener only", id, listen.Running(id), plain.Running(id), fx.l.owner(id))
		}
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		if listen.Running(id) || !plain.Running(id) || fx.l.owner(id) != "r-plain" {
			t.Errorf("feed %s: listener=%v plain=%v owner=%q, want it on the ordinary replica only", id, listen.Running(id), plain.Running(id), fx.l.owner(id))
		}
	}
	if got := fx.hosts["r-listen/pier"]; got != "ingest.example" {
		t.Errorf("driver deps ListenerHost = %q, want ingest.example", got)
	}
	if len(listen.waitingSince) != 0 {
		t.Errorf("the listener's waitingSince = %v, want none: it never waits to step in", listen.waitingSince)
	}
	for _, sup := range []*Supervisor{listen, plain} {
		if n := unplaced(sup, "no_listener") + unplaced(sup, "no_ordinary_replica"); n != 0 {
			t.Errorf("replica %s counts %v feeds unplaced, want none", sup.ReplicaID(), n)
		}
	}
}

// A listener started with INGEST_LISTENER_SHARED=true also takes its
// rendezvous share of the ordinary feeds, next to the listening connections
// that only listeners run.
func TestSharedListenerTakesItsShare(t *testing.T) {
	fx := newListenerFixture([]string{"r-listen", "r-plain"}, []string{"r-listen"}, nil,
		udpFeed("pier"), feedSpec("a", 1, true), feedSpec("b", 1, true), feedSpec("c", 1, true), feedSpec("d", 1, true))
	listen, plain := fx.supervisor("r-listen"), fx.supervisor("r-plain")
	fx.rounds(4, listen, plain)

	if !listen.Running("pier") || plain.Running("pier") || fx.l.owner("pier") != "r-listen" {
		t.Fatalf("listening feed: listener=%v plain=%v owner=%q", listen.Running("pier"), plain.Running("pier"), fx.l.owner("pier"))
	}
	onListen, onPlain := 0, 0
	for _, id := range []string{"a", "b", "c", "d"} {
		if listen.Running(id) == plain.Running(id) {
			t.Errorf("feed %s: listener=%v plain=%v, want exactly one", id, listen.Running(id), plain.Running(id))
		}
		if listen.Running(id) {
			onListen++
		}
		if plain.Running(id) {
			onPlain++
		}
	}
	if onListen == 0 || onPlain == 0 {
		t.Errorf("ordinary feeds should spread over both live replicas: listener %d, ordinary %d", onListen, onPlain)
	}
}

// With no live listener a listening feed is simply not run; it is not handed
// to a plain replica.
func TestListeningFeedWaitsForAListener(t *testing.T) {
	reg := newFakeRegistry()
	reg.set(udpFeed("pier"))
	l := newFakeLeaser("r-plain")
	plain := New(reg, &memPublisher{}, zap.NewNop(), metrics.New(nil), Options{
		ReplicaID: "r-plain", Preferred: defaultPreferred,
		Leaser:  &listenerLeaser{replicaLeaser: &replicaLeaser{fakeLeaser: l, me: "r-plain"}},
		Factory: func(driver.Spec, driver.Deps) (driver.Driver, error) { return newMockDriver(), nil },
	})
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		plain.Reconcile(ctx)
		time.Sleep(l.ttl)
	}
	if plain.Running("pier") || l.owner("pier") != "" {
		t.Fatalf("a plain replica must not take a listening feed: running=%v owner=%q", plain.Running("pier"), l.owner("pier"))
	}
	if got := unplaced(plain, "no_listener"); got != 1 {
		t.Errorf("feeds_unplaced{no_listener} = %v, want 1", got)
	}
}

// With only dedicated listeners live the ordinary feeds wait: nothing runs and
// nothing is leased however long that takes, the gauge counts the feeds that
// wait, and an ordinary replica joining takes them and clears the count.
func TestOrdinaryFeedsWaitWhenOnlyDedicatedListenersLive(t *testing.T) {
	ctx := context.Background()
	feeds := []string{"a", "b", "c", "d"}
	fx := newListenerFixture([]string{"r-listen"}, []string{"r-listen"}, []string{"r-listen"},
		feedSpec("a", 1, true), feedSpec("b", 1, true), feedSpec("c", 1, true), feedSpec("d", 1, true))
	listen := fx.supervisor("r-listen")
	// Well past the lease TTL, which is when an ordinary replica would step in.
	fx.rounds(5, listen)

	for _, id := range feeds {
		if listen.Running(id) || fx.l.owner(id) != "" {
			t.Errorf("feed %s: running=%v owner=%q, want it waiting for an ordinary replica", id, listen.Running(id), fx.l.owner(id))
		}
	}
	if got := unplaced(listen, "no_ordinary_replica"); got != float64(len(feeds)) {
		t.Errorf("feeds_unplaced{no_ordinary_replica} = %v, want %d", got, len(feeds))
	}
	if got := unplaced(listen, "no_listener"); got != 0 {
		t.Errorf("feeds_unplaced{no_listener} = %v, want 0: a listener is live", got)
	}

	// An ordinary replica joins.
	fx.l.setLive("r-listen", "r-plain")
	plain := fx.supervisor("r-plain")
	plain.Reconcile(ctx)
	listen.Reconcile(ctx)
	for _, id := range feeds {
		if !plain.Running(id) || listen.Running(id) || fx.l.owner(id) != "r-plain" {
			t.Errorf("feed %s after r-plain joined: plain=%v listener=%v owner=%q", id, plain.Running(id), listen.Running(id), fx.l.owner(id))
		}
	}
	for _, sup := range []*Supervisor{listen, plain} {
		if got := unplaced(sup, "no_ordinary_replica"); got != 0 {
			t.Errorf("replica %s: feeds_unplaced{no_ordinary_replica} = %v after r-plain joined, want 0", sup.ReplicaID(), got)
		}
	}
}

// A dedicated listener does not step in for an ordinary feed whose preferred
// owner is live but silent, which is what an ordinary replica does after a
// lease TTL: a listener has no automatic failover role.
func TestDedicatedListenerNeverStepsIn(t *testing.T) {
	// Feed a would be r-listen's own by rendezvous hash if it took ordinary
	// feeds; r-plain is live in the lease table and never reconciles.
	fx := newListenerFixture([]string{"r-listen", "r-plain"}, []string{"r-listen"}, []string{"r-listen"}, feedSpec("a", 1, true))
	listen := fx.supervisor("r-listen")
	fx.rounds(5, listen)

	if listen.Running("a") || fx.l.owner("a") != "" {
		t.Errorf("the listener took an ordinary feed: running=%v owner=%q", listen.Running("a"), fx.l.owner("a"))
	}
	if len(listen.waitingSince) != 0 {
		t.Errorf("waitingSince = %v, want none: a dedicated listener never starts waiting to step in", listen.waitingSince)
	}
	if got := unplaced(listen, "no_ordinary_replica"); got != 0 {
		t.Errorf("feeds_unplaced{no_ordinary_replica} = %v, want 0: an ordinary replica is live", got)
	}
}

// Only an enabled feed without a pin counts as unplaced, per reason: a
// disabled feed waits for nobody, and a pinned one runs where it is pinned.
func TestUnplacedCountsEnabledUnpinnedFeedsByReason(t *testing.T) {
	off, offPier := feedSpec("off", 1, false), udpFeed("off-pier")
	offPier.Enabled = false
	// An empty lease table leaves feeds of both kinds without a replica.
	fx := newListenerFixture(nil, nil, nil,
		feedSpec("a", 1, true), off, pinnedTo(t, feedSpec("pinned", 1, true), "r-host"),
		udpFeed("pier"), offPier, pinnedTo(t, udpFeed("pinned-pier"), "r-host"))
	plain := fx.supervisor("r-plain")
	fx.rounds(2, plain)

	if got := unplaced(plain, "no_ordinary_replica"); got != 1 {
		t.Errorf("feeds_unplaced{no_ordinary_replica} = %v, want 1 (only feed a)", got)
	}
	if got := unplaced(plain, "no_listener"); got != 1 {
		t.Errorf("feeds_unplaced{no_listener} = %v, want 1 (only feed pier)", got)
	}
}

// The log says when feeds start or stop having a replica, once per change and
// not on every reconcile.
func TestUnplacedIsLoggedWhenItStartsAndStops(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	reg := newFakeRegistry()
	reg.set(udpFeed("pier"))
	l := newFakeLeaser("r-plain")
	leaser := &listenerLeaser{replicaLeaser: &replicaLeaser{fakeLeaser: l, me: "r-plain"}}
	plain := New(reg, &memPublisher{}, zap.New(core), metrics.New(nil), Options{
		ReplicaID: "r-plain", Preferred: defaultPreferred, Leaser: leaser,
		Factory: func(driver.Spec, driver.Deps) (driver.Driver, error) { return newMockDriver(), nil },
	})
	ctx := context.Background()
	about := func() []observer.LoggedEntry {
		return logs.FilterField(zap.String("reason", "no_listener")).All()
	}

	for i := 0; i < 3; i++ {
		plain.Reconcile(ctx)
	}
	entries := about()
	if len(entries) != 1 || entries[0].Level != zapcore.WarnLevel || entries[0].ContextMap()["feeds"] != int64(1) {
		t.Fatalf("after three reconciles without a listener: %+v, want one warning naming 1 feed", entries)
	}

	leaser.listeners, leaser.dedicated = []string{"r-listen"}, []string{"r-listen"}
	l.setLive("r-listen", "r-plain")
	for i := 0; i < 3; i++ {
		plain.Reconcile(ctx)
	}
	entries = about()
	if len(entries) != 2 || entries[1].Level != zapcore.InfoLevel {
		t.Fatalf("after a listener joined: %+v, want the warning and then one info", entries)
	}
	if got := logs.FilterField(zap.String("reason", "no_ordinary_replica")).Len(); got != 0 {
		t.Errorf("%d entries about ordinary feeds, want none: there are none", got)
	}
}

func TestCandidates(t *testing.T) {
	sup := newListenerTestSupervisor(t, "r-plain", false, nil)
	for _, tc := range []struct {
		name                       string
		live, listeners, dedicated []string
		listening, ordinary        []string
	}{
		{"a dedicated listener and an ordinary replica",
			[]string{"r-listen", "r-plain"}, []string{"r-listen"}, []string{"r-listen"},
			[]string{"r-listen"}, []string{"r-plain"}},
		{"a shared listener and an ordinary replica",
			[]string{"r-listen", "r-plain"}, []string{"r-listen"}, nil,
			[]string{"r-listen"}, []string{"r-listen", "r-plain"}},
		{"a dedicated and a shared listener and an ordinary replica",
			[]string{"r-ded", "r-plain", "r-shared"}, []string{"r-ded", "r-shared"}, []string{"r-ded"},
			[]string{"r-ded", "r-shared"}, []string{"r-plain", "r-shared"}},
		{"only a dedicated listener: ordinary feeds have no candidate",
			[]string{"r-listen"}, []string{"r-listen"}, []string{"r-listen"},
			[]string{"r-listen"}, nil},
		{"no listener",
			[]string{"r-plain"}, nil, nil,
			nil, []string{"r-plain"}},
	} {
		sup.live, sup.listeners, sup.dedicated = tc.live, tc.listeners, tc.dedicated
		if got := sup.candidates(udpFeed("pier")); !slices.Equal(got, tc.listening) {
			t.Errorf("%s: listening feed candidates = %v, want %v", tc.name, got, tc.listening)
		}
		if got := sup.candidates(feedSpec("a", 1, true)); !slices.Equal(got, tc.ordinary) {
			t.Errorf("%s: ordinary feed candidates = %v, want %v", tc.name, got, tc.ordinary)
		}
	}
}
