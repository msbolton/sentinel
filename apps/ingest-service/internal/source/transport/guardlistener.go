package transport

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// GuardListener admits the connections a listener's guard allows and
// closes the rest itself, counting them. A server that serves it (grpc)
// never sees an Accept error for a refused sender: grpc's Serve would stop
// on one, and with it every other sender's stream.
type GuardListener struct {
	net.Listener
	allow   Allowlist
	max     int
	counts  *ListenCounters
	senders *Senders
	live    atomic.Int64
}

// NewGuardListener wraps inner. maxConnections 0 means no cap; senders may
// be nil.
func NewGuardListener(inner net.Listener, allow Allowlist, maxConnections int, counts *ListenCounters, senders *Senders) *GuardListener {
	return &GuardListener{Listener: inner, allow: allow, max: maxConnections, counts: counts, senders: senders}
}

// Accept returns the next admitted connection, or the inner listener's
// error (closed, say). Refused connections are closed and counted:
// rejected outside the allowlist, refused over the cap.
func (l *GuardListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		remote := c.RemoteAddr().String()
		if !l.allow.Allows(remote) {
			l.counts.Rejected.Add(1)
			c.Close()
			continue
		}
		// Accept runs on one goroutine, so the check and the add do not race
		// with another admission; a close only lowers the count.
		if l.max > 0 && l.live.Load() >= int64(l.max) {
			l.counts.Refused.Add(1)
			c.Close()
			continue
		}
		l.live.Add(1)
		if l.senders != nil {
			l.senders.Connect(remote, time.Now().UTC())
		}
		return &guardConn{Conn: c, release: func() {
			l.live.Add(-1)
			if l.senders != nil {
				l.senders.Disconnect(remote)
			}
		}}, nil
	}
}

// Live is the number of admitted connections not yet closed.
func (l *GuardListener) Live() int64 { return l.live.Load() }

// guardConn gives its place back once, however often it is closed.
type guardConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *guardConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}
