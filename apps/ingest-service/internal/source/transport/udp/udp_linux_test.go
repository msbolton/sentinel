//go:build linux

package udp

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/source/transport"
	"github.com/sentinel/ingest-service/internal/source/transport/transporttest"
)

// detailInt reads a counter out of a listener's health detail.
func detailInt(tr transport.Transport, key string) int64 {
	n, _ := tr.State().Detail[key].(int64)
	return n
}

// A datagram that finds the socket's receive buffer full is dropped by the
// kernel, and the listener never sees it. The kernel keeps a count of them and,
// with SO_RXQ_OVFL set, attaches it to the datagrams it delivers once a drop
// has happened. The listener reports it as kernelDropped.
//
// The first datagram parks the receive loop in emit, so the 1000 after it fill
// the few KiB the socket has and the rest are dropped. The datagrams that were
// queued before the first drop carry no count; one sent after the loop has
// drained them does.
//
// It runs only on Linux, which is the only system that reports drops. It
// asserts bounds on the listener's own counters, not exact numbers, so it holds
// whatever the machine's loopback does with a burst.
func TestKernelDropsCounted(t *testing.T) {
	addr := freeUDPAddr(t)
	tr, err := New(transport.Config{Kind: "udp", Address: addr, ReceiveBufferBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	emit := func(ctx context.Context, _ transport.Frame) error {
		once.Do(func() { close(entered) })
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}
	stop := transporttest.Run(t, tr, emit)
	defer stop()
	if !transporttest.WaitFor(func() bool { return tr.State().Connected }, 2*time.Second) {
		t.Fatal("listener not up")
	}
	c, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sent := 0 // touched only by this goroutine
	send := func() {
		if _, err := c.Write([]byte("0123456789")); err == nil {
			sent++
		}
	}

	send()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the receive loop never took the first datagram")
	}
	for range 1000 {
		send()
	}
	close(release)

	// The loop drains what the buffer held, then reads the datagrams sent
	// from here on, each stamped with the count of what was dropped.
	if !transporttest.WaitFor(func() bool { send(); return detailInt(tr, "kernelDropped") > 0 }, 5*time.Second) {
		t.Fatalf("kernelDropped = 0 after %d datagrams into a %d byte buffer: %+v", sent, detailInt(tr, "receiveBufferEffectiveBytes"), tr.State().Detail)
	}

	// The count is the socket's total so far, which each datagram repeats, not
	// a number to add up: reading more datagrams changes it only when more are
	// dropped. Every datagram sent was read or dropped, so the two counts
	// together can never come to more than were sent.
	for range 5 {
		before := detailInt(tr, "accepted")
		send()
		if !transporttest.WaitFor(func() bool { return detailInt(tr, "accepted") > before }, 2*time.Second) {
			t.Fatalf("a datagram sent into a drained buffer was not read: %+v", tr.State().Detail)
		}
	}
	accepted, dropped := detailInt(tr, "accepted"), detailInt(tr, "kernelDropped")
	if accepted+dropped > int64(sent) {
		t.Errorf("accepted %d + kernelDropped %d = %d, more than the %d datagrams sent: the kernel's total was added up instead of stored", accepted, dropped, accepted+dropped, sent)
	}
	if dropped <= 0 {
		t.Errorf("kernelDropped = %d after the buffer overflowed", dropped)
	}
}
