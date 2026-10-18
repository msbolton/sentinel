package transport

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"testing"
	"time"
)

// A delimited frame whose payload is exactly MaxFrameBytes must be emitted.
// The scanner token includes the varint prefix, so its bound has to leave
// room for it. The 64 KiB case is the scanner's initial buffer size, so a
// frame that fills it is the one that hits the bound.
func TestScanConnDelimitedEmitsMaxSizeFrame(t *testing.T) {
	for _, max := range []int{100, 64 << 10} {
		t.Run(fmt.Sprint(max), func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			cfg := Config{Framing: "delimited", MaxFrameBytes: max}

			got := make(chan []byte, 1)
			emit := func(_ context.Context, f Frame) error {
				got <- f.Payload
				return nil
			}
			done := make(chan error, 1)
			go func() { done <- ScanConn(context.Background(), server, cfg, emit) }()

			payload := bytes.Repeat([]byte{'m'}, max)
			frame := binary.AppendUvarint(nil, uint64(len(payload)))
			frame = append(frame, payload...)
			go func() { _, _ = client.Write(frame) }()

			select {
			case p := <-got:
				if !bytes.Equal(p, payload) {
					t.Fatalf("payload = %d bytes, want %d", len(p), len(payload))
				}
			case err := <-done:
				t.Fatalf("ScanConn returned %v before emitting the frame", err)
			case <-time.After(2 * time.Second):
				t.Fatal("frame not emitted")
			}
			client.Close()
			if err := <-done; err != nil {
				t.Errorf("ScanConn after EOF = %v", err)
			}
		})
	}
}
