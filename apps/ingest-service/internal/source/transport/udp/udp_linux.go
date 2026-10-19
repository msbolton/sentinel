//go:build linux

package udp

import (
	"net"
	"net/netip"
	"sync/atomic"
	"syscall"
)

// datagramReader reads a socket's datagrams and, with them, the count of
// datagrams the kernel dropped because the socket's receive buffer was full.
// Such a datagram never reaches the listener, so the kernel is the only one
// who can say it was sent.
type datagramReader struct {
	conn    *net.UDPConn
	oob     []byte        // room for the kernel's drop count; nil when the kernel was not asked for it
	dropped *atomic.Int64 // where the latest count is kept
}

// newDatagramReader asks the kernel to report drops (SO_RXQ_OVFL), after which
// it attaches the socket's running total of dropped datagrams to each datagram
// it delivers once there has been a drop. If the kernel refuses, reads are
// plain and the count stays 0.
func newDatagramReader(conn *net.UDPConn, dropped *atomic.Int64) *datagramReader {
	r := &datagramReader{conn: conn, dropped: dropped}
	if enableDropCount(conn) == nil {
		r.oob = make([]byte, syscall.CmsgSpace(4)) // one message, holding a uint32
	}
	return r
}

// enableDropCount sets SO_RXQ_OVFL on c's socket.
func enableDropCount(c syscall.Conn) error {
	rc, err := c.SyscallConn()
	if err != nil {
		return err
	}
	var sockErr error
	if err := rc.Control(func(fd uintptr) {
		sockErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RXQ_OVFL, 1)
	}); err != nil {
		return err
	}
	return sockErr
}

// read returns the next datagram as ReadFromUDPAddrPort does, and stores the
// socket's drop count if the datagram carries one. The count is the socket's
// total since it opened, so it is stored, not added; and it arrives with the
// next datagram that gets through, so it can trail the drops by that long.
func (r *datagramReader) read(buf []byte) (int, netip.AddrPort, error) {
	if r.oob == nil {
		return r.conn.ReadFromUDPAddrPort(buf)
	}
	n, oobn, _, ap, err := r.conn.ReadMsgUDPAddrPort(buf, r.oob)
	if oobn > 0 {
		if total, ok := controlUint32(r.oob[:oobn], syscall.SOL_SOCKET, syscall.SO_RXQ_OVFL); ok {
			r.dropped.Store(int64(total))
		}
	}
	return n, ap, err
}
