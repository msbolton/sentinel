//go:build unix

package udp

import (
	"encoding/binary"
	"math"
	"net"
	"syscall"
	"testing"
	"unsafe"
)

// controlMessage lays out one control message as the kernel hands it over: a
// header naming its level and type, then its data, padded to the alignment of
// the platform.
func controlMessage(level, typ int, data []byte) []byte {
	b := make([]byte, syscall.CmsgSpace(len(data)))
	h := (*syscall.Cmsghdr)(unsafe.Pointer(&b[0]))
	h.Level, h.Type = int32(level), int32(typ)
	h.SetLen(syscall.CmsgLen(len(data)))
	copy(b[syscall.CmsgLen(0):], data)
	return b
}

// controlUint32 finds the 32-bit value a datagram's control data carries at a
// level and type, which is how the kernel hands over a socket's drop count.
func TestControlUint32(t *testing.T) {
	const level, typ = syscall.SOL_SOCKET, 42
	u32 := func(v uint32) []byte {
		b := make([]byte, 4)
		binary.NativeEndian.PutUint32(b, v)
		return b
	}
	both := func(a, b []byte) []byte { return append(append([]byte(nil), a...), b...) }
	cases := []struct {
		name string
		oob  []byte
		want uint32
		ok   bool
	}{
		{"the value", controlMessage(level, typ, u32(1234)), 1234, true},
		{"the largest value", controlMessage(level, typ, u32(math.MaxUint32)), math.MaxUint32, true},
		{"zero", controlMessage(level, typ, u32(0)), 0, true},
		{"behind a message of another type", both(controlMessage(level, typ+1, u32(7)), controlMessage(level, typ, u32(99))), 99, true},
		{"behind a message of another level", both(controlMessage(level+1, typ, u32(7)), controlMessage(level, typ, u32(99))), 99, true},
		{"beside messages of other types", both(controlMessage(level, typ, u32(5)), controlMessage(level, typ+1, u32(7))), 5, true},
		{"only a message of another type", controlMessage(level, typ+1, u32(7)), 0, false},
		{"only a message of another level", controlMessage(level+1, typ, u32(7)), 0, false},
		{"no control data", nil, 0, false},
		{"empty control data", []byte{}, 0, false},
		{"data too short for a value", controlMessage(level, typ, []byte{1, 2}), 0, false},
		{"a header cut short", controlMessage(level, typ, u32(1))[:5], 0, false},
		{"a length that runs past the data", controlMessage(level, typ, u32(1))[:syscall.CmsgLen(0)], 0, false},
	}
	for _, tc := range cases {
		got, ok := controlUint32(tc.oob, level, typ)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: controlUint32 = %d, %v; want %d, %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

// receiveBufferSize says what the OS reports for a socket's receive buffer,
// which is what the socket has, whatever was asked for.
func TestReceiveBufferSizeReadsTheSocket(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	conn := pc.(*net.UDPConn)
	if err := conn.SetReadBuffer(64 << 10); err != nil {
		t.Fatal(err)
	}
	size, err := receiveBufferSize(conn)
	if err != nil || size <= 0 {
		t.Errorf("receiveBufferSize = %d, %v; want a positive size", size, err)
	}

	// A smaller ask is a smaller buffer: the number follows the socket.
	if err := conn.SetReadBuffer(8 << 10); err != nil {
		t.Fatal(err)
	}
	if smaller, err := receiveBufferSize(conn); err != nil || smaller <= 0 || smaller >= size {
		t.Errorf("after asking for less: receiveBufferSize = %d, %v; want a positive size under %d", smaller, err, size)
	}

	// A socket that is closed has no size to report.
	conn.Close()
	if size, err := receiveBufferSize(conn); err == nil {
		t.Errorf("a closed socket reported a receive buffer of %d bytes", size)
	}
}
