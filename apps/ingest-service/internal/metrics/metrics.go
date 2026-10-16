// Package metrics holds the ingest service's Prometheus collectors on an
// injected registry, so tests can build as many as they like.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds every collector. Labels: feed_id and driver identify a
// feed; reason classifies drops, skips and dead letters.
type Metrics struct {
	Registry *prometheus.Registry

	FramesTotal        *prometheus.CounterVec // feed_id, driver
	ObservationsTotal  *prometheus.CounterVec // feed_id, driver
	DecodeErrorsTotal  *prometheus.CounterVec // feed_id, driver, reason (parse_error|panic)
	SkippedTotal       *prometheus.CounterVec // feed_id, driver, reason
	FramesDroppedTotal *prometheus.CounterVec // feed_id, reason
	NackedTotal        *prometheus.CounterVec // feed_id
	BlockedSeconds     *prometheus.CounterVec // feed_id
	DuplicatesTotal    *prometheus.CounterVec // feed_id

	FeedsRunning    *prometheus.GaugeVec   // driver
	FeedLastSuccess *prometheus.GaugeVec   // feed_id
	FeedEntityCount *prometheus.GaugeVec   // feed_id
	FeedErrorsTotal *prometheus.CounterVec // feed_id
	FeedRestarts    *prometheus.CounterVec // feed_id

	ObservationsPublished      *prometheus.CounterVec // feed_id
	ObservationsDeliveryFailed *prometheus.CounterVec // feed_id
	DeadLetterTotal            *prometheus.CounterVec // feed_id, reason
	DeadLetterDropped          *prometheus.CounterVec // feed_id
	PublisherShardDepth        *prometheus.GaugeVec   // shard
	PublisherBlockedSeconds    prometheus.Counter
}

// New registers every collector on reg (a fresh registry when nil).
func New(reg *prometheus.Registry) *Metrics {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	counter := func(name, help string, labels ...string) *prometheus.CounterVec {
		c := prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "sentinel", Subsystem: "ingest", Name: name, Help: help}, labels)
		reg.MustRegister(c)
		return c
	}
	gauge := func(name, help string, labels ...string) *prometheus.GaugeVec {
		g := prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "sentinel", Subsystem: "ingest", Name: name, Help: help}, labels)
		reg.MustRegister(g)
		return g
	}
	blocked := prometheus.NewCounter(prometheus.CounterOpts{Namespace: "sentinel", Subsystem: "ingest", Name: "publisher_blocked_seconds_total", Help: "Time publisher shards spent waiting on a full Kafka queue."})
	reg.MustRegister(blocked)
	return &Metrics{
		Registry:                   reg,
		FramesTotal:                counter("frames_total", "Frames received from transports or records returned by polls, per feed.", "feed_id", "driver"),
		ObservationsTotal:          counter("observations_total", "Observations emitted by drivers after deduplication, per feed.", "feed_id", "driver"),
		DecodeErrorsTotal:          counter("decode_errors_total", "Frames that failed to decode and were dead-lettered, per feed and reason.", "feed_id", "driver", "reason"),
		SkippedTotal:               counter("skipped_total", "Frames that produced no observation, per feed and reason.", "feed_id", "driver", "reason"),
		FramesDroppedTotal:         counter("frames_dropped_total", "Frames dropped by the backpressure policy, per feed and reason.", "feed_id", "reason"),
		NackedTotal:                counter("nacked_total", "Messages negatively acknowledged to the broker under backpressure, per feed.", "feed_id"),
		BlockedSeconds:             counter("blocked_seconds_total", "Time transports spent blocked on backpressure, per feed.", "feed_id"),
		DuplicatesTotal:            counter("duplicates_total", "Observations suppressed as repeated fixes, per feed.", "feed_id"),
		FeedsRunning:               gauge("feeds_running", "Feeds currently running on this replica, per driver.", "driver"),
		FeedLastSuccess:            gauge("feed_last_success_timestamp", "Unix time of the last successful poll or received report, per feed.", "feed_id"),
		FeedEntityCount:            gauge("feed_entities_count", "Observations produced by the last poll, per feed.", "feed_id"),
		FeedErrorsTotal:            counter("feed_errors_total", "Poll, connection and start errors, per feed.", "feed_id"),
		FeedRestarts:               counter("feed_restarts_total", "Driver restarts after a failure, per feed.", "feed_id"),
		ObservationsPublished:      counter("observations_published_total", "Observations handed to the Kafka producer, per feed.", "feed_id"),
		ObservationsDeliveryFailed: counter("observations_delivery_failed_total", "Observations Kafka reported as undelivered, per feed.", "feed_id"),
		DeadLetterTotal:            counter("deadletter_total", "Payloads sent to the dead-letter topic, per feed and reason.", "feed_id", "reason"),
		DeadLetterDropped:          counter("deadletter_dropped_total", "Dead letters dropped by the per-feed rate limit.", "feed_id"),
		PublisherShardDepth:        gauge("publisher_shard_depth", "Observations waiting in each publisher shard.", "shard"),
		PublisherBlockedSeconds:    blocked,
	}
}
