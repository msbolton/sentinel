package feed

import (
	"sync"

	"github.com/sentinel/ingest-service/internal/registry"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

// exerciseFlags is what the supervisor knows of each listed connection's
// exercise marking, read from the same listing every reconcile makes. It is the
// driver.FeedSource the drivers get, so one that reports on a connection's
// behalf (the emplacement mirror) follows an edit of the connection without a
// restart.
type exerciseFlags struct {
	mu      sync.Mutex
	flags   map[string]bool
	subs    map[int]func(string)
	nextSub int
}

var _ driver.FeedSource = (*exerciseFlags)(nil)

func newExerciseFlags() *exerciseFlags {
	return &exerciseFlags{flags: map[string]bool{}, subs: map[int]func(string){}}
}

// Exercise implements driver.FeedSource.
func (f *exerciseFlags) Exercise(feedID string) (exercise, known bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	exercise, known = f.flags[feedID]
	return exercise, known
}

// OnExerciseChange implements driver.FeedSource.
func (f *exerciseFlags) OnExerciseChange(fn func(string)) (cancel func()) {
	f.mu.Lock()
	id := f.nextSub
	f.nextSub++
	f.subs[id] = fn
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		delete(f.subs, id)
		f.mu.Unlock()
	}
}

// set replaces the flags with the listing's and tells the subscribers each
// connection whose effective marking changed: one that became or stopped being
// an exercise connection, by an edit, an add or a delete.
func (f *exerciseFlags) set(feeds []registry.Feed) {
	next := make(map[string]bool, len(feeds))
	for _, feed := range feeds {
		next[feed.ID] = feed.Common().Exercise
	}
	f.mu.Lock()
	var changed []string
	for id, ex := range next {
		if f.flags[id] != ex { // an unlisted connection counts as not exercise
			changed = append(changed, id)
		}
	}
	for id, ex := range f.flags {
		if _, ok := next[id]; !ok && ex {
			changed = append(changed, id)
		}
	}
	f.flags = next
	fns := make([]func(string), 0, len(f.subs))
	for _, fn := range f.subs {
		fns = append(fns, fn)
	}
	f.mu.Unlock()
	for _, id := range changed {
		for _, fn := range fns {
			fn(id)
		}
	}
}
