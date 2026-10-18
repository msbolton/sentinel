package transport

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/source/transport/framing"
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

// firstReadConn records how large a buffer the scanner first offers to Read.
// That is the scanner's initial buffer, which bufio does not otherwise expose.
type firstReadConn struct {
	net.Conn
	once sync.Once
	size chan int // buffered, so the first Read never waits on the test
}

func (c *firstReadConn) Read(p []byte) (int, error) {
	c.once.Do(func() { c.size <- len(p) })
	return c.Conn.Read(p)
}

// A connection's read buffer starts at the smaller of 64 KiB and its frame
// limit plus the room a varint prefix needs. A listener keeps one buffer per
// connection, so a small limit must not cost 64 KiB each; the scanner doubles
// the buffer, up to that bound, when a frame needs more.
func TestScanConnInitialBufferFollowsMaxFrame(t *testing.T) {
	const kib64 = 64 << 10
	cases := []struct {
		name string
		max  int // MaxFrameBytes; 0 selects the 1 MiB default
		want int // the scanner's initial buffer, in bytes
	}{
		{"tiny limit", 1, 1 + binary.MaxVarintLen64},
		{"small limit", 256, 256 + binary.MaxVarintLen64},
		{"one byte under 64 KiB", kib64 - binary.MaxVarintLen64 - 1, kib64 - 1},
		{"exactly 64 KiB", kib64 - binary.MaxVarintLen64, kib64},
		{"one byte over 64 KiB", kib64 - binary.MaxVarintLen64 + 1, kib64},
		{"large limit", 1 << 20, kib64},
		{"default limit", 0, kib64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			conn := &firstReadConn{Conn: server, size: make(chan int, 1)}
			cfg := Config{Framing: framing.Delimited, MaxFrameBytes: tc.max}
			discard := func(context.Context, Frame) error { return nil }
			done := make(chan error, 1)
			go func() { done <- ScanConn(context.Background(), conn, cfg, discard) }()

			select {
			case got := <-conn.size:
				if got != tc.want {
					t.Errorf("initial buffer = %d bytes, want %d", got, tc.want)
				}
			case err := <-done:
				t.Fatalf("ScanConn returned %v before reading", err)
			case <-time.After(2 * time.Second):
				t.Fatal("ScanConn did not read from the connection")
			}
			client.Close()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("ScanConn after EOF = %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("ScanConn did not return after EOF")
			}
		})
	}
}

// scanStream runs ScanConn over stream, sent on a pipe that closes after the
// last byte, and returns each payload it emitted with ScanConn's own error.
func scanStream(t *testing.T, cfg Config, stream []byte) ([][]byte, error) {
	t.Helper()
	client, server := net.Pipe()
	defer server.Close() // frees the writer if ScanConn stops reading early
	go func() {
		_, _ = client.Write(stream)
		client.Close()
	}()
	var frames [][]byte
	emit := func(_ context.Context, f Frame) error {
		frames = append(frames, f.Payload)
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- ScanConn(context.Background(), server, cfg, emit) }()
	select {
	case err := <-done:
		return frames, err
	case <-time.After(5 * time.Second):
		t.Fatal("ScanConn did not finish")
		return nil, nil
	}
}

// With a 256-byte limit the connection's buffer holds little more than one
// frame, so a run of frames at the limit has to be shifted down and refilled
// between reads. A frame of exactly the limit passes and one byte more is
// oversize, which the framers decide from the prefix, before the payload.
func TestScanConnSmallMaxFrameReadsFullFrames(t *testing.T) {
	const limit = 256
	framers := []struct {
		name   string
		encode func(payload []byte) []byte
	}{
		{framing.Delimited, func(p []byte) []byte {
			return append(binary.AppendUvarint(nil, uint64(len(p))), p...)
		}},
		{framing.LengthPrefix, func(p []byte) []byte {
			return append(binary.BigEndian.AppendUint16(nil, uint16(len(p))), p...)
		}},
	}
	for _, f := range framers {
		cfg := Config{Framing: f.name, MaxFrameBytes: limit}

		t.Run(f.name+"/frames at the limit pass", func(t *testing.T) {
			var want [][]byte
			var stream []byte
			for i, size := range []int{limit, 1, limit, limit - 1, limit} {
				payload := bytes.Repeat([]byte{byte('a' + i)}, size)
				want = append(want, payload)
				stream = append(stream, f.encode(payload)...)
			}
			got, err := scanStream(t, cfg, stream)
			if err != nil {
				t.Fatalf("ScanConn = %v", err)
			}
			if len(got) != len(want) {
				t.Fatalf("emitted %d frames, want %d", len(got), len(want))
			}
			for i := range want {
				if !bytes.Equal(got[i], want[i]) {
					t.Errorf("frame %d = %d bytes, want %d", i, len(got[i]), len(want[i]))
				}
			}
		})

		t.Run(f.name+"/one byte over the limit is oversize", func(t *testing.T) {
			atLimit := bytes.Repeat([]byte{'a'}, limit)
			stream := append(f.encode(atLimit), f.encode(bytes.Repeat([]byte{'b'}, limit+1))...)
			got, err := scanStream(t, cfg, stream)
			if !errors.Is(err, framing.ErrFrameTooLong) {
				t.Errorf("ScanConn = %v, want %v", err, framing.ErrFrameTooLong)
			}
			if len(got) != 1 || !bytes.Equal(got[0], atLimit) {
				t.Errorf("emitted %d frames, want only the one at the limit", len(got))
			}
		})
	}
}
