package feed

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	commonkafka "github.com/sentinel/go-common/kafka"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/ingest-service/internal/outbound"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

// bothDriver is a connection: it runs until stopped and accepts updates.
type bothDriver struct {
	scriptedDriver
	handled chan driver.EntityUpdate
}

func (d *bothDriver) HandleEntity(_ context.Context, u driver.EntityUpdate) error {
	d.handled <- u
	return nil
}
func (d *bothDriver) HandleAlert(context.Context, *sentinelv1.AlertEvent) error { return nil }

// idleConsumer joins and waits; it never delivers anything.
type idleConsumer struct{ closed bool }

func (c *idleConsumer) Subscribe([]string) error { return nil }
func (c *idleConsumer) Run(ctx context.Context, _ commonkafka.BatchHandler) error {
	<-ctx.Done()
	return nil
}
func (c *idleConsumer) Close() error { c.closed = true; return nil }

func TestRunnerRunsPumpBesideDriverAndStopsBoth(t *testing.T) {
	driverStopped := make(chan struct{})
	drv := &bothDriver{handled: make(chan driver.EntityUpdate, 1)}
	drv.run = func(ctx context.Context, _ driver.Sink) error {
		<-ctx.Done()
		close(driverStopped)
		return nil
	}
	consumer := &idleConsumer{}
	pump := outbound.New(outbound.Options{FeedID: "conn", ReplicaID: "r1"}, drv, func(string, string) (outbound.Consumer, error) { return consumer, nil }, nil, zap.NewNop())
	r := NewRunner("conn", "tak", drv, &memPublisher{}, nil, zap.NewNop(), RunnerOptions{Pump: pump})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)
	if st := r.Stats(); st.Outbound == nil {
		t.Fatal("connection stats must carry outbound counters")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not stop")
	}
	<-driverStopped
	if !consumer.closed {
		t.Error("consumer not closed on stop")
	}
}

func TestRunnerPumpFailureStopsTheDriver(t *testing.T) {
	driverStopped := make(chan struct{})
	drv := &bothDriver{handled: make(chan driver.EntityUpdate, 1)}
	drv.run = func(ctx context.Context, _ driver.Sink) error {
		<-ctx.Done()
		close(driverStopped)
		return nil
	}
	pump := outbound.New(outbound.Options{FeedID: "conn", ReplicaID: "r1"}, drv, func(string, string) (outbound.Consumer, error) { return nil, errors.New("no brokers") }, nil, zap.NewNop())
	r := NewRunner("conn", "tak", drv, &memPublisher{}, nil, zap.NewNop(), RunnerOptions{Pump: pump})
	err := r.Run(context.Background())
	if err == nil || err.Error() != "outbound consumer: no brokers" {
		t.Fatalf("run returned %v", err)
	}
	select {
	case <-driverStopped:
	case <-time.After(2 * time.Second):
		t.Fatal("driver kept running after the pump failed")
	}
}
