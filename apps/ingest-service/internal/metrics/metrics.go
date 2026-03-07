package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds all Prometheus metric collectors for the ingest service.
type Metrics struct {
	MessagesReceived    *prometheus.CounterVec
	MessagesProcessed   *prometheus.CounterVec
	MessagesFailed      *prometheus.CounterVec
	ProcessingDuration  prometheus.Histogram
	KafkaProduceDuration prometheus.Histogram
	ActiveConnections   *prometheus.GaugeVec
	BatchSize           prometheus.Histogram
	PipelineQueueDepth  prometheus.Gauge
	FeedLastSuccess     *prometheus.GaugeVec
	FeedEntityCount     *prometheus.GaugeVec
	FeedErrorsTotal     *prometheus.CounterVec
}

// New creates and registers all Prometheus metrics for the ingest service.
func New() *Metrics {
	return &Metrics{
		MessagesReceived: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "sentinel",
				Subsystem: "ingest",
				Name:      "messages_received_total",
				Help:      "Total number of messages received from sensor sources.",
			},
			[]string{"source_type"},
		),
		MessagesProcessed: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "sentinel",
				Subsystem: "ingest",
				Name:      "messages_processed_total",
				Help:      "Total number of messages successfully processed and normalized.",
			},
			[]string{"entity_type"},
		),
		MessagesFailed: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "sentinel",
				Subsystem: "ingest",
				Name:      "messages_failed_total",
				Help:      "Total number of messages that failed processing.",
			},
			[]string{"source_type", "error_type"},
		),
		ProcessingDuration: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "sentinel",
				Subsystem: "ingest",
				Name:      "processing_duration_seconds",
				Help:      "Time spent processing individual messages from receipt to normalized output.",
				Buckets:   prometheus.ExponentialBuckets(0.0001, 2, 15),
			},
		),
		KafkaProduceDuration: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "sentinel",
				Subsystem: "ingest",
				Name:      "kafka_produce_duration_seconds",
				Help:      "Time spent producing batches to Kafka.",
				Buckets:   prometheus.ExponentialBuckets(0.001, 2, 12),
			},
		),
		ActiveConnections: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "sentinel",
				Subsystem: "ingest",
				Name:      "active_connections",
				Help:      "Number of currently active source connections.",
			},
			[]string{"source_type"},
		),
		BatchSize: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "sentinel",
				Subsystem: "ingest",
				Name:      "batch_size",
				Help:      "Number of messages per Kafka produce batch.",
				Buckets:   prometheus.ExponentialBuckets(1, 2, 12),
			},
		),
		PipelineQueueDepth: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "sentinel",
				Subsystem: "ingest",
				Name:      "pipeline_queue_depth",
				Help:      "Current number of messages waiting in the pipeline input queue.",
			},
		),
		FeedLastSuccess: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "sentinel",
				Subsystem: "ingest",
				Name:      "feed_last_success_timestamp",
				Help:      "Unix timestamp of the last successful poll for an external feed.",
			},
			[]string{"feed"},
		),
		FeedEntityCount: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "sentinel",
				Subsystem: "ingest",
				Name:      "feed_entities_count",
				Help:      "Number of entities returned by the last successful poll.",
			},
			[]string{"feed"},
		),
		FeedErrorsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "sentinel",
				Subsystem: "ingest",
				Name:      "feed_errors_total",
				Help:      "Total number of poll errors for an external feed.",
			},
			[]string{"feed"},
		),
	}
}
