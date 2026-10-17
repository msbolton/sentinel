// Package consumer reads entities.events and turns created and position
// events into fixes for the detectors and into coverage-cell updates.
package consumer

import (
	"context"
	"time"

	ckafka "github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/sentinel/go-common/kafka"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/analytics-service/internal/geo"
	"github.com/sentinel/analytics-service/internal/model"
	"github.com/sentinel/analytics-service/internal/stats"
	"github.com/sentinel/analytics-service/internal/store"
)

// Sink receives every fix; the detector engine implements it.
type Sink interface {
	OnFix(ctx context.Context, f model.Fix)
}

// NopSink discards fixes.
type NopSink struct{}

// OnFix does nothing.
func (NopSink) OnFix(context.Context, model.Fix) {}

// Coverage receives the per-batch coverage updates.
type Coverage interface {
	UpsertCells(ctx context.Context, cells []store.CellObs) error
}

// PositionConsumer feeds the sink and the coverage grid from entity events.
type PositionConsumer struct {
	bc       *kafka.BatchConsumer
	sink     Sink
	coverage Coverage
	cellDeg  float64
	counters *stats.Counters
	logger   *zap.Logger
	cancel   context.CancelFunc
	done     chan struct{}
}

// NewPositionConsumer creates and subscribes the consumer.
func NewPositionConsumer(brokers string, sink Sink, coverage Coverage, cellDeg float64, counters *stats.Counters, logger *zap.Logger) (*PositionConsumer, error) {
	bc, err := kafka.NewBatchConsumer(brokers, kafka.GroupAnalyticsService, "analytics-service", "latest",
		kafka.BatchOptions{MaxMessages: 500, MaxWait: 200 * time.Millisecond}, logger)
	if err != nil {
		return nil, err
	}
	if err := bc.Subscribe([]string{kafka.TopicEntityEvents}); err != nil {
		_ = bc.Close()
		return nil, err
	}
	return &PositionConsumer{bc: bc, sink: sink, coverage: coverage, cellDeg: cellDeg, counters: counters, logger: logger, done: make(chan struct{})}, nil
}

// Start begins consuming in the background.
func (c *PositionConsumer) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	go func() {
		defer close(c.done)
		c.logger.Info("analytics consumer started", zap.String("topic", kafka.TopicEntityEvents))
		if err := c.bc.Run(ctx, c.Handle); err != nil {
			c.logger.Error("analytics consumer stopped", zap.Error(err))
		}
	}()
}

// Stop ends consumption.
func (c *PositionConsumer) Stop() {
	if c.cancel != nil {
		c.cancel()
		<-c.done
	}
	_ = c.bc.Close()
}

// Handle processes one batch. It never returns an error: a message that
// cannot be decoded is logged and skipped, because retrying would only
// replay it, and a coverage write failure is logged because the next batch
// repairs it.
func (c *PositionConsumer) Handle(ctx context.Context, msgs []*ckafka.Message) error {
	cells := map[[2]string]time.Time{}
	fixes := 0
	for _, m := range msgs {
		kind := header(m, kafka.HeaderEventType)
		if kind != "position" && kind != "created" {
			continue
		}
		var ev sentinelv1.EntityEvent
		if err := proto.Unmarshal(m.Value, &ev); err != nil {
			c.logger.Warn("undecodable entity event skipped", zap.Error(err))
			continue
		}
		f, ok := model.FixFromEvent(&ev)
		if !ok {
			continue
		}
		if f.FeedID != "" {
			key := [2]string{geo.Cell(f.Lat, f.Lon, c.cellDeg), f.FeedID}
			if f.At.After(cells[key]) {
				cells[key] = f.At
			}
		}
		c.sink.OnFix(ctx, f)
		fixes++
	}
	if c.counters != nil {
		c.counters.Positions(fixes)
	}
	if len(cells) > 0 {
		obs := make([]store.CellObs, 0, len(cells))
		for k, at := range cells {
			obs = append(obs, store.CellObs{Cell: k[0], FeedID: k[1], At: at})
		}
		if err := c.coverage.UpsertCells(ctx, obs); err != nil {
			c.logger.Warn("coverage cells not written", zap.Error(err))
		} else if c.counters != nil {
			c.counters.CoverageWrites(len(obs))
		}
	}
	return nil
}

func header(m *ckafka.Message, key string) string {
	for _, h := range m.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}
