package transport

import "sync"

// StateTracker is a mutex-guarded State for transports to embed.
type StateTracker struct {
	mu    sync.Mutex
	state State
}

// Set mutates the state under the lock.
func (t *StateTracker) Set(fn func(*State)) {
	t.mu.Lock()
	fn(&t.state)
	t.mu.Unlock()
}

// Get returns a copy of the state.
func (t *StateTracker) Get() State {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}
