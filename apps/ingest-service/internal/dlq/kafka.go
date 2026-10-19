package dlq

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"go.uber.org/zap"
)

// ErrClosed is what a read made after Close returns.
var ErrClosed = errors.New("dead-letter reader is closed")

// peekGroup is the group.id of the consumer. librdkafka insists on one, but the
// consumer assigns itself partitions and never commits, so the group never has
// a member or an offset.
const peekGroup = "sentinel-ingest-dlq-peek"

// KafkaReader reads the dead-letter topic with one consumer, which it creates
// when the first read comes, so that a Kafka that is down at startup does not
// stop the service, and reads one request at a time, because a consumer has one
// assignment.
type KafkaReader struct {
	topic    string
	maxScan  int64
	deadline time.Duration
	logger   *zap.Logger
	open     func() (source, error)

	// slot is held by the read that is using the consumer, and by Close. It is
	// a channel of one rather than a sync.Mutex so that a request waiting its
	// turn stops waiting when its time is up or its caller goes away.
	slot chan struct{}
	// src and closed belong to whoever holds slot.
	src    source
	closed bool
}

// NewKafkaReader returns a reader of topic on brokers that scans at most
// maxScan messages of each partition per read (it must be positive). It
// contacts nothing until the first read.
func NewKafkaReader(brokers, topic string, maxScan int, logger *zap.Logger) *KafkaReader {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &KafkaReader{
		topic: topic, maxScan: int64(maxScan), deadline: Deadline, logger: logger,
		open: func() (source, error) { return newKafkaSource(brokers, topic) },
		slot: make(chan struct{}, 1),
	}
}

// Recent implements Reader. The read, the wait for the consumer included, takes
// at most Deadline.
func (r *KafkaReader) Recent(ctx context.Context, feedID string, limit int) ([]Letter, int, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, r.deadline)
	defer cancel()
	select {
	case r.slot <- struct{}{}:
		defer func() { <-r.slot }()
	case <-ctx.Done():
		return nil, 0, false, fmt.Errorf("waiting for an earlier read of the dead-letter topic: %w", ctx.Err())
	}
	if r.closed {
		return nil, 0, false, ErrClosed
	}
	if r.src == nil {
		src, err := r.open()
		if err != nil {
			return nil, 0, false, fmt.Errorf("creating the dead-letter consumer: %w", err)
		}
		r.src = src
	}
	return scan(ctx, r.src, r.maxScan, feedID, limit)
}

// Close releases the consumer once a read in progress has finished. Reads made
// afterwards return ErrClosed. It is safe to call more than once.
func (r *KafkaReader) Close() {
	r.slot <- struct{}{}
	defer func() { <-r.slot }()
	if r.closed {
		return
	}
	r.closed = true
	if r.src != nil {
		if err := r.src.Close(); err != nil {
			r.logger.Warn("closing the dead-letter consumer failed", zap.Error(err))
		}
		r.src = nil
	}
}

// kafkaSource is a librdkafka consumer as scan uses it.
type kafkaSource struct {
	c     *kafka.Consumer
	topic string
}

func newKafkaSource(brokers, topic string) (*kafkaSource, error) {
	c, err := kafka.NewConsumer(&kafka.ConfigMap{
		"bootstrap.servers": brokers,
		"group.id":          peekGroup,
		// Partitions are assigned with explicit offsets and nothing is committed.
		"enable.auto.commit": false,
		// Say when a partition has been read to its end, which is how a scan
		// knows it is done without waiting for a message that never comes.
		"enable.partition.eof": true,
		// If retention removes the first offset assigned before it is fetched,
		// start at what is left instead of failing.
		"auto.offset.reset": "earliest",
		// This reads; it must not bring a topic into being.
		"allow.auto.create.topics": false,
	})
	if err != nil {
		return nil, fmt.Errorf("creating kafka consumer: %w", err)
	}
	return &kafkaSource{c: c, topic: topic}, nil
}

// Bounds asks for the topic's partitions and then, for each, the offsets it holds.
func (s *kafkaSource) Bounds(ctx context.Context) ([]bounds, error) {
	md, err := s.c.GetMetadata(&s.topic, false, millisLeft(ctx))
	if err != nil {
		return nil, fmt.Errorf("fetching metadata of %s: %w", s.topic, err)
	}
	tm, ok := md.Topics[s.topic]
	if !ok {
		return nil, fmt.Errorf("no metadata for topic %s", s.topic)
	}
	if tm.Error.Code() != kafka.ErrNoError {
		return nil, fmt.Errorf("topic %s: %w", s.topic, tm.Error)
	}
	out := make([]bounds, 0, len(tm.Partitions))
	for _, p := range tm.Partitions {
		if p.Error.Code() != kafka.ErrNoError {
			return nil, fmt.Errorf("partition %d of %s: %w", p.ID, s.topic, p.Error)
		}
		low, high, err := s.c.QueryWatermarkOffsets(s.topic, p.ID, millisLeft(ctx))
		if err != nil {
			return nil, fmt.Errorf("offsets of partition %d of %s: %w", p.ID, s.topic, err)
		}
		out = append(out, bounds{partition: p.ID, low: low, high: high})
	}
	return out, nil
}

func (s *kafkaSource) Assign(from map[int32]int64) error {
	parts := make([]kafka.TopicPartition, 0, len(from))
	for id, offset := range from {
		parts = append(parts, kafka.TopicPartition{Topic: &s.topic, Partition: id, Offset: kafka.Offset(offset)})
	}
	return s.c.Assign(parts)
}

func (s *kafkaSource) Unassign() error { return s.c.Unassign() }

// Next polls once. A message that carries an error, and a fatal client error,
// fail the scan; the other errors librdkafka reports (a broker went away, say)
// are noise it recovers from by itself, and the deadline covers the case where
// it does not.
func (s *kafkaSource) Next(wait time.Duration) (event, bool, error) {
	switch e := s.c.Poll(int(wait / time.Millisecond)).(type) {
	case *kafka.Message:
		if e.TopicPartition.Error != nil {
			return event{}, false, e.TopicPartition.Error
		}
		return eventFrom(e), true, nil
	case kafka.PartitionEOF:
		return event{partition: e.Partition, eof: true}, true, nil
	case kafka.Error:
		if e.IsFatal() {
			return event{}, false, e
		}
	}
	return event{}, false, nil
}

func (s *kafkaSource) Close() error { return s.c.Close() }

// eventFrom is the message a scan reads.
func eventFrom(m *kafka.Message) event {
	ev := event{
		partition: m.TopicPartition.Partition, offset: int64(m.TopicPartition.Offset),
		at: m.Timestamp, key: string(m.Key), value: m.Value,
	}
	if len(m.Headers) > 0 {
		ev.headers = make([]header, len(m.Headers))
		for i, h := range m.Headers {
			ev.headers[i] = header{key: h.Key, value: h.Value}
		}
	}
	return ev
}

// millisLeft is the time until ctx's deadline in milliseconds, for librdkafka's
// calls that take a timeout. It is at least one: zero means do not wait.
func millisLeft(ctx context.Context) int {
	if deadline, ok := ctx.Deadline(); ok {
		return max(int(time.Until(deadline)/time.Millisecond), 1)
	}
	return int(Deadline / time.Millisecond)
}
