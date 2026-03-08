package ingest

import (
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/config"
	kafkaproducer "github.com/sentinel/ingest-service/internal/kafka"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/models"
)

// Pipeline is the core ingestion pipeline that receives raw messages from
// multiple sensor sources, parses and normalizes them, correlates entity
// tracks, batches the results, and produces them to Kafka.
type Pipeline struct {
	input         chan *models.IngestMessage
	output        chan *models.EntityPosition
	producer      *kafkaproducer.Producer
	logger        *zap.Logger
	metrics       *metrics.Metrics
	parser        *Parser
	correlator    *TrackCorrelator
	batchSize     int
	flushInterval time.Duration
	entityTopic   string
	workerCount   int
	workerWg      sync.WaitGroup
	wg            sync.WaitGroup
	stop          chan struct{}
}

// NewPipeline creates a new ingestion pipeline with the given configuration,
// Kafka producer, logger, and metrics collector.
func NewPipeline(cfg *config.Config, producer *kafkaproducer.Producer, logger *zap.Logger, m *metrics.Metrics) *Pipeline {
	return &Pipeline{
		input:         make(chan *models.IngestMessage, cfg.WorkerPoolSize*cfg.BatchSize),
		output:        make(chan *models.EntityPosition, cfg.WorkerPoolSize*cfg.BatchSize),
		producer:      producer,
		logger:        logger,
		metrics:       m,
		parser:        NewParser(),
		correlator:    NewTrackCorrelator(),
		batchSize:     cfg.BatchSize,
		flushInterval: time.Duration(cfg.FlushIntervalMs) * time.Millisecond,
		entityTopic:   cfg.KafkaEntityTopic,
		workerCount:   cfg.WorkerPoolSize,
		stop:          make(chan struct{}),
	}
}

// Input returns the pipeline's input channel where raw ingest messages
// should be sent by source listeners.
func (p *Pipeline) Input() chan<- *models.IngestMessage {
	return p.input
}

// Start launches the worker pool goroutines for parsing/normalization
// and the batcher goroutine for Kafka production.
func (p *Pipeline) Start() {
	p.logger.Info("starting ingestion pipeline",
		zap.Int("workers", p.workerCount),
		zap.Int("batch_size", p.batchSize),
		zap.Duration("flush_interval", p.flushInterval),
	)

	// Start worker pool for parsing and correlation.
	for i := 0; i < p.workerCount; i++ {
		p.workerWg.Add(1)
		go p.worker(i)
	}

	// Start batcher for Kafka production.
	p.wg.Add(1)
	go p.batcher()

	// Start queue depth reporter.
	p.wg.Add(1)
	go p.reportQueueDepth()
}

// Stop signals all pipeline goroutines to shut down and waits for them to finish.
func (p *Pipeline) Stop() {
	p.logger.Info("stopping ingestion pipeline")
	close(p.stop)
	close(p.input)

	// Wait for workers to finish processing, then close the output channel
	// so the batcher can drain remaining items and exit.
	p.workerWg.Wait()
	close(p.output)

	p.wg.Wait()
	p.logger.Info("ingestion pipeline stopped")
}

// worker is a goroutine that reads from the input channel, parses and normalizes
// each message, runs track correlation, and forwards the result to the output channel.
func (p *Pipeline) worker(id int) {
	defer p.workerWg.Done()

	p.logger.Debug("pipeline worker started", zap.Int("worker_id", id))

	for msg := range p.input {
		p.processMessage(msg)
	}

	p.logger.Debug("pipeline worker stopped", zap.Int("worker_id", id))
}

// processMessage handles a single ingest message through the full pipeline:
// parse, normalize, correlate, and forward to the output channel.
func (p *Pipeline) processMessage(msg *models.IngestMessage) {
	start := time.Now()

	p.metrics.MessagesReceived.WithLabelValues(msg.SourceType).Inc()

	// Parse the raw message into an entity position.
	var entity *models.EntityPosition
	var err error
	if msg.Format != "" {
		entity, err = p.parser.ParseFormat(msg.Format, msg.SourceType, msg.Payload)
	} else {
		entity, err = p.parser.ParseGeneric(msg.SourceType, msg.Payload)
	}
	if err != nil {
		p.metrics.MessagesFailed.WithLabelValues(msg.SourceType, "parse_error").Inc()
		p.logger.Debug("failed to parse message",
			zap.String("source_type", msg.SourceType),
			zap.String("source_addr", msg.SourceAddr),
			zap.Error(err),
		)
		return
	}

	// Propagate feed ID from source to entity.
	entity.FeedID = msg.FeedID

	// Correlate with existing tracks.
	correlated, isDuplicate := p.correlator.Correlate(entity)
	if isDuplicate {
		p.metrics.MessagesFailed.WithLabelValues(msg.SourceType, "duplicate").Inc()
		return
	}

	// Forward to the output channel for batching.
	select {
	case p.output <- correlated:
		p.metrics.MessagesProcessed.WithLabelValues(correlated.EntityType).Inc()
	case <-p.stop:
		return
	default:
		// Output channel full; drop the message and record the failure.
		p.metrics.MessagesFailed.WithLabelValues(msg.SourceType, "backpressure").Inc()
		p.logger.Warn("output channel full, dropping message",
			zap.String("entity_id", correlated.EntityID),
		)
	}

	p.metrics.ProcessingDuration.Observe(time.Since(start).Seconds())
}

// batcher accumulates entity positions from the output channel and produces
// them to Kafka in batches, flushing either when the batch is full or when
// the flush interval elapses.
func (p *Pipeline) batcher() {
	defer p.wg.Done()

	batch := make([]*models.EntityPosition, 0, p.batchSize)
	ticker := time.NewTicker(p.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case entity, ok := <-p.output:
			if !ok {
				// Channel closed; flush remaining.
				p.flush(batch)
				return
			}
			batch = append(batch, entity)
			if len(batch) >= p.batchSize {
				p.flush(batch)
				batch = make([]*models.EntityPosition, 0, p.batchSize)
			}

		case <-ticker.C:
			if len(batch) > 0 {
				p.flush(batch)
				batch = make([]*models.EntityPosition, 0, p.batchSize)
			}

		case <-p.stop:
			// Drain remaining messages from the output channel.
			for entity := range p.output {
				batch = append(batch, entity)
				if len(batch) >= p.batchSize {
					p.flush(batch)
					batch = make([]*models.EntityPosition, 0, p.batchSize)
				}
			}
			p.flush(batch)
			return
		}
	}
}

// flush produces a batch of entity positions to Kafka.
func (p *Pipeline) flush(batch []*models.EntityPosition) {
	if len(batch) == 0 {
		return
	}

	if err := p.producer.ProduceBatch(p.entityTopic, batch); err != nil {
		p.logger.Error("failed to produce batch to kafka",
			zap.Int("batch_size", len(batch)),
			zap.Error(err),
		)
	} else {
		p.logger.Debug("flushed batch to kafka",
			zap.Int("batch_size", len(batch)),
			zap.String("topic", p.entityTopic),
		)
	}
}

// reportQueueDepth periodically updates the pipeline queue depth gauge metric.
func (p *Pipeline) reportQueueDepth() {
	defer p.wg.Done()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.metrics.PipelineQueueDepth.Set(float64(len(p.input)))
		case <-p.stop:
			return
		}
	}
}
