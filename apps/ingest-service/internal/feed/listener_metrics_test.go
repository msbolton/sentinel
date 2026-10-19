package feed

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/registry"
	_ "github.com/sentinel/ingest-service/internal/source/all"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/transport/transporttest"
)

// detailDriver is a mock driver that reports health detail, the way a
// listener reports its counters.
type detailDriver struct {
	*mockDriver
	detail func() map[string]any
}

func (d *detailDriver) Health() driver.Health { return driver.Health{Detail: d.detail()} }

// tcpFeed is a tcp connection with the given config.
func tcpFeed(id, config string) registry.Feed {
	return registry.Feed{ID: id, Name: id, Driver: "tcp", Enabled: true, Version: 1, Config: json.RawMessage(config)}
}

// fixedListeners is a metrics.ListenerSource with a fixed answer.
type fixedListeners []metrics.ListenerStat

func (f fixedListeners) ListenerStats() []metrics.ListenerStat { return f }

// listenerDrivers maps the feeds ListenerStats reports to their driver ids.
func listenerDrivers(t *testing.T, sup *Supervisor) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, st := range sup.ListenerStats() {
		if _, dup := out[st.FeedID]; dup {
			t.Errorf("feed %s is listed twice", st.FeedID)
		}
		out[st.FeedID] = st.Driver
		if st.Detail["feed"] != st.FeedID {
			t.Errorf("feed %s: detail %v is not the one its driver reported", st.FeedID, st.Detail)
		}
	}
	return out
}

// The collector exports what ListenerStats lists, so a feed that is not up, or
// does not listen, must not appear: its series would claim a listener that is
// not there.
func TestListenerStatsListsOnlyRunningListeningFeeds(t *testing.T) {
	disabled := udpFeed("off")
	disabled.Enabled = false

	var mu sync.Mutex
	built := map[string]*detailDriver{}
	reg := newFakeRegistry()
	sup := newSupervisor(reg, func(spec driver.Spec) (driver.Driver, error) {
		if spec.FeedID == "unbindable" {
			return nil, errors.New("port is taken")
		}
		d := &detailDriver{mockDriver: newMockDriver(), detail: func() map[string]any {
			return map[string]any{"feed": spec.FeedID, "accepted": int64(1)}
		}}
		mu.Lock()
		built[spec.FeedID] = d
		mu.Unlock()
		return d, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel) // stops the drivers the test leaves running

	reg.set(
		// Listen and run.
		udpFeed("pier"),
		tcpFeed("gate", `{"mode":"listen","port":6000}`),
		// Run, but bind no port.
		tcpFeed("client", `{"mode":"connect","host":"sender.example","port":30003}`),
		feedSpec("plain", 1, true),
		// Listen, but are not up: one is disabled, one fails to start.
		disabled,
		udpFeed("unbindable"),
	)
	sup.Reconcile(ctx)
	for _, id := range []string{"pier", "gate", "client", "plain"} {
		if !sup.Running(id) {
			t.Fatalf("feed %s is not running", id)
		}
	}
	if got, want := listenerDrivers(t, sup), map[string]string{"pier": "stream", "gate": "tcp"}; !maps.Equal(got, want) {
		t.Errorf("listeners = %v, want %v: the running feeds that bind a port", got, want)
	}

	// A feed that is disabled is gone.
	pier := udpFeed("pier")
	pier.Enabled = false
	reg.set(pier, tcpFeed("gate", `{"mode":"listen","port":6000}`))
	sup.Reconcile(ctx)
	if got, want := listenerDrivers(t, sup), map[string]string{"gate": "tcp"}; !maps.Equal(got, want) {
		t.Errorf("after disabling pier: listeners = %v, want %v", got, want)
	}

	// So is one whose driver stopped on its own, until it is restarted.
	mu.Lock()
	gate := built["gate"]
	mu.Unlock()
	gate.failRun <- errors.New("accept: too many open files")
	if !waitFor(func() bool { return !sup.Running("gate") }, 2*time.Second) {
		t.Fatal("gate is still running after its driver failed")
	}
	if got := listenerDrivers(t, sup); len(got) != 0 {
		t.Errorf("after the driver failed: listeners = %v, want none", got)
	}
}

// Health reads transport state under locks of its own, so ListenerStats copies
// what it needs under the supervisor's lock and calls it once that is
// released. A driver whose Health asks the supervisor something would
// deadlock otherwise.
func TestListenerStatsReadsHealthOutsideTheLock(t *testing.T) {
	reg := newFakeRegistry()
	var sup *Supervisor
	sup = newSupervisor(reg, func(spec driver.Spec) (driver.Driver, error) {
		return &detailDriver{mockDriver: newMockDriver(), detail: func() map[string]any {
			return map[string]any{"feed": spec.FeedID, "running": sup.Running(spec.FeedID)} // takes the supervisor's lock
		}}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel) // not the supervisor's lock: a failing run may leave that held
	reg.set(udpFeed("pier"))
	sup.Reconcile(ctx)

	done := make(chan []metrics.ListenerStat, 1)
	go func() { done <- sup.ListenerStats() }()
	select {
	case stats := <-done:
		if len(stats) != 1 || stats[0].Detail["running"] != true {
			t.Errorf("ListenerStats = %+v, want pier with running=true", stats)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListenerStats did not return: it called Health with the supervisor's lock held")
	}
}

// Scrapes run on the HTTP server's goroutines while Reconcile starts and stops
// feeds, and two scrapes can overlap: neither ListenerStats nor the collector
// over it may race with that (run with -race).
func TestListenerStatsWhileReconciling(t *testing.T) {
	reg := newFakeRegistry()
	sup := newSupervisor(reg, func(spec driver.Spec) (driver.Driver, error) {
		return &detailDriver{mockDriver: newMockDriver(), detail: func() map[string]any {
			return map[string]any{"feed": spec.FeedID, "transport": "udp", "accepted": int64(1), "distinctSenders": 1}
		}}, nil
	})
	scrapes := prometheus.NewPedanticRegistry()
	scrapes.MustRegister(metrics.NewListenerCollector(sup))

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				sup.ListenerStats()
				if _, err := scrapes.Gather(); err != nil {
					t.Errorf("scrape: %v", err)
					return
				}
			}
		}()
	}

	ctx := context.Background()
	for round := 1; round <= 31; round++ {
		// A new version restarts a feed, and quay is out every third round,
		// so feeds start and stop under the readers.
		pier, quay := udpFeed("pier"), udpFeed("quay")
		pier.Version, quay.Version = round, round
		quay.Enabled = round%3 != 0
		reg.set(pier, quay)
		sup.Reconcile(ctx)
	}
	close(stop)
	readers.Wait()

	if got := len(sup.ListenerStats()); got != 2 {
		t.Errorf("after the last round: %d listeners, want 2", got)
	}
	if got, err := testutil.GatherAndCount(scrapes, "sentinel_ingest_listener_senders"); err != nil || got != 2 {
		t.Errorf("after the last round: %d sender series (err %v), want 2", got, err)
	}
	reg.set()
	sup.Reconcile(ctx)
	if got, err := testutil.GatherAndCount(scrapes); err != nil || got != 0 {
		t.Errorf("after the feeds left: %d series (err %v), want none", got, err)
	}
}

// datagramOnlyDetailKeys are the health detail keys only a datagram listener
// has. A listener whose senders connect has no receive buffer of its own to
// size and no datagrams for the kernel to drop, so it must not report them: the
// series would claim a count it cannot make. kernelDropped is an event the
// collector exports; receiveBufferEffectiveBytes is a size, which it does not.
var datagramOnlyDetailKeys = []string{"kernelDropped", "receiveBufferEffectiveBytes"}

// tlsOnlyDetailKeys are the health detail keys only a listener with TLS has: a
// plain one has no certificate to expire and nothing to reload, so reporting
// them would claim a state it cannot be in. tlsReloadFailed is an event the
// collector exports; tlsReloads is a count of swaps, which it does not export,
// and tlsCertNotAfter is RFC 3339 text (the collector exports a timestamp from
// it), the one key that the numeric guard exempts, by name.
var tlsOnlyDetailKeys = []string{"tlsReloadFailed", "tlsReloads", "tlsCertNotAfter"}

// The collector reads counters out of the health detail by key. A driver that
// renames one, or reports it as a string, would leave its metric silently
// empty, so this builds the kinds of listener (without running them) and asks
// the collector what it exports from each one's real detail. Every listener is
// held to every event, except that the events only datagram listeners have are
// asked of those alone.
func TestListenerDetailKeysAreNumeric(t *testing.T) {
	const (
		events      = "sentinel_ingest_listener_events_total"
		connections = "sentinel_ingest_listener_connections"
		senders     = "sentinel_ingest_listener_senders"
	)
	// exported counts the series of one family the collector builds from a
	// feed whose driver reported detail; it, not this test, decides what is a number.
	exported := func(family string, detail map[string]any) int {
		src := fixedListeners{{FeedID: "f", Driver: "d", Detail: detail}}
		return testutil.CollectAndCount(metrics.NewListenerCollector(src), family)
	}
	for _, tc := range []struct {
		name, driverID, config string
		datagrams              bool // a datagram listener: no connections to count, and the datagram-only keys
		tls                    bool // a listener with TLS: the TLS-only keys
	}{
		{"tcp listen", "tcp", `{"mode":"listen","port":6000,"address":"127.0.0.1"}`, false, false},
		{"udp", "udp", `{"port":10110,"address":"127.0.0.1"}`, true, false},
		{"stream tcpserver", "stream", `{"transport":{"kind":"tcpserver","address":"127.0.0.1:6001"},"framing":"line","decoder":{"name":"nmea-ais"}}`, false, false},
		{"stream udp", "stream", `{"transport":{"kind":"udp","address":"127.0.0.1:6002"},"framing":"line","decoder":{"name":"nmea-ais"}}`, true, false},
		{"tcp listen with TLS", "tcp", `{"mode":"listen","port":6003,"address":"127.0.0.1","tls":{"enabled":true}}`, false, true},
		{"stream tcpserver with TLS", "stream", `{"transport":{"kind":"tcpserver","address":"127.0.0.1:6004","tls":{"enabled":true}},"framing":"line","decoder":{"name":"nmea-ais"}}`, false, true},
	} {
		spec := driver.Spec{FeedID: "f", Driver: tc.driverID, Config: json.RawMessage(tc.config)}
		if tc.tls {
			pair := transporttest.NewCA(t).Issue(t, "localhost")
			spec.Secrets = map[string]string{"tlsCert": string(pair.CertPEM), "tlsKey": string(pair.KeyPEM)}
		}
		drv, err := driver.New(spec, driver.Deps{})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		detail := drv.Health().Detail

		for _, key := range metrics.ListenerEventKeys() {
			if !tc.datagrams && slices.Contains(datagramOnlyDetailKeys, key) {
				continue // asked below not to be there
			}
			if !tc.tls && slices.Contains(tlsOnlyDetailKeys, key) {
				continue // asked below not to be there
			}
			if exported(events, map[string]any{key: detail[key]}) != 1 {
				t.Errorf("%s: Health().Detail[%q] = %#v (%T), which the listener collector does not export as an event", tc.name, key, detail[key], detail[key])
			}
		}
		for _, key := range datagramOnlyDetailKeys {
			if _, has := detail[key]; has != tc.datagrams {
				t.Errorf("%s: Health().Detail has %q = %v; only a datagram listener reports it (this one is a datagram listener: %v)", tc.name, key, has, tc.datagrams)
			}
		}
		for _, key := range tlsOnlyDetailKeys {
			if _, has := detail[key]; has != tc.tls {
				t.Errorf("%s: Health().Detail has %q = %v; only a listener with TLS reports it (this one has TLS: %v)", tc.name, key, has, tc.tls)
			}
		}
		// The certificate expiry is text, not a count: exempt from the numeric
		// guard above by name, and held to being a UTC time the client and the
		// collector can read instead.
		if tc.tls {
			text, _ := detail["tlsCertNotAfter"].(string)
			if when, err := time.Parse(time.RFC3339, text); err != nil || when.Location() != time.UTC {
				t.Errorf("%s: Health().Detail[tlsCertNotAfter] = %#v, want RFC 3339 text in UTC", tc.name, detail["tlsCertNotAfter"])
			}
			if got := exported("sentinel_ingest_listener_tls_cert_expiry_timestamp_seconds", map[string]any{"tlsCertNotAfter": detail["tlsCertNotAfter"]}); got != 1 {
				t.Errorf("%s: the collector exports %d expiry series from Health().Detail[tlsCertNotAfter], want 1", tc.name, got)
			}
			if n, ok := detail["tlsReloads"].(int64); !ok || n != 0 {
				t.Errorf("%s: Health().Detail[tlsReloads] = %#v, want an int64 0", tc.name, detail["tlsReloads"])
			}
		}
		// A size is a number the client reads and no event of the collector's: it
		// is reported as one, and the collector exports no series for it.
		if tc.datagrams {
			if _, ok := detail["receiveBufferEffectiveBytes"].(int64); !ok {
				t.Errorf("%s: Health().Detail[receiveBufferEffectiveBytes] = %#v (%T), want an int64", tc.name, detail["receiveBufferEffectiveBytes"], detail["receiveBufferEffectiveBytes"])
			}
			if got := exported(events, map[string]any{"receiveBufferEffectiveBytes": detail["receiveBufferEffectiveBytes"]}); got != 0 {
				t.Errorf("%s: the collector exported %d event series from receiveBufferEffectiveBytes, a size", tc.name, got)
			}
		}
		if exported(senders, map[string]any{"distinctSenders": detail["distinctSenders"]}) != 1 {
			t.Errorf("%s: Health().Detail[distinctSenders] = %#v (%T), which the listener collector does not export", tc.name, detail["distinctSenders"], detail["distinctSenders"])
		}
		// A datagram listener has no connections to count; it must say so in
		// the way the collector looks for.
		want := 1
		if tc.datagrams {
			want = 0
		}
		if got := exported(connections, map[string]any{"transport": detail["transport"], "connections": detail["connections"]}); got != want {
			t.Errorf("%s: %d connections series from transport %#v and connections %#v, want %d", tc.name, got, detail["transport"], detail["connections"], want)
		}
	}
}
