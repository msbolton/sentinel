package udp

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/source/transport"
)

// A datagram its sender's bucket turns away is the flood case, so it costs as
// little as it can: it is counted per message, but no remote text is built for
// it and the datagram is not cut into slices. What it still costs is the
// sender's text, the name of its row and the key of its bucket.
func TestRefusedDatagramBuildsNoRemoteOrParts(t *testing.T) {
	tr, err := New(transport.Config{Kind: "udp", Address: "127.0.0.1:0", Framing: "line", MaxMessagesPerSecPerSender: 1})
	if err != nil {
		t.Fatal(err)
	}
	l := tr.(*Listener)
	ap := netip.MustParseAddrPort("10.0.0.9:5000")
	data := []byte("a\nb\nc\nd\n")
	var emitted int
	emit := func(context.Context, transport.Frame) error { emitted++; return nil }
	now := time.Now()

	// The first datagram spends the sender's one token on its first message.
	if err := l.deliver(context.Background(), ap, data, now, emit); err != nil {
		t.Fatal(err)
	}
	if emitted != 1 || l.counts.Accepted.Load() != 1 || l.counts.SenderRateLimited.Load() != 3 {
		t.Fatalf("first datagram: emitted %d, accepted %d, senderRateLimited %d; want 1, 1, 3", emitted, l.counts.Accepted.Load(), l.counts.SenderRateLimited.Load())
	}

	before := l.counts.SenderRateLimited.Load()
	allocs := testing.AllocsPerRun(100, func() { _ = l.deliver(context.Background(), ap, data, now, emit) })
	if allocs > 1 {
		t.Errorf("a refused datagram allocated %v times per call, want at most 1 (the sender's text)", allocs)
	}
	if emitted != 1 {
		t.Errorf("a refused datagram emitted %d frames", emitted-1)
	}
	if got := l.counts.SenderRateLimited.Load() - before; got != 4*101 { // 101 calls (AllocsPerRun warms up once), 4 messages each
		t.Errorf("senderRateLimited grew by %d, want %d: every message of a refused datagram is counted", got, 4*101)
	}

	// A datagram that is taken builds its remote and parts, as it always did.
	taken := testing.AllocsPerRun(20, func() {
		l.perSender = nil // no bucket: everything passes
		_ = l.deliver(context.Background(), ap, data, now, emit)
	})
	if taken <= allocs {
		t.Errorf("a taken datagram allocated %v times per call, a refused one %v: the refused one should be the cheaper", taken, allocs)
	}
}
