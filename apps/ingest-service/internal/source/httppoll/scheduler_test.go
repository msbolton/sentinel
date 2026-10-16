package httppoll

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/aoi"
)

var box = aoi.BBox{South: 40, West: -90, North: 45, East: -80}

func waitFor(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

func run(t *testing.T, s *Scheduler) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = s.Run(ctx, nil) }()
	t.Cleanup(func() { cancel(); <-done })
	return cancel
}

func TestEffectiveInterval(t *testing.T) {
	base, maxInt := 10*time.Second, 60*time.Second
	for area, want := range map[float64]time.Duration{10: base, 400: base, 500: 20 * time.Second, 1500: 40 * time.Second, aoi.GlobalArea: maxInt} {
		if got := EffectiveInterval(base, maxInt, area, 400); got != want {
			t.Errorf("area %v: %v, want %v", area, got, want)
		}
	}
	if got := EffectiveInterval(base, maxInt, 5000, 0); got != base {
		t.Errorf("maxArea 0 must disable stretching, got %v", got)
	}
}

func TestIdleUntilAreaArrives(t *testing.T) {
	var polls atomic.Int32
	s := NewScheduler(Options{Interval: 10 * time.Millisecond, AreaRequired: true}, func(context.Context, aoi.Area) error { polls.Add(1); return nil })
	run(t, s)
	time.Sleep(100 * time.Millisecond)
	if polls.Load() != 0 || !s.Status().Idle {
		t.Fatalf("expected no polls without an area, got %d idle=%v", polls.Load(), s.Status().Idle)
	}
}

func TestAreaChangeWakesAndPolls(t *testing.T) {
	var polls atomic.Int32
	var seen atomic.Value
	s := NewScheduler(Options{Interval: time.Hour, MinSpacing: 0, AreaRequired: true}, func(_ context.Context, a aoi.Area) error {
		polls.Add(1)
		seen.Store(a)
		return nil
	})
	run(t, s)
	st := s.SetArea(aoi.Area{Active: true, Clients: 1, Boxes: []aoi.BBox{box}})
	if st.Idle || st.AreaSqDeg != 50 || !st.Active {
		t.Errorf("status after SetArea = %+v", st)
	}
	if !waitFor(func() bool { return polls.Load() == 1 }, time.Second) {
		t.Fatalf("expected one poll, got %d", polls.Load())
	}
	if got := seen.Load().(aoi.Area); len(got.Boxes) != 1 || got.Boxes[0] != box {
		t.Errorf("poll saw %+v", got)
	}
	time.Sleep(50 * time.Millisecond)
	if polls.Load() != 1 {
		t.Errorf("hour-long interval must not poll again, got %d", polls.Load())
	}
	// Heartbeat: same boxes, more clients → no poll.
	s.SetArea(aoi.Area{Active: true, Clients: 2, Boxes: []aoi.BBox{box}})
	time.Sleep(50 * time.Millisecond)
	if polls.Load() != 1 {
		t.Errorf("heartbeat must not poll, got %d", polls.Load())
	}
	if st := s.SetArea(aoi.Area{Active: false}); !st.Idle {
		t.Errorf("inactive area should idle, got %+v", st)
	}
	if lp := s.Status().LastPollAt; lp.IsZero() {
		t.Error("LastPollAt should be set")
	}
}

func TestExpiredLeaseIdles(t *testing.T) {
	var polls atomic.Int32
	s := NewScheduler(Options{Interval: 10 * time.Millisecond, AreaRequired: true}, func(context.Context, aoi.Area) error { polls.Add(1); return nil })
	run(t, s)
	s.SetArea(aoi.Area{Active: true, Boxes: []aoi.BBox{box}, ExpiresAt: time.Now().Add(60 * time.Millisecond)})
	if !waitFor(func() bool { return polls.Load() >= 2 }, time.Second) {
		t.Fatalf("expected repeated polls while the lease is fresh, got %d", polls.Load())
	}
	time.Sleep(150 * time.Millisecond)
	settled := polls.Load()
	time.Sleep(100 * time.Millisecond)
	if polls.Load() != settled {
		t.Errorf("polling continued after expiry: %d -> %d", settled, polls.Load())
	}
	if !s.Status().Idle {
		t.Error("expired lease should report idle")
	}
	// A fresh push (new expiry, same boxes) resumes polling.
	s.SetArea(aoi.Area{Active: true, Boxes: []aoi.BBox{box}, ExpiresAt: time.Now().Add(time.Minute)})
	if !waitFor(func() bool { return polls.Load() > settled }, time.Second) {
		t.Error("renewed lease should resume polling")
	}
}

func TestBackoffAndQuota(t *testing.T) {
	var polls atomic.Int32
	s := NewScheduler(Options{Interval: 10 * time.Second, MaxInterval: 60 * time.Second, CreditFloor: 200, MaxBackoff: time.Second}, func(context.Context, aoi.Area) error {
		if polls.Add(1) == 1 {
			return errors.New("boom")
		}
		return nil
	})
	if st := s.Status(); st.CreditsRemaining != -1 || st.LowCredits || st.EffectiveInterval != 10*time.Second {
		t.Errorf("initial status = %+v", st)
	}
	if low, changed := s.ReportQuota(500); low || changed {
		t.Error("500 credits is not low")
	}
	if low, changed := s.ReportQuota(150); !low || !changed || s.Status().EffectiveInterval != 60*time.Second {
		t.Errorf("150 credits should be low and slow: %v %v %v", low, changed, s.Status().EffectiveInterval)
	}
	run(t, s)
	if !waitFor(func() bool { return s.LastError() == "boom" }, time.Second) {
		t.Fatal("first poll error not recorded")
	}
	s.mu.Lock()
	b := s.backoff
	s.mu.Unlock()
	if b != time.Second {
		t.Errorf("backoff after one error = %v", b)
	}
}

func TestNotRequiredPollsImmediately(t *testing.T) {
	var polls atomic.Int32
	s := NewScheduler(Options{Interval: time.Hour}, func(context.Context, aoi.Area) error { polls.Add(1); return nil })
	run(t, s)
	if !waitFor(func() bool { return polls.Load() == 1 }, time.Second) {
		t.Fatalf("expected an immediate poll, got %d", polls.Load())
	}
	if s.Status().Idle {
		t.Error("never idle without AreaRequired")
	}
}
