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
	"sync/atomic"
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

	// pending is the observations Emit has taken that no shard has handed to the
	// producer yet: the ones queued and the ones in a shard's hands. It is what
	// CloseWithin gives up on if it runs out of time.
	pending atomic.Int64
	// done is closed when the publisher starts closing: Emit refuses from then
	// on, and an Emit blocked on a full shard is let go. The shard channels are
	// never closed, so no Emit can send on a closed one.
	done chan struct{}
	// gate is read-locked by every Emit while it queues an observation and
	// locked once by the close, after done, so that when the close gets it no
	// Emit is queueing and none will: what is in the shards is all there will be.
	gate sync.RWMutex
	// drain is closed once that is so, and tells the shards to hand over what is
	// queued and leave.
	drain chan struct{}
	// abandon is closed when CloseWithin gives up, and stops the shards.
	abandon chan struct{}

	mu       sync.Mutex
	closed   bool
	limiters map[string]*rate.Limiter
}

// abandonGrace is how long CloseWithin, having given up, waits for the shards to
// notice and stop calling the producer, so that the caller can close it. A shard
// notices at once unless it is inside a call to the producer that does not
// return, which nothing can interrupt.
const abandonGrace = 100 * time.Millisecond

// New creates a publisher; call Start before Emit.
func New(p Producer, m *metrics.Metrics, logger *zap.Logger, opts Options) *Publisher {
	if logger == nil {
		logger = zap.NewNop()
	}
	o := opts.withDefaults()
	pub := &Publisher{producer: p, metrics: m, logger: logger, opts: o, done: make(chan struct{}), drain: make(chan struct{}), abandon: make(chan struct{}), limiters: map[string]*rate.Limiter{}}
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
// ctx.Err() if the caller gives up first, or ErrClosed if the publisher closes
// first (an observation Emit returned nil for is handed to the producer by the
// close, or counted among those it abandoned).
func (p *Publisher) Emit(ctx context.Context, o *obs.Observation) error {
	if o.ReplicaID == "" {
		o.ReplicaID = p.opts.ReplicaID
	}
	if o.FencingToken == 0 {
		o.FencingToken = p.opts.FencingToken
	}
	p.gate.RLock()
	defer p.gate.RUnlock()
	select {
	case <-p.done:
		return ErrClosed
	default:
	}
	ch := p.shards[shardFor(wire.Key(o), len(p.shards))]
	p.pending.Add(1)
	select {
	case ch <- o:
		return nil
	case <-p.done:
		p.pending.Add(-1)
		return ErrClosed
	case <-ctx.Done():
		p.pending.Add(-1)
		return ctx.Err()
	}
}

// DeadLetterRawMax is the most of a payload a dead letter carries. A stream
// frame can be 1 MiB, which is over librdkafka's message.max.bytes, so the
// letter would be refused whole; a longer payload is cut and says so (the
// raw-truncated and raw-size headers).
const DeadLetterRawMax = 64 << 10

// Reasons a dead letter is counted in deadletter_dropped_total.
const (
	dropRateLimited   = "rate_limited"
	dropClosing       = "closing"
	dropProduceFailed = "produce_failed"
)

// DeadLetter sends an undecodable payload to the dead-letter topic, with the
// decoder that refused it and the sender it came from when the driver knew
// them, at most DLQRatePerSec per feed; excess is counted and dropped so a
// misconfigured feed cannot flood Kafka. received-at is when the frame
// arrived, or when it was dead-lettered for a payload that was never a frame.
//
// Once the publisher is closing a dead letter is dropped and counted with the
// ones over the rate: the producer is about to be flushed and closed, and a
// letter handed to it then would be lost unseen. The gate is held for the whole
// call, Produce included (which does not wait for Kafka), so that the close
// waits for a letter on its way, and the producer is flushed after it.
//
// A payload over DeadLetterRawMax is cut to it. A letter the producer refuses
// (its queue is full, say) is logged and counted as dropped with the reason
// produce_failed.
func (p *Publisher) DeadLetter(dl obs.DeadLetter) {
	feedID := dl.FeedID
	if feedID == "" {
		feedID = "unknown"
	}
	p.gate.RLock()
	defer p.gate.RUnlock()
	select {
	case <-p.done:
		p.dropDeadLetter(feedID, dropClosing)
		return
	default:
	}
	if !p.limiter(feedID).Allow() {
		p.dropDeadLetter(feedID, dropRateLimited)
		return
	}
	receivedAt := dl.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = time.Now()
	}
	headers := []kafka.Header{
		{Key: commonkafka.HeaderProducer, Value: []byte(wire.Producer)},
		{Key: commonkafka.HeaderFeedID, Value: []byte(feedID)},
		{Key: "driver", Value: []byte(dl.Driver)},
		{Key: "decoder", Value: []byte(dl.Decoder)},
		{Key: "sender", Value: []byte(dl.Sender)},
		{Key: "reason", Value: []byte(dl.Reason)},
		{Key: commonkafka.HeaderError, Value: []byte(errString(dl.Cause))},
		{Key: "received-at", Value: []byte(receivedAt.UTC().Format(time.RFC3339Nano))},
	}
	raw := dl.Raw
	if len(raw) > DeadLetterRawMax {
		headers = append(headers,
			kafka.Header{Key: "raw-truncated", Value: []byte("true")},
			kafka.Header{Key: "raw-size", Value: []byte(strconv.Itoa(len(raw)))})
		raw = raw[:DeadLetterRawMax]
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
		p.dropDeadLetter(feedID, dropProduceFailed)
		return
	}
	if p.metrics != nil {
		p.metrics.DeadLetterTotal.WithLabelValues(feedID, dl.Reason).Inc()
	}
}

// dropDeadLetter counts a dead letter that was not sent, by why.
func (p *Publisher) dropDeadLetter(feedID, reason string) {
	if p.metrics != nil {
		p.metrics.DeadLetterDropped.WithLabelValues(feedID, reason).Inc()
	}
}

// Close stops accepting observations, drains every shard and returns. It waits
// as long as the drain takes, which is for ever when the producer never takes
// what it is given (Kafka unreachable, its queue full): a caller with a deadline
// calls CloseWithin.
func (p *Publisher) Close() { p.shutdown(nil) }

// CloseWithin stops accepting observations and waits up to d for the shards to
// hand what they hold to the producer. If that takes longer it gives up: the
// shards stop, whatever they had not handed over is dropped, and the number of
// observations dropped is logged and returned. It returns 0 when the shards
// drained in time, as Close does. Only the first call to Close or CloseWithin
// does anything; the others return at once.
//
// What the producer holds is the caller's to flush, and to close once CloseWithin
// has returned: no shard calls the producer after that, unless one is stuck
// inside a call that never returns (and then the process is about to end).
func (p *Publisher) CloseWithin(d time.Duration) (abandoned int) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	return p.shutdown(timer.C)
}

// shutdown is Close, giving up when expired fires; a nil expired never does.
func (p *Publisher) shutdown(expired <-chan time.Time) int {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return 0
	}
	p.closed = true
	p.mu.Unlock()
	close(p.done)
	p.gate.Lock() // waits for the Emits that are queueing; each returns at once now
	p.gate.Unlock()
	close(p.drain)
	drained := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(drained)
	}()
	select {
	case <-drained:
		return 0
	case <-expired:
	}
	select {
	case <-drained: // done as the time ran out
		return 0
	default:
	}

	// Counted before the shards are told to stop: each one that stops drops its
	// pending count as it leaves, and what it was holding was abandoned all the same.
	abandoned := int(p.pending.Load())
	close(p.abandon)
	select {
	case <-drained:
	case <-time.After(abandonGrace):
		p.logger.Warn("a publisher shard is stuck inside the producer and was left behind")
	}
	if abandoned > 0 {
		p.logger.Warn("publisher gave up on observations that were not handed to the producer", zap.Int("abandoned", abandoned))
	}
	return abandoned
}

// abandoned reports whether CloseWithin has given up.
func (p *Publisher) abandoned() bool {
	select {
	case <-p.abandon:
		return true
	default:
		return false
	}
}

func (p *Publisher) run(shard int, ch <-chan *obs.Observation) {
	defer p.wg.Done()
	label := strconv.Itoa(shard)
	handle := func(o *obs.Observation) {
		if p.metrics != nil {
			p.metrics.PublisherShardDepth.WithLabelValues(label).Set(float64(len(ch)))
		}
		p.produce(o)
		p.pending.Add(-1)
	}
	for {
		select {
		case o := <-ch:
			if p.abandoned() {
				return // the rest of the queue is given up
			}
			handle(o)
		case <-p.drain:
			// No Emit is queueing any more, so what is in the channel is the
			// whole of what is left: hand it over, without waiting for more.
			for {
				select {
				case o := <-ch:
					if p.abandoned() {
						return
					}
					handle(o)
				default:
					return
				}
			}
		}
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
		if p.abandoned() {
			return
		}
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
		if !p.sleep(wait) {
			return
		}
		if wait < 500*time.Millisecond {
			wait *= 2
		}
	}
}

// sleep waits d, or until CloseWithin gives up, and reports whether it waited
// the whole of d.
func (p *Publisher) sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-p.abandon:
		return false
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
