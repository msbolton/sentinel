package stream

import (
	"bytes"
	"encoding/json"
	"sync"
	"time"
)

// Batcher coalesces entity events and flushes them to the hub on an interval,
// so bursts of position updates become one frame per client. Raw broadcast
// events (stale, aged out, restored) are coalesced the same way into one
// JSON array frame per type: the ageout service can emit a thousand of them
// in one cycle, which as individual frames would overflow client buffers.
type Batcher struct {
	hub      *Hub
	interval time.Duration

	mu         sync.Mutex
	pending    []PositionedEvent
	pendingRaw map[string][]json.RawMessage
	rawOrder   []string

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

// AddRaw queues an already-encoded JSON event to be broadcast to every
// client as part of an array frame of the given type.
func (b *Batcher) AddRaw(msgType string, data json.RawMessage) {
	b.mu.Lock()
	if b.pendingRaw == nil {
		b.pendingRaw = map[string][]json.RawMessage{}
	}
	if _, seen := b.pendingRaw[msgType]; !seen {
		b.rawOrder = append(b.rawOrder, msgType)
	}
	b.pendingRaw[msgType] = append(b.pendingRaw[msgType], data)
	b.mu.Unlock()
}

// Flush broadcasts everything queued so far.
func (b *Batcher) Flush() {
	b.mu.Lock()
	batch := b.pending
	b.pending = nil
	raw := b.pendingRaw
	order := b.rawOrder
	b.pendingRaw = nil
	b.rawOrder = nil
	b.mu.Unlock()

	b.hub.BroadcastBatch(batch)
	for _, msgType := range order {
		b.hub.BroadcastRaw(msgType, joinJSONArray(raw[msgType]))
	}
}

// joinJSONArray concatenates encoded objects into a JSON array.
func joinJSONArray(items []json.RawMessage) []byte {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(item)
	}
	buf.WriteByte(']')
	return buf.Bytes()
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
