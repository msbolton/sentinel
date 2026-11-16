package kafka

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is safe to advance from a test while the consume loop reads it.
type fakeClock struct{ unix atomic.Int64 }

func (c *fakeClock) now() time.Time          { return time.Unix(0, c.unix.Load()) }
func (c *fakeClock) set(t time.Time)         { c.unix.Store(t.UnixNano()) }
func (c *fakeClock) advance(d time.Duration) { c.unix.Add(int64(d)) }

func newTestLiveness() (*liveness, *fakeClock) {
	clock := &fakeClock{}
	clock.set(time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC))
	l := newLiveness(time.Minute, 15*time.Second)
	l.now = clock.now
	return l, clock
}

func wantReason(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), substr) {
		t.Fatalf("got %v, want error containing %q", err, substr)
	}
}

func TestLivenessNotStarted(t *testing.T) {
	l, _ := newTestLiveness()
	wantReason(t, l.check(), "not started")
}

func TestLivenessHealthyWhenAssignedAndPolling(t *testing.T) {
	l, clock := newTestLiveness()
	l.start()
	l.assign()
	clock.advance(30 * time.Second)
	l.polled()
	clock.advance(30 * time.Second)
	if err := l.check(); err != nil {
		t.Fatalf("unexpected %v", err)
	}
}

func TestLivenessUnassignedPastStaleAfter(t *testing.T) {
	l, clock := newTestLiveness()
	l.start()
	clock.advance(59 * time.Second)
	l.polled()
	if err := l.check(); err != nil {
		t.Fatalf("within grace: %v", err)
	}
	clock.advance(2 * time.Second)
	l.polled()
	wantReason(t, l.check(), "no partition assignment")
	l.assign()
	if err := l.check(); err != nil {
		t.Fatalf("after assignment: %v", err)
	}
	l.revoke()
	clock.advance(61 * time.Second)
	l.polled()
	wantReason(t, l.check(), "no partition assignment")
}

func TestLivenessNoPoll(t *testing.T) {
	l, clock := newTestLiveness()
	l.start()
	l.assign()
	clock.advance(61 * time.Second)
	wantReason(t, l.check(), "not polled")
	l.polled()
	if err := l.check(); err != nil {
		t.Fatalf("after poll: %v", err)
	}
}

func TestLivenessBrokersDownWithGrace(t *testing.T) {
	l, clock := newTestLiveness()
	l.start()
	l.assign()
	l.brokersDown()
	clock.advance(10 * time.Second)
	l.polled()
	if err := l.check(); err != nil {
		t.Fatalf("within grace: %v", err)
	}
	clock.advance(10 * time.Second)
	l.polled()
	wantReason(t, l.check(), "unreachable")
	// Outage end is seen through the client's statistics, a message, a
	// commit or a fresh assignment; each clears it.
	for name, clear := range map[string]func(){"stats": l.brokersUp, "progress": l.progressed, "assign": l.assign} {
		l.brokersDown()
		clock.advance(time.Minute)
		l.polled()
		wantReason(t, l.check(), "unreachable")
		clear()
		if err := l.check(); err != nil {
			t.Fatalf("%s did not clear the outage: %v", name, err)
		}
	}
}

func TestLivenessBrokersDownKeepsFirstTimestamp(t *testing.T) {
	l, clock := newTestLiveness()
	l.start()
	l.assign()
	l.brokersDown()
	clock.advance(14 * time.Second)
	l.brokersDown() // a repeat report must not restart the grace
	clock.advance(2 * time.Second)
	l.polled()
	wantReason(t, l.check(), "unreachable for 16s")
}

func TestLivenessCommitPending(t *testing.T) {
	l, clock := newTestLiveness()
	l.start()
	l.assign()
	l.commitStarted()
	clock.advance(14 * time.Second)
	if err := l.check(); err != nil {
		t.Fatalf("within grace: %v", err)
	}
	clock.advance(2 * time.Second)
	wantReason(t, l.check(), "commit pending for 16s")
	l.commitEnded()
	if err := l.check(); err != nil {
		t.Fatalf("after commit returned: %v", err)
	}
}

func TestLivenessStopped(t *testing.T) {
	l, _ := newTestLiveness()
	l.start()
	l.assign()
	cause := errors.New("poison")
	l.stop(cause)
	err := l.check()
	wantReason(t, err, "stopped")
	if !errors.Is(err, cause) {
		t.Fatal("stop reason not wrapped")
	}
	l.stop(nil)
	wantReason(t, l.check(), "stopped")
	l.start()
	if err := l.check(); err != nil {
		t.Fatalf("restarted: %v", err)
	}
}

func TestLivenessRecreatedStartsUnassigned(t *testing.T) {
	l, clock := newTestLiveness()
	l.start()
	l.assign()
	l.recreated()
	clock.advance(61 * time.Second)
	l.polled()
	wantReason(t, l.check(), "no partition assignment")
	l.assign()
	if err := l.check(); err != nil {
		t.Fatalf("after assignment: %v", err)
	}
}

func TestBrokersConnected(t *testing.T) {
	cases := []struct {
		name      string
		stats     string
		up, known int
		bad       bool
	}{
		{"one up", `{"brokers":{"localhost:9092/bootstrap":{"nodeid":-1,"state":"UP"},"localhost:9092/1":{"nodeid":1,"state":"UP"}}}`, 1, 1, false},
		{"all down", `{"brokers":{"localhost:9092/bootstrap":{"nodeid":-1,"state":"DOWN"},"localhost:9092/1":{"nodeid":1,"state":"DOWN"}}}`, 0, 1, false},
		{"reconnecting", `{"brokers":{"a/1":{"nodeid":1,"state":"APIVERSION_QUERY"},"b/2":{"nodeid":2,"state":"UPDATE"}}}`, 1, 2, false},
		{"only bootstrap", `{"brokers":{"localhost:9092/bootstrap":{"nodeid":-1,"state":"UP"}}}`, 0, 0, false},
		{"garbage", `{`, 0, 0, true},
	}
	for _, c := range cases {
		up, known, err := brokersConnected(c.stats)
		if (err != nil) != c.bad || up != c.up || known != c.known {
			t.Errorf("%s: up=%d known=%d err=%v", c.name, up, known, err)
		}
	}
}
