package stream

import (
	"sync"
	"time"
)

// Batcher coalesces entity events and flushes them to the hub on an interval,
// so bursts of position updates become one frame per client.
type Batcher struct {
	hub      *Hub
	interval time.Duration

	mu      sync.Mutex
	pending []PositionedEvent

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

// NewBatcher creates a batcher flushing every interval (200 ms in production).
func NewBatcher(hub *Hub, interval time.Duration) *Batcher {
	return &Batcher{hub: hub, interval: interval, stop: make(chan struct{}), done: make(chan struct{})}
}

// Add queues an event for the next flush.
func (b *Batcher) Add(e PositionedEvent) {
	b.mu.Lock()
	b.pending = append(b.pending, e)
	b.mu.Unlock()
}

// Flush broadcasts everything queued so far.
func (b *Batcher) Flush() {
	b.mu.Lock()
	batch := b.pending
	b.pending = nil
	b.mu.Unlock()
	b.hub.BroadcastBatch(batch)
}

// Start runs the flush ticker until Stop is called.
func (b *Batcher) Start() {
	go func() {
		defer close(b.done)
		ticker := time.NewTicker(b.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				b.Flush()
			case <-b.stop:
				b.Flush()
				return
			}
		}
	}()
}

// Stop ends the ticker after a final flush.
func (b *Batcher) Stop() {
	b.stopOnce.Do(func() { close(b.stop) })
	<-b.done
}
