//go:build unix

package udp

import (
	"encoding/binary"
	"math"
	"math/rand/v2"
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

// u32 is a 32-bit value as the kernel writes it into control data.
func u32(v uint32) []byte {
	b := make([]byte, 4)
	binary.NativeEndian.PutUint32(b, v)
	return b
}

// both is the control data of a then b.
func both(a, b []byte) []byte { return append(append([]byte(nil), a...), b...) }

// controlUint32 finds the 32-bit value a datagram's control data carries at a
// level and type, which is how the kernel hands over a socket's drop count.
func TestControlUint32(t *testing.T) {
	const level, typ = syscall.SOL_SOCKET, 42
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

// controlUint32 runs for every datagram that carries a drop count, which is
// every datagram after the socket's first drop, so it must allocate nothing:
// not for a message it finds, nor for one behind another, nor for one that is
// not there.
func TestControlUint32DoesNotAllocate(t *testing.T) {
	const level, typ = syscall.SOL_SOCKET, 42
	for name, oob := range map[string][]byte{
		"the message":               controlMessage(level, typ, u32(1234)),
		"behind a message":          both(controlMessage(level, typ+1, u32(7)), controlMessage(level, typ, u32(99))),
		"no message of its type":    controlMessage(level, typ+1, u32(7)),
		"a header cut short":        controlMessage(level, typ, u32(1))[:5],
		"a length past the data":    controlMessage(level, typ, u32(1))[:syscall.CmsgLen(0)],
		"no control data":           nil,
		"a message with no payload": controlMessage(level, typ, nil),
	} {
		if n := testing.AllocsPerRun(1000, func() { controlUint32(oob, level, typ) }); n != 0 {
			t.Errorf("%s: controlUint32 allocated %v times per call, want none", name, n)
		}
	}
}

// A read whose control data the OS cut short (MSG_CTRUNC: it had more to say
// than the buffer had room for) may end in the middle of a message, so none of
// it is read, even a message that looks whole.
func TestControlUint32IntactIgnoresTruncatedControlData(t *testing.T) {
	const level, typ = syscall.SOL_SOCKET, 42
	oob := controlMessage(level, typ, u32(1234))
	if got, ok := controlUint32Intact(oob, 0, level, typ); got != 1234 || !ok {
		t.Errorf("complete control data: controlUint32Intact = %d, %v; want 1234, true", got, ok)
	}
	if got, ok := controlUint32Intact(oob, syscall.MSG_CTRUNC, level, typ); ok {
		t.Errorf("truncated control data: controlUint32Intact = %d, %v; want nothing read", got, ok)
	}
	// Other flags a read returns are no reason to ignore it.
	if got, ok := controlUint32Intact(oob, syscall.MSG_TRUNC, level, typ); got != 1234 || !ok {
		t.Errorf("a truncated datagram, whole control data: controlUint32Intact = %d, %v; want 1234, true", got, ok)
	}
}

// standardControlUint32 is what controlUint32 did before it read the header by
// hand: the standard library's parser, which makes a slice of the messages. parsed
// is false when the parser refused the data.
func standardControlUint32(oob []byte, level, typ int) (value uint32, found, parsed bool) {
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return 0, false, false
	}
	for _, m := range msgs {
		if int(m.Header.Level) == level && int(m.Header.Type) == typ && len(m.Data) >= 4 {
			return binary.NativeEndian.Uint32(m.Data), true, true
		}
	}
	return 0, false, true
}

// controlUint32 reads the header by hand, so it is held to what the standard
// library's parser, which it replaces, makes of the same bytes. For any run of
// whole messages (levels and types of either sign, data of any length) the two
// find the same value; for bytes that are not messages, cut anywhere or plain
// noise, it finds what it finds and never reads outside them.
func TestControlUint32ReadsWhatTheStandardParserReads(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	levels, types := []int{syscall.SOL_SOCKET, 0, 6, -1}, []int{1, 42, 43, -7}
	for range 20000 {
		var oob []byte
		for range rng.IntN(5) {
			data := make([]byte, rng.IntN(13))
			for i := range data {
				data[i] = byte(rng.Uint32())
			}
			oob = append(oob, controlMessage(levels[rng.IntN(len(levels))], types[rng.IntN(len(types))], data)...)
		}
		if rng.IntN(3) == 0 {
			oob = oob[:rng.IntN(len(oob)+1)]
		}
		level, typ := levels[rng.IntN(len(levels))], types[rng.IntN(len(types))]
		got, ok := controlUint32(oob, level, typ)
		if want, found, parsed := standardControlUint32(oob, level, typ); parsed && (got != want || ok != found) {
			t.Fatalf("controlUint32(%x, %d, %d) = %d, %v; the standard parser finds %d, %v", oob, level, typ, got, ok, want, found)
		}
	}
	for range 20000 {
		noise := make([]byte, rng.IntN(80))
		for i := range noise {
			noise[i] = byte(rng.Uint32())
		}
		controlUint32(noise, levels[rng.IntN(len(levels))], types[rng.IntN(len(types))])
	}
}

// BenchmarkControlUint32 is the cost per datagram of reading the drop count out
// of its control data, by hand and by the standard parser it replaced.
func BenchmarkControlUint32(b *testing.B) {
	const level, typ = syscall.SOL_SOCKET, 42
	oob := controlMessage(level, typ, u32(1234))
	b.Run("by hand", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			controlUint32(oob, level, typ)
		}
	})
	b.Run("standard parser", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			standardControlUint32(oob, level, typ)
		}
	})
}
