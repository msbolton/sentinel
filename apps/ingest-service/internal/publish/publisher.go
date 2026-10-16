// Package publish hands observations to Kafka. It shards by the Kafka key
// so one object's reports leave in order, turns a full producer queue into
// backpressure instead of loss, and keeps a rate-limited dead-letter path
// for payloads nothing could decode.
package publish

import (
	"context"
	"hash/fnv"
	"strconv"
	"sync"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"go.uber.org/zap"
	"golang.org/x/time/rate"

	commonkafka "github.com/sentinel/go-common/kafka"

	kafkaproducer "github.com/sentinel/ingest-service/internal/kafka"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/wire"
)

// Producer is what the publisher needs from the Kafka producer.
type Producer interface {
	Produce(msg *kafka.Message) error
}

// Options tunes the publisher. Zero values take the defaults.
type Options struct {
	Topic           string        // observations.raw
	DeadLetterTopic string        // observations.dlq
	Shards          int           // 8
	Buffer          int           // observations queued per shard (1000)
	DLQRatePerSec   float64       // dead letters per feed per second (10)
	DLQBurst        int           // 20
	QueueFullWait   time.Duration // first retry delay on a full producer queue (10 ms)
	ReplicaID       string
	FencingToken    int64
}

func (o Options) withDefaults() Options {
	if o.Topic == "" {
		o.Topic = commonkafka.TopicObservationsRaw
	}
	if o.DeadLetterTopic == "" {
		o.DeadLetterTopic = commonkafka.TopicObservationsDLQ
	}
	if o.Shards <= 0 {
		o.Shards = 8
	}
	if o.Buffer <= 0 {
		o.Buffer = 1000
	}
	if o.DLQRatePerSec <= 0 {
		o.DLQRatePerSec = 10
	}
	if o.DLQBurst <= 0 {
		o.DLQBurst = 20
	}
	if o.QueueFullWait <= 0 {
		o.QueueFullWait = 10 * time.Millisecond
	}
	return o
}

// Publisher is safe for concurrent Emit calls.
type Publisher struct {
	producer Producer
	metrics  *metrics.Metrics
	logger   *zap.Logger
	opts     Options

	shards []chan *obs.Observation
	wg     sync.WaitGroup

	mu       sync.Mutex
	closed   bool
	limiters map[string]*rate.Limiter
}

// New creates a publisher; call Start before Emit.
func New(p Producer, m *metrics.Metrics, logger *zap.Logger, opts Options) *Publisher {
	if logger == nil {
		logger = zap.NewNop()
	}
	o := opts.withDefaults()
	pub := &Publisher{producer: p, metrics: m, logger: logger, opts: o, limiters: map[string]*rate.Limiter{}}
	pub.shards = make([]chan *obs.Observation, o.Shards)
	for i := range pub.shards {
		pub.shards[i] = make(chan *obs.Observation, o.Buffer)
	}
	return pub
}

// Start launches one goroutine per shard.
func (p *Publisher) Start() {
	for i, ch := range p.shards {
		p.wg.Add(1)
		go p.run(i, ch)
	}
}

// Emit queues an observation on the shard its key hashes to. It blocks
// while the shard is full, so pressure reaches the caller, and returns
// ctx.Err() if the caller gives up first.
func (p *Publisher) Emit(ctx context.Context, o *obs.Observation) error {
	if o.ReplicaID == "" {
		o.ReplicaID = p.opts.ReplicaID
	}
	if o.FencingToken == 0 {
		o.FencingToken = p.opts.FencingToken
	}
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return ErrClosed
	}
	ch := p.shards[shardFor(wire.Key(o), len(p.shards))]
	select {
	case ch <- o:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// DeadLetter sends an undecodable payload to the dead-letter topic, at
// most DLQRatePerSec per feed; excess is counted and dropped so a
// misconfigured feed cannot flood Kafka.
func (p *Publisher) DeadLetter(feedID, sourceType, decoder string, raw []byte, reason string, cause error) {
	if feedID == "" {
		feedID = "unknown"
	}
	if !p.limiter(feedID).Allow() {
		if p.metrics != nil {
			p.metrics.DeadLetterDropped.WithLabelValues(feedID).Inc()
		}
		return
	}
	headers := []kafka.Header{
		{Key: commonkafka.HeaderProducer, Value: []byte(wire.Producer)},
		{Key: commonkafka.HeaderFeedID, Value: []byte(feedID)},
		{Key: "source-type", Value: []byte(sourceType)},
		{Key: "decoder", Value: []byte(decoder)},
		{Key: "reason", Value: []byte(reason)},
		{Key: commonkafka.HeaderError, Value: []byte(errString(cause))},
		{Key: "received-at", Value: []byte(time.Now().UTC().Format(time.RFC3339Nano))},
	}
	topic := p.opts.DeadLetterTopic
	msg := &kafka.Message{
		TopicPartition: kafka.TopicPartition{Topic: &topic, Partition: kafka.PartitionAny},
		Key:            []byte(feedID),
		Value:          raw,
		Headers:        headers,
		Opaque:         feedID,
	}
	if err := p.producer.Produce(msg); err != nil {
		p.logger.Warn("dead letter not enqueued", zap.String("feed", feedID), zap.Error(err))
		return
	}
	if p.metrics != nil {
		p.metrics.DeadLetterTotal.WithLabelValues(feedID, reason).Inc()
	}
}

// Close stops accepting observations, drains every shard and returns.
func (p *Publisher) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()
	for _, ch := range p.shards {
		close(ch)
	}
	p.wg.Wait()
}

func (p *Publisher) run(shard int, ch <-chan *obs.Observation) {
	defer p.wg.Done()
	label := strconv.Itoa(shard)
	for o := range ch {
		if p.metrics != nil {
			p.metrics.PublisherShardDepth.WithLabelValues(label).Set(float64(len(ch)))
		}
		p.produce(o)
	}
}

func (p *Publisher) produce(o *obs.Observation) {
	value, err := wire.Marshal(o)
	if err != nil {
		p.logger.Error("observation did not serialise", zap.String("feed", o.FeedID), zap.Error(err))
		return
	}
	topic := p.opts.Topic
	msg := &kafka.Message{
		TopicPartition: kafka.TopicPartition{Topic: &topic, Partition: kafka.PartitionAny},
		Key:            []byte(wire.Key(o)),
		Value:          value,
		Opaque:         o.FeedID,
	}
	for k, v := range wire.Headers(o) {
		msg.Headers = append(msg.Headers, kafka.Header{Key: k, Value: []byte(v)})
	}

	wait := p.opts.QueueFullWait
	for {
		err := p.producer.Produce(msg)
		if err == nil {
			if p.metrics != nil {
				p.metrics.ObservationsPublished.WithLabelValues(o.FeedID).Inc()
			}
			return
		}
		if !kafkaproducer.IsQueueFull(err) {
			p.logger.Error("observation not enqueued", zap.String("feed", o.FeedID), zap.Error(err))
			if p.metrics != nil {
				p.metrics.ObservationsDeliveryFailed.WithLabelValues(o.FeedID).Inc()
			}
			return
		}
		// Full local queue: wait and retry. The shard stalls, its channel
		// fills, and Emit callers block, which is the backpressure we want.
		if p.metrics != nil {
			p.metrics.PublisherBlockedSeconds.Add(wait.Seconds())
		}
		time.Sleep(wait)
		if wait < 500*time.Millisecond {
			wait *= 2
		}
	}
}

func (p *Publisher) limiter(feedID string) *rate.Limiter {
	p.mu.Lock()
	defer p.mu.Unlock()
	l, ok := p.limiters[feedID]
	if !ok {
		l = rate.NewLimiter(rate.Limit(p.opts.DLQRatePerSec), p.opts.DLQBurst)
		p.limiters[feedID] = l
	}
	return l
}

func shardFor(key string, n int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % uint32(n))
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
