package ingesttoken

import (
	"testing"
	"time"
)

// within polls cond for up to five seconds.
func within(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Exports for the external tests (package ingesttoken_test), which can import
// the registry package; the internal ones cannot, because registry imports
// driver and driver imports this package.
const FeedsChannel = feedsChannel

var (
	TestPool = testPool
	NewToken = newToken
	Within   = within
)
