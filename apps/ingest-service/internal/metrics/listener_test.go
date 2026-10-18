package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// listeners is a fixed ListenerSource.
type listeners []ListenerStat

func (l listeners) ListenerStats() []ListenerStat { return l }

// One TCP and one UDP listener as their drivers report them. The counters are
// int64 and the connection and sender counts int, as the transports render
// them; the UDP feed's values come in the other number types the collector
// reads, and both feeds carry detail it must not export.
var exposedListeners = listeners{
	{FeedID: "gate", Driver: "tcp", Detail: map[string]any{
		"transport": "tcpserver", "connections": 2, "distinctSenders": 3,
		"accepted": int64(120), "rejected": int64(3), "refused": int64(1),
		"rateLimited": int64(7), "oversize": int64(2), "tlsFailed": int64(4), "senderRateLimited": int64(6),
		"malformed":  int64(9),             // the same count as decode_errors_total
		"senders":    []any{"a", "b", "c"}, // not a number
		"format":     "sentinel",           // not a number
		"newCounter": int64(5),             // a counter the collector has no row for
	}},
	{FeedID: "pier", Driver: "udp", Detail: map[string]any{
		"transport": "udp", "connections": 0, "distinctSenders": uint64(12),
		"accepted": uint64(500), "rejected": int32(2), "refused": 0, "rateLimited": float64(9), "senderRateLimited": uint64(8),
		"oversize": "many", // a number in the wrong type exports nothing
		// tlsFailed is absent: no series rather than a zero.
	}},
	{FeedID: "quiet", Driver: "stream"}, // a driver with no detail yet
}

func TestListenerCollectorExposition(t *testing.T) {
	const want = `
# HELP sentinel_ingest_listener_connections Connections currently open on a TCP listening feed, per feed.
# TYPE sentinel_ingest_listener_connections gauge
sentinel_ingest_listener_connections{driver="tcp",feed_id="gate"} 2
# HELP sentinel_ingest_listener_events_total Frames a listening feed accepted and the connections, datagrams and frames it turned away, per feed and event.
# TYPE sentinel_ingest_listener_events_total counter
sentinel_ingest_listener_events_total{driver="tcp",event="accepted",feed_id="gate"} 120
sentinel_ingest_listener_events_total{driver="tcp",event="oversize",feed_id="gate"} 2
sentinel_ingest_listener_events_total{driver="tcp",event="rate_limited",feed_id="gate"} 7
sentinel_ingest_listener_events_total{driver="tcp",event="refused",feed_id="gate"} 1
sentinel_ingest_listener_events_total{driver="tcp",event="rejected",feed_id="gate"} 3
sentinel_ingest_listener_events_total{driver="tcp",event="sender_rate_limited",feed_id="gate"} 6
sentinel_ingest_listener_events_total{driver="tcp",event="tls_failed",feed_id="gate"} 4
sentinel_ingest_listener_events_total{driver="udp",event="accepted",feed_id="pier"} 500
sentinel_ingest_listener_events_total{driver="udp",event="rate_limited",feed_id="pier"} 9
sentinel_ingest_listener_events_total{driver="udp",event="refused",feed_id="pier"} 0
sentinel_ingest_listener_events_total{driver="udp",event="rejected",feed_id="pier"} 2
sentinel_ingest_listener_events_total{driver="udp",event="sender_rate_limited",feed_id="pier"} 8
# HELP sentinel_ingest_listener_senders Distinct senders connected to a listening feed or heard from in the last 10 minutes, per feed.
# TYPE sentinel_ingest_listener_senders gauge
sentinel_ingest_listener_senders{driver="tcp",feed_id="gate"} 3
sentinel_ingest_listener_senders{driver="udp",feed_id="pier"} 12
`
	// CollectAndCompare uses a pedantic registry, so this also fails when the
	// collector sends a metric it did not describe.
	if err := testutil.CollectAndCompare(NewListenerCollector(exposedListeners), strings.NewReader(want)); err != nil {
		t.Error(err)
	}
}

func TestListenerCollectorLint(t *testing.T) {
	// Every counter in the event table, so a row added later is linted too and
	// a duplicated event label fails the gather.
	detail := map[string]any{"transport": "tcpserver", "connections": 1, "distinctSenders": 2}
	for _, key := range ListenerEventKeys() {
		detail[key] = int64(1)
	}
	c := NewListenerCollector(listeners{{FeedID: "gate", Driver: "tcp", Detail: detail}})

	problems, err := testutil.CollectAndLint(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Errorf("%s: %s", p.Metric, p.Text)
	}
	// Nothing to lint is not a pass.
	if got, want := testutil.CollectAndCount(c), len(ListenerEventKeys())+2; got != want {
		t.Errorf("collected %d series, want %d: one per event, connections and senders", got, want)
	}
}

func TestListenerCollectorEmptySource(t *testing.T) {
	for name, src := range map[string]listeners{"nil": nil, "no listening feeds": {}} {
		c := NewListenerCollector(src)
		if got := testutil.CollectAndCount(c); got != 0 {
			t.Errorf("%s: collected %d series, want none", name, got)
		}
		// The registry accepts it and a scrape has nothing to say.
		reg := prometheus.NewPedanticRegistry()
		if err := reg.Register(c); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if families, err := reg.Gather(); err != nil || len(families) != 0 {
			t.Errorf("%s: gathered %d families, err %v; want neither", name, len(families), err)
		}
	}
}

// main registers the collector on the registry New fills, and MustRegister
// panics at startup on a name that registry already has.
func TestListenerCollectorRegistersBesideTheOtherMetrics(t *testing.T) {
	m := New(nil)
	if err := m.Registry.Register(NewListenerCollector(exposedListeners)); err != nil {
		t.Fatal(err)
	}
	m.FramesTotal.WithLabelValues("gate", "tcp").Inc()
	if _, err := m.Registry.Gather(); err != nil {
		t.Errorf("gathering both: %v", err)
	}
}
