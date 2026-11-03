package kafka

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// liveness is what a batch consumer knows about its own health: whether its
// loop is running and polling, whether the group has given it partitions,
// and whether librdkafka can reach a broker. It is fed from the consume loop
// and read by the service's readiness check, and it has no broker of its
// own, so it is tested with a fake clock.
type liveness struct {
	now             func() time.Time
	staleAfter      time.Duration
	disconnectGrace time.Duration

	mu              sync.Mutex
	started         bool
	stopped         bool
	stopErr         error
	lastPoll        time.Time
	assigned        bool
	unassignedSince time.Time
	downSince       time.Time
	commitSince     time.Time // a synchronous commit in flight since then
	recreations     int
}

func newLiveness(staleAfter, disconnectGrace time.Duration) *liveness {
	return &liveness{now: time.Now, staleAfter: staleAfter, disconnectGrace: disconnectGrace}
}

// start marks the loop running. Until the first assignment arrives the
// consumer counts as unassigned from this moment.
func (l *liveness) start() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.started = true
	l.stopped = false
	l.stopErr = nil
	l.lastPoll = now
	if !l.assigned {
		l.unassignedSince = now
	}
}

// stop records that Run returned. err is nil for an orderly shutdown.
func (l *liveness) stop(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopped = true
	l.stopErr = err
}

// polled records that the loop asked the client for an event.
func (l *liveness) polled() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lastPoll = l.now()
}

// progressed records something only a reachable broker can deliver: a
// message, a successful commit. It clears any outage.
func (l *liveness) progressed() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.downSince = time.Time{}
}

// commitStarted records that a synchronous commit began. librdkafka blocks
// the call while the coordinator is away, and nothing else is observed
// meanwhile, so the time it has taken is the outage signal.
func (l *liveness) commitStarted() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.commitSince = l.now()
}

// commitEnded records that the commit returned, however it went.
func (l *liveness) commitEnded() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.commitSince = time.Time{}
}

// assign records a partition assignment from the group coordinator, which
// also proves the broker is reachable.
func (l *liveness) assign() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.assigned = true
	l.downSince = time.Time{}
}

// revoke records that the group took the partitions back.
func (l *liveness) revoke() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.assigned {
		l.assigned = false
		l.unassignedSince = l.now()
	}
}

// brokersDown records that the client reports no broker connection.
func (l *liveness) brokersDown() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.downSince.IsZero() {
		l.downSince = l.now()
	}
}

// brokersUp records that the client reports at least one broker connection.
func (l *liveness) brokersUp() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.downSince = time.Time{}
}

// recreated records that the client was replaced after a fatal error. The
// new client starts unassigned.
func (l *liveness) recreated() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recreations++
	l.assigned = false
	l.unassignedSince = l.now()
}

// check reports why the consumer is not healthy, or nil.
func (l *liveness) check() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	switch {
	case !l.started:
		return errors.New("kafka consumer not started")
	case l.stopped && l.stopErr != nil:
		return fmt.Errorf("kafka consumer stopped: %w", l.stopErr)
	case l.stopped:
		return errors.New("kafka consumer stopped")
	}
	if !l.downSince.IsZero() {
		if d := now.Sub(l.downSince); d > l.disconnectGrace {
			return fmt.Errorf("kafka brokers unreachable for %s", d.Round(time.Second))
		}
	}
	if !l.commitSince.IsZero() {
		if d := now.Sub(l.commitSince); d > l.disconnectGrace {
			return fmt.Errorf("kafka offset commit pending for %s", d.Round(time.Second))
		}
	}
	if d := now.Sub(l.lastPoll); d > l.staleAfter {
		return fmt.Errorf("kafka consumer has not polled for %s", d.Round(time.Second))
	}
	if !l.assigned {
		if d := now.Sub(l.unassignedSince); d > l.staleAfter {
			return fmt.Errorf("kafka consumer has had no partition assignment for %s", d.Round(time.Second))
		}
	}
	return nil
}

// brokersConnected reads librdkafka's statistics JSON and reports how many
// of the brokers it knows by node id are connected (state UP or UPDATE) and
// how many it knows. Bootstrap entries (node id -1) are left out: they are
// the configured addresses, not brokers the client has learned.
func brokersConnected(stats string) (up, known int, err error) {
	var doc struct {
		Brokers map[string]struct {
			NodeID int    `json:"nodeid"`
			State  string `json:"state"`
		} `json:"brokers"`
	}
	if err := json.Unmarshal([]byte(stats), &doc); err != nil {
		return 0, 0, err
	}
	for _, b := range doc.Brokers {
		if b.NodeID < 0 {
			continue
		}
		known++
		if b.State == "UP" || b.State == "UPDATE" {
			up++
		}
	}
	return up, known, nil
}
