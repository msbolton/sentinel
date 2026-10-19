//go:build unix

package udp

import (
	"encoding/binary"
	"syscall"
)

// receiveBufferSize reports the size of c's receive buffer as the OS keeps it,
// which is what the socket has whatever it was asked for: Linux holds twice
// the ask (the other half is its bookkeeping) and caps the ask at
// net.core.rmem_max, and macOS caps it at kern.ipc.maxsockbuf.
func receiveBufferSize(c syscall.Conn) (int, error) {
	rc, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var size int
	var sockErr error
	if err := rc.Control(func(fd uintptr) {
		size, sockErr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF)
	}); err != nil {
		return 0, err
	}
	return size, sockErr
}

// controlUint32 finds the control message of the given level and type in the
// control data of one datagram, and returns its 32-bit value. It is how the
// kernel hands over a socket's count of dropped datagrams (udp_linux.go). It
// reports false when there is no such message, or its data holds no value.
func controlUint32(oob []byte, level, typ int) (uint32, bool) {
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return 0, false
	}
	for _, m := range msgs {
		if int(m.Header.Level) == level && int(m.Header.Type) == typ && len(m.Data) >= 4 {
			return binary.NativeEndian.Uint32(m.Data), true
		}
	}
	return 0, false
}
