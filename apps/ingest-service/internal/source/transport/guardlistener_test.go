package transport

import (
	"net"
	"testing"
	"time"
)

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func dialTCP(t *testing.T, addr string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

type accepted struct {
	conn net.Conn
	err  error
}

func acceptLoop(gl *GuardListener) <-chan accepted {
	out := make(chan accepted, 8)
	go func() {
		for {
			c, err := gl.Accept()
			out <- accepted{c, err}
			if err != nil {
				return
			}
		}
	}()
	return out
}

// A sender outside the allowlist is closed and counted; Accept does not
// return for it (grpc's Serve would stop on an Accept error).
func TestGuardListenerRefusesOutsideTheAllowlistWithoutAnError(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	allow, err := ParseAllowlist([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	var counts ListenCounters
	gl := NewGuardListener(inner, allow, 0, &counts, NewSenders(50))
	got := acceptLoop(gl)
	dialTCP(t, inner.Addr().String())
	dialTCP(t, inner.Addr().String())
	waitUntil(t, func() bool { return counts.Rejected.Load() == 2 })
	select {
	case a := <-got:
		t.Fatalf("Accept returned for a refused sender: %v, %v", a.conn, a.err)
	case <-time.After(50 * time.Millisecond):
	}
	gl.Close()
	if a := <-got; a.err == nil {
		t.Fatal("Accept must return the error once the listener is closed")
	}
}

// Over the cap a connection is refused and counted; closing an admitted
// one (twice, even) frees exactly one place.
func TestGuardListenerCapsConnections(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var counts ListenCounters
	senders := NewSenders(50)
	gl := NewGuardListener(inner, Allowlist{}, 1, &counts, senders)
	defer gl.Close()
	got := acceptLoop(gl)
	dialTCP(t, inner.Addr().String())
	first := <-got
	if first.err != nil {
		t.Fatal(first.err)
	}
	if gl.Live() != 1 {
		t.Fatalf("Live = %d, want 1", gl.Live())
	}
	if _, n := senders.Snapshot(time.Now(), time.Minute); n != 1 {
		t.Fatalf("distinct senders = %d, want 1", n)
	}
	dialTCP(t, inner.Addr().String())
	waitUntil(t, func() bool { return counts.Refused.Load() == 1 })
	first.conn.Close()
	first.conn.Close()
	if gl.Live() != 0 {
		t.Fatalf("Live after a double close = %d, want 0", gl.Live())
	}
	dialTCP(t, inner.Addr().String())
	if a := <-got; a.err != nil {
		t.Fatalf("a place freed up, but Accept failed: %v", a.err)
	}
}

// CloseAll ends every admitted connection and gives its place back; the
// peers see the close.
func TestGuardListenerCloseAll(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var counts ListenCounters
	senders := NewSenders(50)
	gl := NewGuardListener(inner, Allowlist{}, 0, &counts, senders)
	defer gl.Close()
	got := acceptLoop(gl)
	peers := []net.Conn{dialTCP(t, inner.Addr().String()), dialTCP(t, inner.Addr().String())}
	for range peers {
		if a := <-got; a.err != nil {
			t.Fatal(a.err)
		}
	}
	if gl.Live() != 2 {
		t.Fatalf("Live = %d, want 2", gl.Live())
	}
	gl.CloseAll()
	if gl.Live() != 0 {
		t.Fatalf("Live after CloseAll = %d, want 0", gl.Live())
	}
	for i, p := range peers {
		p.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err := p.Read(make([]byte, 1)); err == nil || isTimeout(err) {
			t.Errorf("peer %d: read = %v, want the connection closed", i, err)
		}
	}
	gl.CloseAll() // nothing left, and no panic
	dialTCP(t, inner.Addr().String())
	if a := <-got; a.err != nil {
		t.Fatalf("Accept after CloseAll: %v", a.err)
	}
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}
