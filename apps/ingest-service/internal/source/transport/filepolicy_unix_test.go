//go:build unix

package transport

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A FIFO is not a file to read: opening it for reading would wait for a
// writer, and New with it would never come back. It is refused at once.
func TestReadUnderRefusesAFIFO(t *testing.T) {
	allowed, _ := fileTree(t)
	fifo := filepath.Join(allowed, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("no FIFOs here: %v", err)
	}
	done := make(chan error, 1)
	go func() { _, err := ReadUnder(fifo, []string{allowed}); done <- err }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("a FIFO: %v, want it refused as not a regular file", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ReadUnder of a FIFO blocked")
	}
}
