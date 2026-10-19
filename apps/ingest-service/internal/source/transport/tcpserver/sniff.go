package tcpserver

import (
	"errors"
	"net"
)

// errTLSClient is what a sniffConn's first Read returns when the peer opens
// with a TLS record header: a TLS client talking to a plain listener.
var errTLSClient = errors.New("tcpserver: TLS client on a plain listener")

// sniffConn wraps the connection of a plain listener and checks the first
// bytes for a TLS record header (0x16 0x03 0x00-0x04), so a ClientHello is
// closed and counted instead of being framed as data. Bytes read for the
// check are handed to the reader unchanged.
//
// Record headers are rare as the start of real data: line and XML data is
// text, Beast starts with 0x1a, a protobuf message never starts with a
// field-0 tag (0x00-0x07), and a 2-byte length prefix of 0x1603 is a
// 5635-byte frame whose first byte would be 0x00-0x04.
type sniffConn struct {
	net.Conn
	sniffed bool
	pending []byte // bytes read for the check, not yet handed on
	err     error  // the read error that ended the check, returned after pending
}

func (c *sniffConn) Read(p []byte) (int, error) {
	if !c.sniffed {
		c.sniffed = true
		var head [3]byte
		n := 0
		for {
			m, err := c.Conn.Read(head[n:])
			n += m
			if isTLS, decided := tlsVerdict(head[:n]); isTLS {
				return 0, errTLSClient
			} else if decided || err != nil {
				c.pending, c.err = append([]byte(nil), head[:n]...), err
				break
			}
		}
	}
	if len(c.pending) > 0 {
		n := copy(p, c.pending)
		c.pending = c.pending[n:]
		return n, nil
	}
	if c.err != nil {
		return 0, c.err
	}
	return c.Conn.Read(p)
}

// tlsVerdict says whether b starts a TLS record header and whether b is
// enough to tell. It decides as soon as a byte rules the header out, so a
// short plain message is never held back waiting for a third byte.
func tlsVerdict(b []byte) (isTLS, decided bool) {
	switch {
	case len(b) >= 1 && b[0] != 0x16:
		return false, true
	case len(b) >= 2 && b[1] != 0x03:
		return false, true
	case len(b) >= 3:
		return b[2] <= 0x04, true
	}
	return false, false
}
