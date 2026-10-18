package feed

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/registry"
	_ "github.com/sentinel/ingest-service/internal/source/all"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

// listenerLeaser adds the listener set to the fake leaser used by lease_test.go.
type listenerLeaser struct {
	*replicaLeaser
	listeners []string
}

func (l *listenerLeaser) Listeners(context.Context) ([]string, error) { return l.listeners, nil }

func udpFeed(id string) registry.Feed {
	return registry.Feed{ID: id, Name: id, Driver: "stream", Enabled: true, Version: 1,
		Config: json.RawMessage(`{"transport":{"kind":"udp","address":"127.0.0.1:0"},"decoder":{"name":"nmea-ais"}}`)}
}

func newListenerTestSupervisor(t *testing.T, me string, listener bool, listeners []string) *Supervisor {
	t.Helper()
	l := newFakeLeaser("r-listen", "r-plain")
	sup := New(newFakeRegistry(), &memPublisher{}, zap.NewNop(), metrics.New(nil), Options{
		ReplicaID: me, Listener: listener, Preferred: defaultPreferred,
		Leaser: &listenerLeaser{replicaLeaser: &replicaLeaser{fakeLeaser: l, me: me}, listeners: listeners},
	})
	sup.live = []string{"r-listen", "r-plain"}
	sup.listeners = listeners
	return sup
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

// The listener set reaches the supervisor through the leaser, listening
// feeds are placed on listener replicas only, and other feeds still spread
// over every live replica.
func TestReconcilePlacesListeningFeedsOnListeners(t *testing.T) {
	reg := newFakeRegistry()
	reg.set(udpFeed("pier"), feedSpec("a", 1, true), feedSpec("b", 1, true), feedSpec("c", 1, true), feedSpec("d", 1, true))
	l := newFakeLeaser("r-listen", "r-plain")
	hosts := map[string]string{}
	mk := func(me string, listener bool, host string) *Supervisor {
		return New(reg, &memPublisher{}, zap.NewNop(), metrics.New(nil), Options{
			ReplicaID: me, Listener: listener, ListenerHost: host, Preferred: defaultPreferred,
			Leaser: &listenerLeaser{replicaLeaser: &replicaLeaser{fakeLeaser: l, me: me}, listeners: []string{"r-listen"}},
			Factory: func(spec driver.Spec, deps driver.Deps) (driver.Driver, error) {
				hosts[me+"/"+spec.FeedID] = deps.ListenerHost
				return newMockDriver(), nil
			},
		})
	}
	listen, plain := mk("r-listen", true, "ingest.example"), mk("r-plain", false, "")
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		listen.Reconcile(ctx)
		plain.Reconcile(ctx)
		time.Sleep(l.ttl)
	}
	if !listen.Running("pier") || plain.Running("pier") || l.owner("pier") != "r-listen" {
		t.Fatalf("listening feed: listener=%v plain=%v owner=%q", listen.Running("pier"), plain.Running("pier"), l.owner("pier"))
	}
	if got := hosts["r-listen/pier"]; got != "ingest.example" {
		t.Errorf("driver deps ListenerHost = %q, want ingest.example", got)
	}
	onPlain := 0
	for _, f := range []string{"a", "b", "c", "d"} {
		if listen.Running(f) == plain.Running(f) {
			t.Errorf("feed %s: listener=%v plain=%v, want exactly one", f, listen.Running(f), plain.Running(f))
		}
		if plain.Running(f) {
			onPlain++
		}
	}
	if onPlain == 0 {
		t.Error("non-listening feeds should still spread over every live replica")
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
}

func TestCandidates(t *testing.T) {
	sup := newListenerTestSupervisor(t, "r-plain", false, []string{"r-listen"})
	if got := sup.candidates(udpFeed("pier")); len(got) != 1 || got[0] != "r-listen" {
		t.Errorf("listening feed candidates = %v, want [r-listen]", got)
	}
	if got := sup.candidates(feedSpec("a", 1, true)); len(got) != 2 {
		t.Errorf("plain feed candidates = %v, want every live replica", got)
	}
}
