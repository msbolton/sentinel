package ingest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/config"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/models"
	"github.com/sentinel/ingest-service/internal/obs"
)

// LegacyProducer sends the JSON entity positions older consumers still read.
type LegacyProducer interface {
	ProduceBatch(topic string, messages []*models.EntityPosition) error
}

// ObservationPublisher receives every decoded report and the payloads that
// could not be decoded.
type ObservationPublisher interface {
	Emit(ctx context.Context, o *obs.Observation) error
	DeadLetter(feedID, sourceType, decoder string, raw []byte, reason string, cause error)
}

// Pipeline receives raw messages from the source listeners, parses and
// normalises them, correlates tracks, and publishes each report as an
// observation. While a legacy topic is configured it also batches the JSON
// entity positions to it.
type Pipeline struct {
	input         chan *models.IngestMessage
	output        chan *models.EntityPosition
	legacy        LegacyProducer
	publisher     ObservationPublisher
	logger        *zap.Logger
	metrics       *metrics.Metrics
	parser        *Parser
	correlator    *TrackCorrelator
	batchSize     int
	flushInterval time.Duration
	legacyTopic   string
	rawMax        int
	workerCount   int
	workerWg      sync.WaitGroup
	wg            sync.WaitGroup
	stop          chan struct{}
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewPipeline creates a pipeline. legacy may be nil when cfg.KafkaLegacyTopic
// is empty.
func NewPipeline(cfg *config.Config, legacy LegacyProducer, publisher ObservationPublisher, logger *zap.Logger, m *metrics.Metrics) *Pipeline {
	ctx, cancel := context.WithCancel(context.Background())
	return &Pipeline{
		input:         make(chan *models.IngestMessage, cfg.WorkerPoolSize*cfg.BatchSize),
		output:        make(chan *models.EntityPosition, cfg.WorkerPoolSize*cfg.BatchSize),
		legacy:        legacy,
		publisher:     publisher,
		logger:        logger,
		metrics:       m,
		parser:        NewParser(),
		correlator:    NewTrackCorrelator(),
		batchSize:     cfg.BatchSize,
		flushInterval: time.Duration(cfg.FlushIntervalMs) * time.Millisecond,
		legacyTopic:   cfg.KafkaLegacyTopic,
		rawMax:        cfg.RawPayloadMaxBytes,
		workerCount:   cfg.WorkerPoolSize,
		stop:          make(chan struct{}),
		ctx:           ctx,
		cancel:        cancel,
	}
}

// Input returns the channel source listeners send raw messages to.
func (p *Pipeline) Input() chan<- *models.IngestMessage {
	return p.input
}

// Start launches the worker pool and, when a legacy topic is configured,
// the JSON batcher.
func (p *Pipeline) Start() {
	p.logger.Info("starting ingestion pipeline",
		zap.Int("workers", p.workerCount),
		zap.Int("batch_size", p.batchSize),
		zap.Duration("flush_interval", p.flushInterval),
		zap.String("legacy_topic", p.legacyTopic),
	)

	for i := 0; i < p.workerCount; i++ {
		p.workerWg.Add(1)
		go p.worker(i)
	}

	p.wg.Add(1)
	go p.batcher()

	p.wg.Add(1)
	go p.reportQueueDepth()
}

// Stop closes the input, waits for the workers to drain it, then flushes
// the legacy batcher. The observation publisher is closed by the caller
// afterwards, so every observation the workers emitted is already queued.
func (p *Pipeline) Stop() {
	p.logger.Info("stopping ingestion pipeline")
	close(p.stop)
	close(p.input)

	p.workerWg.Wait()
	p.cancel()
	close(p.output)

	p.wg.Wait()
	p.logger.Info("ingestion pipeline stopped")
}

func (p *Pipeline) worker(id int) {
	defer p.workerWg.Done()

	p.logger.Debug("pipeline worker started", zap.Int("worker_id", id))

	for msg := range p.input {
		p.processMessage(msg)
	}

	p.logger.Debug("pipeline worker stopped", zap.Int("worker_id", id))
}

// processMessage parses, correlates and publishes one raw message. A
// parser panic is recovered and dead-lettered so one bad payload cannot
// take the service down.
func (p *Pipeline) processMessage(msg *models.IngestMessage) {
	start := time.Now()
	p.metrics.MessagesReceived.WithLabelValues(msg.SourceType).Inc()

	entity, err := p.parse(msg)
	if err != nil {
		var pe *panicError
		reason := "parse_error"
		if errors.As(err, &pe) {
			reason = "panic"
			p.metrics.DecodePanics.WithLabelValues(msg.SourceType).Inc()
			p.logger.Error("parser panic recovered",
				zap.String("source_type", msg.SourceType),
				zap.String("feed_id", msg.FeedID),
				zap.Any("panic", pe.value),
			)
		} else {
			p.logger.Debug("failed to parse message",
				zap.String("source_type", msg.SourceType),
				zap.String("source_addr", msg.SourceAddr),
				zap.Error(err),
			)
		}
		p.metrics.MessagesFailed.WithLabelValues(msg.SourceType, reason).Inc()
		if p.publisher != nil {
			p.publisher.DeadLetter(msg.FeedID, msg.SourceType, msg.Format, msg.Payload, reason, err)
		}
		return
	}

	if msg.FeedID != "" {
		entity.FeedID = msg.FeedID
	}

	// A report without a fix (an AIS static report) is published as an
	// observation but neither moves the track nor reaches legacy consumers.
	if entity.NoPosition {
		p.metrics.FramesSkipped.WithLabelValues(msg.SourceType, "no_position").Inc()
		p.emit(entity, msg)
		return
	}

	correlated, isDuplicate := p.correlator.Correlate(entity)
	if isDuplicate {
		p.metrics.FramesSkipped.WithLabelValues(msg.SourceType, "duplicate").Inc()
		return
	}

	p.emit(correlated, msg)
	p.metrics.MessagesProcessed.WithLabelValues(correlated.EntityType).Inc()

	if p.legacyTopic != "" {
		select {
		case p.output <- correlated:
		case <-p.stop:
			return
		default:
			p.metrics.MessagesFailed.WithLabelValues(msg.SourceType, "backpressure").Inc()
			p.logger.Warn("legacy output channel full, dropping message",
				zap.String("entity_id", correlated.EntityID),
			)
		}
	}

	p.metrics.ProcessingDuration.Observe(time.Since(start).Seconds())
}

// emit converts and publishes one report. Emit blocks while the publisher
// is saturated; that stall propagates to the input channel and on to the
// listeners' own backpressure policies.
func (p *Pipeline) emit(entity *models.EntityPosition, msg *models.IngestMessage) {
	if p.publisher == nil {
		return
	}
	o := obs.FromEntityPosition(entity, msg, p.rawMax)
	if err := p.publisher.Emit(p.ctx, o); err != nil && !errors.Is(err, context.Canceled) {
		p.metrics.MessagesFailed.WithLabelValues(msg.SourceType, "publish").Inc()
		p.logger.Warn("observation not published", zap.String("feed_id", o.FeedID), zap.Error(err))
	}
}

type panicError struct{ value any }

func (e *panicError) Error() string { return fmt.Sprintf("parser panic: %v", e.value) }

func (p *Pipeline) parse(msg *models.IngestMessage) (entity *models.EntityPosition, err error) {
	defer func() {
		if r := recover(); r != nil {
			entity, err = nil, &panicError{value: r}
		}
	}()
	if msg.Format != "" {
		return p.parser.ParseFormat(msg.Format, msg.SourceType, msg.Payload)
	}
	return p.parser.ParseGeneric(msg.SourceType, msg.Payload)
}

// batcher accumulates legacy entity positions and produces them in batches.
func (p *Pipeline) batcher() {
	defer p.wg.Done()

	batch := make([]*models.EntityPosition, 0, p.batchSize)
	ticker := time.NewTicker(p.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case entity, ok := <-p.output:
			if !ok {
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

func (p *Pipeline) flush(batch []*models.EntityPosition) {
	if len(batch) == 0 || p.legacy == nil || p.legacyTopic == "" {
		return
	}
	if err := p.legacy.ProduceBatch(p.legacyTopic, batch); err != nil {
		p.logger.Error("failed to produce legacy batch to kafka",
			zap.Int("batch_size", len(batch)),
			zap.Error(err),
		)
	}
}

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
