package metrics

import "github.com/prometheus/client_golang/prometheus"

// ListenerStat is one listening feed (a TCP or UDP listener) as its driver
// reports it in Health().Detail.
type ListenerStat struct {
	FeedID, Driver string
	Detail         map[string]any
}

// ListenerSource lists the listening feeds this replica runs, each with the
// health detail its driver reports now. The feed supervisor implements it.
type ListenerSource interface {
	ListenerStats() []ListenerStat
}

// listenerEvents is the one table of listener counters exported as
// sentinel_ingest_listener_events_total{event}: the key the driver reports a
// counter under in its health detail, and the event label it is exported
// under. A counter is exported by adding a row.
//
// malformed has no row: the stream driver counts it exactly when it
// dead-letters a frame, which is what decode_errors_total counts.
var listenerEvents = []struct{ key, event string }{
	{"accepted", "accepted"},                     // frames taken in
	{"rejected", "rejected"},                     // senders outside the allowlist
	{"refused", "refused"},                       // connections over maxConnections
	{"rateLimited", "rate_limited"},              // frames over maxMessagesPerSec, the limit all senders share
	{"oversize", "oversize"},                     // frames or datagrams over maxMessageBytes
	{"tlsFailed", "tls_failed"},                  // failed handshakes, and TLS clients on a plain listener
	{"senderRateLimited", "sender_rate_limited"}, // frames over one sender's maxMessagesPerSecPerSender
}

// ListenerEventKeys lists the health detail keys the collector exports as
// events, so a test can check that the drivers still report each of them.
func ListenerEventKeys() []string {
	keys := make([]string, len(listenerEvents))
	for i, e := range listenerEvents {
		keys[i] = e.key
	}
	return keys
}

// listenerCollector exports what listening drivers already count in their
// health detail (which the supervisor also writes to feed_status), read when
// Prometheus scrapes. Its series therefore exist only while the feed runs on
// this replica, and the counters start again at zero when the feed restarts,
// which rate() copes with. The sender is never a label: a datagram sender is
// cheap to spoof, so only the count of distinct senders is exported.
type listenerCollector struct {
	src                          ListenerSource
	events, connections, senders *prometheus.Desc
}

// NewListenerCollector exports, for each listening feed src lists:
//   - sentinel_ingest_listener_events_total{feed_id,driver,event}, a counter
//     for each row of the event table the driver reports as a number;
//   - sentinel_ingest_listener_connections{feed_id,driver}, a gauge, except
//     for a UDP listener, which has no connections;
//   - sentinel_ingest_listener_senders{feed_id,driver}, a gauge of the
//     distinct senders.
func NewListenerCollector(src ListenerSource) prometheus.Collector {
	desc := func(name, help string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName("sentinel", "ingest", name), help, labels, nil)
	}
	return &listenerCollector{
		src:         src,
		events:      desc("listener_events_total", "Frames a listening feed accepted and the connections, datagrams and frames it turned away, per feed and event.", "feed_id", "driver", "event"),
		connections: desc("listener_connections", "Connections currently open on a TCP listening feed, per feed.", "feed_id", "driver"),
		senders:     desc("listener_senders", "Distinct senders connected to a listening feed or heard from in the last 10 minutes, per feed.", "feed_id", "driver"),
	}
}

// Describe sends every descriptor, which makes this a checked collector: the
// registry refuses a name another collector already has, and a pedantic one
// rejects a metric that was not described.
func (c *listenerCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.events
	ch <- c.connections
	ch <- c.senders
}

// Collect may run alongside itself and alongside the supervisor's reconcile;
// it keeps nothing between scrapes.
func (c *listenerCollector) Collect(ch chan<- prometheus.Metric) {
	for _, st := range c.src.ListenerStats() {
		for _, e := range listenerEvents {
			if v, ok := number(st.Detail[e.key]); ok {
				sample(ch, c.events, prometheus.CounterValue, v, st.FeedID, st.Driver, e.event)
			}
		}
		// A datagram listener reports 0 connections; as a series that would
		// read as "nobody is connected" about a listener nobody connects to.
		if st.Detail["transport"] != "udp" {
			if v, ok := number(st.Detail["connections"]); ok {
				sample(ch, c.connections, prometheus.GaugeValue, v, st.FeedID, st.Driver)
			}
		}
		if v, ok := number(st.Detail["distinctSenders"]); ok {
			sample(ch, c.senders, prometheus.GaugeValue, v, st.FeedID, st.Driver)
		}
	}
}

// sample sends one value. The registry runs Collect on a goroutine of its own,
// where a panic would end the process, so a sample that cannot be built is sent
// as an invalid metric, which fails the scrape with the reason.
func sample(ch chan<- prometheus.Metric, desc *prometheus.Desc, kind prometheus.ValueType, v float64, labels ...string) {
	m, err := prometheus.NewConstMetric(desc, kind, v, labels...)
	if err != nil {
		m = prometheus.NewInvalidMetric(desc, err)
	}
	ch <- m
}

// number reads a count out of a health detail: the types the drivers and the
// JSON decoder produce. Anything else is not a number, and exports nothing.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}
