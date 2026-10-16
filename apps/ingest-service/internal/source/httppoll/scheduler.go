package httppoll

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/sentinel/ingest-service/internal/aoi"
)

// Options tune a scheduler. Zero values take the defaults.
type Options struct {
	Interval    time.Duration // base poll interval (10 s)
	MaxInterval time.Duration // slowest interval when stretched (60 s)
	MinSpacing  time.Duration // shortest gap after an area change (3 s)
	MaxBackoff  time.Duration // error backoff cap (60 s)
	// MaxArea is the area (sq deg) up to which the base interval applies;
	// beyond it the interval stretches by ceil(area / MaxArea). 0 disables.
	MaxArea float64
	// AreaRequired idles the scheduler until an active, unexpired area is
	// set (viewport mode). Without it the scheduler polls from the start
	// with whatever area it has (none = global).
	AreaRequired bool
	// CreditFloor slows polling to MaxInterval while the remote quota
	// (from ReportQuota) is below it. 0 disables.
	CreditFloor int
}

func (o Options) withDefaults() Options {
	if o.Interval <= 0 {
		o.Interval = 10 * time.Second
	}
	if o.MaxInterval <= 0 {
		o.MaxInterval = 60 * time.Second
	}
	if o.MaxInterval < o.Interval {
		o.MaxInterval = o.Interval
	}
	if o.MinSpacing < 0 {
		o.MinSpacing = 0
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = 60 * time.Second
	}
	return o
}

// Poll is called for every due poll with the current area; it returns an
// error to trigger backoff. It must honour ctx.
type Poll func(ctx context.Context, area aoi.Area) error

// Scheduler runs a poll on a schedule shaped by the area of interest. It is
// a timer loop rather than a ticker so an area change can pull the next
// poll forward and an idle period costs nothing.
type Scheduler struct {
	opts Options
	poll Poll

	mu            sync.RWMutex
	area          aoi.Area
	lastPollAt    time.Time // last successful poll
	lastAttemptAt time.Time // last attempt, success or not
	dirty         bool      // area changed since the last attempt
	credits       int       // -1 unknown
	backoff       time.Duration
	lastErr       string
	polls         int64

	wake chan struct{}
	now  func() time.Time
}

// NewScheduler builds a scheduler for poll.
func NewScheduler(opts Options, poll Poll) *Scheduler {
	return &Scheduler{opts: opts.withDefaults(), poll: poll, credits: -1, wake: make(chan struct{}, 1), now: time.Now}
}

// SetArea replaces the polled area. A change wakes the loop so the new
// area is fetched after at most the minimum spacing; an unchanged push
// refreshes the lease (expiry) without a poll.
func (s *Scheduler) SetArea(a aoi.Area) aoi.Status {
	s.mu.Lock()
	changed := !s.area.Equal(a)
	wasIdle := s.idleLocked()
	s.area = a
	if changed {
		s.dirty = true
	}
	nowIdle := s.idleLocked()
	st := s.statusLocked()
	s.mu.Unlock()
	if changed || wasIdle != nowIdle {
		s.Wake()
	}
	return st
}

// Wake nudges the loop to recompute its schedule.
func (s *Scheduler) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Area returns the current area.
func (s *Scheduler) Area() aoi.Area {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.area
}

// Status reports the area and timing.
func (s *Scheduler) Status() aoi.Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.statusLocked()
}

// ReportQuota records the remote's remaining quota from a response.
func (s *Scheduler) ReportQuota(remaining int) (nowLow bool, changed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wasLow := s.lowCreditsLocked()
	s.credits = remaining
	nowLow = s.lowCreditsLocked()
	return nowLow, nowLow != wasLow
}

// LastError is the last poll error, "" after a success.
func (s *Scheduler) LastError() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastErr
}

// Polls counts successful polls.
func (s *Scheduler) Polls() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.polls
}

func (s *Scheduler) statusLocked() aoi.Status {
	st := aoi.Status{
		Active: s.area.Active, Clients: s.area.Clients, Boxes: append([]aoi.BBox(nil), s.area.Boxes...),
		AreaSqDeg: s.area.SqDeg(), EffectiveInterval: s.effectiveIntervalLocked(), LastPollAt: s.lastPollAt,
		ExpiresAt: s.area.ExpiresAt, Idle: s.idleLocked(), CreditsRemaining: s.credits, LowCredits: s.lowCreditsLocked(),
	}
	if !st.Idle {
		if wait := s.nextAttemptLocked().Sub(s.now()); wait > 0 {
			st.NextPollIn = wait
		}
	}
	return st
}

func (s *Scheduler) idleLocked() bool {
	if !s.opts.AreaRequired {
		return false
	}
	return !s.area.Active || len(s.area.Boxes) == 0 || s.area.Expired(s.now())
}

func (s *Scheduler) lowCreditsLocked() bool {
	return s.opts.CreditFloor > 0 && s.credits >= 0 && s.credits < s.opts.CreditFloor
}

func (s *Scheduler) effectiveIntervalLocked() time.Duration {
	if s.lowCreditsLocked() {
		return s.opts.MaxInterval
	}
	return EffectiveInterval(s.opts.Interval, s.opts.MaxInterval, s.area.SqDeg(), s.opts.MaxArea)
}

// nextAttemptLocked returns when the next poll is due: immediately before
// the first attempt, after the minimum spacing when the area changed, else
// after the stretched interval plus any error backoff.
func (s *Scheduler) nextAttemptLocked() time.Time {
	if s.lastAttemptAt.IsZero() {
		return s.now()
	}
	wait := s.effectiveIntervalLocked()
	if s.dirty {
		wait = s.opts.MinSpacing
	}
	return s.lastAttemptAt.Add(wait + s.backoff)
}

// EffectiveInterval stretches the base interval for large areas: up to
// maxArea the base applies, beyond it the interval grows with ceil(area /
// maxArea), capped at maxInterval.
func EffectiveInterval(base, maxInterval time.Duration, area, maxArea float64) time.Duration {
	if maxArea <= 0 || area <= maxArea {
		return base
	}
	stretched := time.Duration(float64(base) * math.Ceil(area/maxArea))
	if stretched > maxInterval {
		return maxInterval
	}
	return stretched
}

// Run polls until ctx is done. onIdle, when non-nil, is called with true
// when the loop starts idling and false when it resumes.
func (s *Scheduler) Run(ctx context.Context, onIdle func(bool)) error {
	idleLogged := false
	for {
		s.mu.Lock()
		idle := s.idleLocked()
		next := s.nextAttemptLocked()
		s.mu.Unlock()

		if idle {
			if !idleLogged && onIdle != nil {
				onIdle(true)
			}
			idleLogged = true
			select {
			case <-ctx.Done():
				return nil
			case <-s.wake:
				continue
			}
		}
		if idleLogged && onIdle != nil {
			onIdle(false)
		}
		idleLogged = false

		timer := time.NewTimer(next.Sub(s.now()))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-s.wake:
			timer.Stop()
			continue
		case <-timer.C:
		}

		s.mu.Lock()
		area := s.area
		s.lastAttemptAt = s.now()
		s.dirty = false
		s.mu.Unlock()

		err := s.poll(ctx, area)
		if ctx.Err() != nil {
			return nil
		}
		s.mu.Lock()
		if err != nil {
			s.lastErr = err.Error()
			if s.backoff == 0 {
				s.backoff = time.Second
			} else if s.backoff *= 2; s.backoff > s.opts.MaxBackoff {
				s.backoff = s.opts.MaxBackoff
			}
		} else {
			s.lastErr = ""
			s.backoff = 0
			s.lastPollAt = s.now()
			s.polls++
		}
		s.mu.Unlock()
	}
}
