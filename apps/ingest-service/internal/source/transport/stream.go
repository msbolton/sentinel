package transport

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
	"time"

	"github.com/sentinel/ingest-service/internal/source/transport/framing"
)

// initialBuffer is the size a connection's scanner buffer starts at: scanBound
// capped at 64 KiB. A listener keeps one buffer per connection, so a small
// limit must not cost 64 KiB each. When a larger frame arrives the scanner
// doubles the buffer up to scanBound, which is the frame limit plus the varint
// allowance, not 64 KiB. Taking the minimum with scanBound is what keeps the
// buffer from starting above it: bufio would take a larger capacity as the
// token limit.
func initialBuffer(cfg Config) int {
	return min(64<<10, scanBound(cfg))
}

// scanBound is the largest token a connection's scanner may hold: the frame
// limit plus the room for a delimited token's varint prefix. It stops at
// math.MaxInt rather than wrapping negative, as the plain sum would for a limit
// within a varint of the int ceiling; sizing the buffer from a negative bound
// panics.
func scanBound(cfg Config) int {
	if m := cfg.MaxFrame(); m <= math.MaxInt-binary.MaxVarintLen64 {
		return m + binary.MaxVarintLen64
	}
	return math.MaxInt
}

// ScanConn reads frames from conn with the configured framer and emits each
// one until the connection ends, ctx is done, or emit refuses. The caller
// owns conn; ScanConn does not close it. readTimeout resets per read.
func ScanConn(ctx context.Context, conn net.Conn, cfg Config, emit Emit) error {
	split, err := framing.New(cfg.Framing, cfg.MaxFrame())
	if err != nil {
		return err
	}
	sc := bufio.NewScanner(conn)
	// The bound leaves room for the varint prefix a delimited token carries.
	// The delimited and length-prefix framers enforce the frame limit
	// themselves; for the others (line, xmlevent, beast) this bound is the
	// limit.
	sc.Buffer(make([]byte, 0, initialBuffer(cfg)), scanBound(cfg))
	sc.Split(split)
	remote := ""
	if conn.RemoteAddr() != nil {
		remote = conn.RemoteAddr().String()
	}
	timeout := time.Duration(cfg.ReadTimeoutSec) * time.Second
	for {
		if timeout > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(timeout))
		}
		if !sc.Scan() {
			err := sc.Err()
			if err == nil || errors.Is(err, io.EOF) || ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		b := sc.Bytes()
		if len(b) == 0 {
			continue
		}
		payload := make([]byte, len(b))
		copy(payload, b)
		if err := emit(ctx, Frame{Payload: payload, ReceivedAt: time.Now().UTC(), Remote: remote}); err != nil {
			if errors.Is(err, ErrBackpressure) {
				continue // the policy already counted the drop
			}
			return err
		}
	}
}

// Backoff grows a reconnect delay from 1 s to max.
func Backoff(cur, max time.Duration) time.Duration {
	if cur <= 0 {
		return time.Second
	}
	cur *= 2
	if cur > max {
		return max
	}
	return cur
}

// Sleep waits d or until ctx is done.
func Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
