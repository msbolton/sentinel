package transport

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"time"

	"github.com/sentinel/ingest-service/internal/source/transport/framing"
)

// initialBuffer is the size a connection's scanner buffer starts at: the frame
// limit plus the room for a delimited token's varint prefix, capped at 64 KiB.
// A listener keeps one buffer per connection, so a small limit must not cost
// 64 KiB each. The scanner doubles the buffer up to the same bound when a
// larger frame arrives. The buffer never starts above that bound, because
// bufio would then take its capacity as the token limit.
func initialBuffer(cfg Config) int {
	return min(64<<10, cfg.MaxFrame()+binary.MaxVarintLen64)
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
	// A delimited token carries its varint prefix, so the bound leaves room
	// for it; the framers enforce the frame limit themselves.
	sc.Buffer(make([]byte, 0, initialBuffer(cfg)), cfg.MaxFrame()+binary.MaxVarintLen64)
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
