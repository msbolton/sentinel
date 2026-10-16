package outbound

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	ckafka "github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
	"google.golang.org/protobuf/proto"

	commonkafka "github.com/sentinel/go-common/kafka"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/policy"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

// Consumer is the slice of the shared batch consumer the pump uses.
type Consumer interface {
	Subscribe(topics []string) error
	Run(ctx context.Context, handle commonkafka.BatchHandler) error
	Close() error
}

// ConsumerFactory builds the connection's own consumer group. Production
// wraps commonkafka.NewBatchConsumer; tests hand in a fake.
type ConsumerFactory func(groupID, clientID string) (Consumer, error)

// GroupID is the consumer group of a connection, derived from its feed id
// so offsets survive restarts and handovers.
func GroupID(feedID string) string { return "sentinel-conn-" + feedID }

// Options configure one connection's pump.
type Options struct {
	FeedID    string
	ReplicaID string
	Policy    policy.Policy
	Context   policy.Context

	QueueMax        int           // pending updates kept (10000)
	PerEntityMin    time.Duration // min spacing between upserts of one entity (5 s)
	MaxEventsPerSec int           // global delivery rate (200)
	MaxLag          time.Duration // events older than this are dropped, not replayed (5 m)
	ExitGrace       time.Duration // how long an entity must fail the policy before it is removed (30 s)
}

func (o Options) withDefaults() Options {
	if o.QueueMax <= 0 {
		o.QueueMax = 10000
	}
	if o.PerEntityMin < 0 {
		o.PerEntityMin = 0
	}
	if o.MaxEventsPerSec <= 0 {
		o.MaxEventsPerSec = 200
	}
	if o.MaxLag <= 0 {
		o.MaxLag = 5 * time.Minute
	}
	if o.ExitGrace < 0 {
		o.ExitGrace = 0
	}
	return o
}

// Counters are what a pump has done so far.
type Counters struct {
	Delivered       int64
	DeliveryErrors  int64
	Denied          int64
	Coalesced       int64
	Dropped         int64
	QueueDepth      int
	Tracked         int
	LastDeliveredAt time.Time
	LastError       string
}

// Pump consumes entity and alert events for one connection, filters them
// through the policy, coalesces per entity and delivers to the driver.
type Pump struct {
	opts    Options
	drv     driver.Consumer
	factory ConsumerFactory
	metrics *metrics.Metrics
	logger  *zap.Logger

	queue  *Queue
	alerts chan *sentinelv1.AlertEvent
	wake   chan struct{}

	mu          sync.Mutex
	tracked     map[string]bool
	deniedSince map[string]time.Time
	counters    Counters
	now         func() time.Time
}

// New builds a pump; Run starts it.
func New(o Options, drv driver.Consumer, factory ConsumerFactory, m *metrics.Metrics, logger *zap.Logger) *Pump {
	if logger == nil {
		logger = zap.NewNop()
	}
	o = o.withDefaults()
	return &Pump{
		opts: o, drv: drv, factory: factory, metrics: m, logger: logger,
		queue: NewQueue(o.QueueMax, o.PerEntityMin), alerts: make(chan *sentinelv1.AlertEvent, 1000), wake: make(chan struct{}, 1),
		tracked: map[string]bool{}, deniedSince: map[string]time.Time{}, now: time.Now,
	}
}

// Run consumes until ctx ends. It returns the consumer's error when the
// group cannot be joined or fails, which the supervisor treats like a
// driver failure (restart with backoff).
func (p *Pump) Run(ctx context.Context) error {
	c, err := p.factory(GroupID(p.opts.FeedID), "ingest-"+p.opts.ReplicaID)
	if err != nil {
		return fmt.Errorf("outbound consumer: %w", err)
	}
	topics := []string{commonkafka.TopicEntityEvents}
	if p.opts.Policy.Alerts.On() {
		topics = append(topics, commonkafka.TopicAlertEvents)
	}
	if err := c.Subscribe(topics); err != nil {
		_ = c.Close()
		return fmt.Errorf("outbound subscribe: %w", err)
	}
	deliverCtx, cancelDeliver := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.deliver(deliverCtx)
	}()
	runErr := c.Run(ctx, p.handle)
	closeErr := c.Close()
	cancelDeliver()
	wg.Wait()
	if runErr != nil && ctx.Err() == nil {
		return fmt.Errorf("outbound consumer: %w", runErr)
	}
	if closeErr != nil && ctx.Err() == nil {
		return fmt.Errorf("outbound consumer close: %w", closeErr)
	}
	return nil
}

// Stats returns a copy of the counters with live queue and tracked sizes.
func (p *Pump) Stats() Counters {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := p.counters
	c.QueueDepth = p.queue.Len()
	c.Tracked = len(p.tracked)
	c.Coalesced = p.queue.Coalesced
	c.Dropped = p.counters.Dropped + p.queue.DroppedOldest
	return c
}

// handle is the batch handler. It never returns an error: a message that
// cannot be decoded is logged and skipped, so offsets always advance.
func (p *Pump) handle(_ context.Context, msgs []*ckafka.Message) error {
	now := p.now()
	for _, m := range msgs {
		topic := ""
		if m.TopicPartition.Topic != nil {
			topic = *m.TopicPartition.Topic
		}
		switch topic {
		case commonkafka.TopicEntityEvents:
			p.handleEntity(m, now)
		case commonkafka.TopicAlertEvents:
			p.handleAlert(m, now)
		}
	}
	p.signal()
	return nil
}

func (p *Pump) handleEntity(m *ckafka.Message, now time.Time) {
	var ev sentinelv1.EntityEvent
	if err := proto.Unmarshal(m.Value, &ev); err != nil {
		p.logger.Warn("undecodable entity event skipped", zap.Error(err))
		p.drop("undecodable")
		return
	}
	eventAt := now
	if ev.EventTime != nil {
		eventAt = ev.EventTime.AsTime()
	}
	if p.opts.MaxLag > 0 && now.Sub(eventAt) > p.opts.MaxLag {
		p.drop("lag")
		return
	}
	var entity *sentinelv1.Entity
	switch e := ev.Event.(type) {
	case *sentinelv1.EntityEvent_Created:
		entity = e.Created.GetEntity()
	case *sentinelv1.EntityEvent_Position:
		entity = e.Position.GetEntity()
	case *sentinelv1.EntityEvent_Updated:
		entity = e.Updated.GetEntity()
	case *sentinelv1.EntityEvent_Restored:
		entity = e.Restored.GetEntity()
	case *sentinelv1.EntityEvent_Deleted:
		if e.Deleted.GetBulk() {
			p.removeAll("deleted", eventAt)
			return
		}
		p.remove(ev.EntityId, "deleted", eventAt)
		return
	case *sentinelv1.EntityEvent_AgedOut:
		p.remove(ev.EntityId, "aged_out", eventAt)
		return
	default:
		return // stale: the remote's own stale time covers it
	}
	if entity == nil || entity.Id == "" {
		return
	}
	d := policy.Evaluate(p.opts.Policy, p.opts.Context, entity)
	p.mu.Lock()
	defer p.mu.Unlock()
	if !d.Allow {
		p.counters.Denied++
		if p.metrics != nil {
			p.metrics.OutboundDeniedTotal.WithLabelValues(p.opts.FeedID, d.Reason).Inc()
		}
		if p.tracked[entity.Id] {
			if _, already := p.deniedSince[entity.Id]; !already {
				p.deniedSince[entity.Id] = now
			}
		}
		return
	}
	delete(p.deniedSince, entity.Id)
	p.tracked[entity.Id] = true
	p.queue.Push(driver.EntityUpdate{Kind: driver.UpsertEntity, EntityID: entity.Id, Entity: entity, EventAt: eventAt})
}

func (p *Pump) handleAlert(m *ckafka.Message, now time.Time) {
	var ev sentinelv1.AlertEvent
	if err := proto.Unmarshal(m.Value, &ev); err != nil || ev.Alert == nil {
		p.logger.Warn("undecodable alert event skipped", zap.Error(err))
		p.drop("undecodable")
		return
	}
	if ev.EventTime != nil && p.opts.MaxLag > 0 && now.Sub(ev.EventTime.AsTime()) > p.opts.MaxLag {
		p.drop("lag")
		return
	}
	p.mu.Lock()
	tracked := p.tracked[ev.Alert.EntityId]
	p.mu.Unlock()
	if d := policy.AllowAlert(p.opts.Policy, p.opts.Context, ev.Alert, tracked); !d.Allow {
		p.mu.Lock()
		p.counters.Denied++
		p.mu.Unlock()
		if p.metrics != nil {
			p.metrics.OutboundDeniedTotal.WithLabelValues(p.opts.FeedID, d.Reason).Inc()
		}
		return
	}
	select {
	case p.alerts <- &ev:
	default:
		p.drop("alert_queue_full")
	}
}

// remove queues a removal for an entity the remote knows about.
func (p *Pump) remove(id, reason string, at time.Time) {
	if id == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.tracked[id] {
		return
	}
	delete(p.tracked, id)
	delete(p.deniedSince, id)
	p.queue.Push(driver.EntityUpdate{Kind: driver.RemoveEntity, EntityID: id, Reason: reason, EventAt: at})
}

func (p *Pump) removeAll(reason string, at time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id := range p.tracked {
		p.queue.Push(driver.EntityUpdate{Kind: driver.RemoveEntity, EntityID: id, Reason: reason, EventAt: at})
	}
	p.tracked = map[string]bool{}
	p.deniedSince = map[string]time.Time{}
}

// expireDenied removes entities that have failed the policy for longer than
// the grace period (hysteresis against box-edge flapping).
func (p *Pump) expireDenied(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, since := range p.deniedSince {
		if now.Sub(since) >= p.opts.ExitGrace {
			delete(p.deniedSince, id)
			delete(p.tracked, id)
			p.queue.Push(driver.EntityUpdate{Kind: driver.RemoveEntity, EntityID: id, Reason: "policy", EventAt: now})
		}
	}
}

func (p *Pump) drop(reason string) {
	p.mu.Lock()
	p.counters.Dropped++
	p.mu.Unlock()
	if p.metrics != nil {
		p.metrics.OutboundDroppedTotal.WithLabelValues(p.opts.FeedID, reason).Inc()
	}
}

func (p *Pump) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// deliver drains the queue and the alert channel into the driver under the
// global rate limit, pausing while the remote is not connected.
func (p *Pump) deliver(ctx context.Context) {
	limiter := rate.NewLimiter(rate.Limit(p.opts.MaxEventsPerSec), p.opts.MaxEventsPerSec)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		now := p.now()
		progressed := false
		if u, ok := p.queue.Pop(now); ok {
			progressed = true
			if err := limiter.Wait(ctx); err != nil {
				return
			}
			p.send(ctx, u)
		} else {
			select {
			case ev := <-p.alerts:
				progressed = true
				if err := limiter.Wait(ctx); err != nil {
					return
				}
				p.sendAlert(ctx, ev)
			default:
			}
		}
		if progressed {
			continue
		}
		wait := p.queue.NextDue(now)
		if wait <= 0 || wait > time.Second {
			wait = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
		case <-tick.C:
			if p.opts.ExitGrace >= 0 {
				p.expireDenied(p.now())
			}
		case <-time.After(wait):
		}
	}
}

func (p *Pump) send(ctx context.Context, u driver.EntityUpdate) {
	err := p.drv.HandleEntity(ctx, u)
	switch {
	case err == nil:
		p.delivered(u.EventAt, u.Kind)
	case errors.Is(err, driver.ErrNotConnected):
		// Keep the update and let the remote come back.
		p.queue.Push(u)
		p.pause(ctx)
	default:
		p.failed(err)
	}
}

func (p *Pump) sendAlert(ctx context.Context, ev *sentinelv1.AlertEvent) {
	err := p.drv.HandleAlert(ctx, ev)
	switch {
	case err == nil:
		at := p.now()
		if ev.EventTime != nil {
			at = ev.EventTime.AsTime()
		}
		p.delivered(at, driver.UpsertEntity)
		if p.metrics != nil {
			p.metrics.OutboundEventsTotal.WithLabelValues(p.opts.FeedID, "alert").Inc()
		}
	case errors.Is(err, driver.ErrNotConnected):
		select {
		case p.alerts <- ev:
		default:
			p.drop("alert_queue_full")
		}
		p.pause(ctx)
	default:
		p.failed(err)
	}
}

func (p *Pump) pause(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
	}
}

func (p *Pump) delivered(eventAt time.Time, kind driver.UpdateKind) {
	now := p.now()
	p.mu.Lock()
	p.counters.Delivered++
	p.counters.LastDeliveredAt = now
	p.mu.Unlock()
	if p.metrics != nil {
		label := "upsert"
		if kind == driver.RemoveEntity {
			label = "remove"
		}
		p.metrics.OutboundEventsTotal.WithLabelValues(p.opts.FeedID, label).Inc()
		if !eventAt.IsZero() {
			p.metrics.OutboundLagSeconds.WithLabelValues(p.opts.FeedID).Set(now.Sub(eventAt).Seconds())
		}
	}
}

func (p *Pump) failed(err error) {
	p.mu.Lock()
	p.counters.DeliveryErrors++
	p.counters.LastError = err.Error()
	p.mu.Unlock()
	if p.metrics != nil {
		p.metrics.OutboundDeliveryErrorsTotal.WithLabelValues(p.opts.FeedID).Inc()
	}
	p.logger.Warn("outbound delivery failed", zap.String("feed", p.opts.FeedID), zap.Error(err))
}
