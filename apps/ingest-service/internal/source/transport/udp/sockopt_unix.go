//go:build unix

package udp

import (
	"encoding/binary"
	"syscall"
	"unsafe"
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

// controlUint32Intact is controlUint32 for the control data of a read that
// returned flags. When the OS cut the control data short (MSG_CTRUNC: it had
// more than the buffer had room for) the data may end in the middle of a
// message, so none of it is read.
func controlUint32Intact(oob []byte, flags, level, typ int) (uint32, bool) {
	if flags&syscall.MSG_CTRUNC != 0 {
		return 0, false
	}
	return controlUint32(oob, level, typ)
}

// controlUint32 finds the control message of the given level and type in the
// control data of one datagram, and returns its 32-bit value. It is how the
// kernel hands over a socket's count of dropped datagrams (udp_linux.go). It
// reports false when there is no such message, or its data holds no value, or
// the data is not control messages a kernel wrote. It reads the messages where
// they lie and allocates nothing (syscall.ParseSocketControlMessage makes a
// slice of them), for it runs on every datagram once the socket has dropped one.
func controlUint32(oob []byte, level, typ int) (uint32, bool) {
	dataAt := syscall.CmsgLen(0) // a message's data starts after its header, padded
	for len(oob) >= dataAt {
		// The header is copied into a struct of the platform's own layout, so
		// the width and the place of each field are the compiler's (the length
		// is a uint64 on 64-bit Linux, a uint32 elsewhere). A copy does not mind
		// where the bytes lie; a pointer cast would, if they were not aligned.
		var h syscall.Cmsghdr
		copy(unsafe.Slice((*byte)(unsafe.Pointer(&h)), unsafe.Sizeof(h)), oob)
		n := uint64(h.Len)
		if n < uint64(dataAt) || n > uint64(len(oob)) {
			return 0, false // shorter than its own header, or running past the data
		}
		if int(h.Level) == level && int(h.Type) == typ && n-uint64(dataAt) >= 4 {
			return binary.NativeEndian.Uint32(oob[dataAt:]), true
		}
		// The next message starts where this one ends, padded to the alignment.
		next := syscall.CmsgSpace(int(n) - dataAt)
		if next >= len(oob) {
			break
		}
		oob = oob[next:]
	}
	return 0, false
}
