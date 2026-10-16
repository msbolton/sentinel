// Package transporttest has helpers shared by transport tests.
package transporttest

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/source/transport"
)

// Collector gathers emitted frames.
type Collector struct {
	mu     sync.Mutex
	frames []transport.Frame
}

// Emit records a frame.
func (c *Collector) Emit(_ context.Context, f transport.Frame) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frames = append(c.frames, f)
	return nil
}

// Payloads returns the collected payloads as strings.
func (c *Collector) Payloads() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.frames))
	for i, f := range c.frames {
		out[i] = string(f.Payload)
	}
	return out
}

// Frames returns a copy of the collected frames.
func (c *Collector) Frames() []transport.Frame {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]transport.Frame(nil), c.frames...)
}

// Count returns the number of frames.
func (c *Collector) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.frames)
}

// WaitFor polls cond until it is true or the timeout passes.
func WaitFor(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// FreePort returns a free loopback host:port.
func FreePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// Run starts the transport and returns a stop function that cancels and
// waits for Run to return.
func Run(t *testing.T, tr transport.Transport, emit transport.Emit) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- tr.Run(ctx, emit) }()
	return func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run returned %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("transport did not stop")
		}
	}
}
