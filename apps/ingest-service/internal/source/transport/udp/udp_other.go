//go:build !linux

package udp

import (
	"net"
	"net/netip"
	"sync/atomic"
)

// datagramReader reads a socket's datagrams. Only Linux can tell a listener how
// many datagrams the kernel dropped from a full receive buffer (udp_linux.go),
// so here the count is never stored and stays 0.
type datagramReader struct{ conn *net.UDPConn }

func newDatagramReader(conn *net.UDPConn, _ *atomic.Int64) *datagramReader {
	return &datagramReader{conn: conn}
}

// read returns the next datagram.
func (r *datagramReader) read(buf []byte) (int, netip.AddrPort, error) {
	return r.conn.ReadFromUDPAddrPort(buf)
}
