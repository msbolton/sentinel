package framing

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func scanAll(t *testing.T, name string, input []byte) [][]byte {
	t.Helper()
	split, err := New(name, 1<<16)
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(bytes.NewReader(input))
	sc.Split(split)
	var out [][]byte
	for sc.Scan() {
		out = append(out, append([]byte(nil), sc.Bytes()...))
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return out
}

func TestLineStripsCRAndDropsEmpty(t *testing.T) {
	frames := scanAll(t, Line, []byte("a\r\nb\n\nc"))
	want := []string{"a", "b", "", "c"}
	if len(frames) != len(want) {
		t.Fatalf("frames = %q", frames)
	}
	for i, w := range want {
		if string(frames[i]) != w {
			t.Errorf("frame %d = %q, want %q", i, frames[i], w)
		}
	}
}

func TestLengthPrefix(t *testing.T) {
	input := []byte{0, 3, 'a', 'b', 'c', 0, 1, 'z'}
	frames := scanAll(t, LengthPrefix, input)
	if len(frames) != 2 || string(frames[0]) != "abc" || string(frames[1]) != "z" {
		t.Errorf("frames = %q", frames)
	}
	split, _ := New(LengthPrefix, 2)
	if _, _, err := split([]byte{0, 3, 'a', 'b', 'c'}, false); err != ErrFrameTooLong {
		t.Errorf("expected ErrFrameTooLong, got %v", err)
	}
}

func TestBeastUnescapesAndSkipsGarbage(t *testing.T) {
	short := []byte{0x32, 1, 2, 3, 4, 5, 6, 0x7f, 0x5d, 0x1a, 0x1a, 0x01, 0x02, 0x03, 0x04} // 15 bytes with an escaped 0x1a
	escaped := bytes.ReplaceAll(short[1:], []byte{0x1a}, []byte{0x1a, 0x1a})
	input := append([]byte("junk"), 0x1a, 0x32)
	input = append(input, escaped...)
	input = append(input, 0x1a, 0x31, 1, 2, 3, 4, 5, 6, 0x50, 0xaa, 0xbb)
	frames := scanAll(t, Beast, input)
	if len(frames) != 2 {
		t.Fatalf("frames = %x", frames)
	}
	if !bytes.Equal(frames[0], short) {
		t.Errorf("short frame = %x, want %x", frames[0], short)
	}
	if frames[1][0] != 0x31 || len(frames[1]) != 10 {
		t.Errorf("mode-ac frame = %x", frames[1])
	}
}

func TestXMLEventSplitsAndHandlesSelfClosing(t *testing.T) {
	input := []byte(`<?xml version="1.0"?><event uid="a"><point lat="1" lon="2"/></event>
<event uid="b"/><event uid="c"><detail/></event>`)
	frames := scanAll(t, XMLEvent, input)
	want := []string{`<event uid="a"><point lat="1" lon="2"/></event>`, `<event uid="b"/>`, `<event uid="c"><detail/></event>`}
	if len(frames) != len(want) {
		t.Fatalf("frames = %q", frames)
	}
	for i := range want {
		if string(frames[i]) != want[i] {
			t.Errorf("frame %d = %q, want %q", i, frames[i], want[i])
		}
	}
}

func TestXMLEventAcrossReads(t *testing.T) {
	split, _ := New(XMLEvent, 0)
	adv, tok, err := split([]byte(`<event uid="a"><poi`), false)
	if err != nil || tok != nil || adv != 0 {
		t.Fatalf("partial event must wait: adv=%d tok=%q err=%v", adv, tok, err)
	}
	adv, tok, err = split([]byte(`   <ev`), false)
	if err != nil || tok != nil || adv != 1 {
		t.Fatalf("prefix tail must be kept: adv=%d tok=%q err=%v", adv, tok, err)
	}
}

func TestUnknownAndMessageFraming(t *testing.T) {
	if _, err := New("nope", 0); err == nil {
		t.Error("unknown framing must fail")
	}
	if _, err := New(Message, 0); err == nil {
		t.Error("message framing has no split function")
	}
	if !Known(Message) || !Known("") || Known("nope") {
		t.Error("Known is wrong")
	}
}

func delimitedFrames(t *testing.T, input []byte, max int) ([][]byte, error) {
	t.Helper()
	split, err := New(Delimited, max)
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(bytes.NewReader(input))
	sc.Buffer(make([]byte, 0, 16), max+binary.MaxVarintLen64)
	sc.Split(split)
	var out [][]byte
	for sc.Scan() {
		out = append(out, append([]byte(nil), sc.Bytes()...))
	}
	return out, sc.Err()
}

func TestDelimitedSplitsVarintFrames(t *testing.T) {
	long := bytes.Repeat([]byte{'x'}, 300) // needs a two-byte varint
	var in []byte
	for _, p := range [][]byte{[]byte("ab"), long, []byte("c")} {
		in = binary.AppendUvarint(in, uint64(len(p)))
		in = append(in, p...)
	}
	frames, err := delimitedFrames(t, in, 1<<16)
	if err != nil || len(frames) != 3 || string(frames[0]) != "ab" || len(frames[1]) != 300 || string(frames[2]) != "c" {
		t.Fatalf("frames %d err %v", len(frames), err)
	}
	if !Known(Delimited) {
		t.Error("delimited must be a known framing")
	}
}

func TestDelimitedRejectsHugeAndMalformed(t *testing.T) {
	huge := binary.AppendUvarint(nil, 1<<40) // a claimed 1 TiB frame
	if _, err := delimitedFrames(t, append(huge, 'x'), 1<<16); !errors.Is(err, ErrFrameTooLong) {
		t.Errorf("huge length: err = %v, want ErrFrameTooLong", err)
	}
	overlong := bytes.Repeat([]byte{0xff}, 11) // never terminates within 10 bytes
	if _, err := delimitedFrames(t, overlong, 1<<16); err == nil || errors.Is(err, ErrFrameTooLong) {
		t.Errorf("malformed varint: err = %v", err)
	}
	truncated := append(binary.AppendUvarint(nil, 10), 'a', 'b')
	if _, err := delimitedFrames(t, truncated, 1<<16); err == nil {
		t.Error("a frame cut short at EOF must be an error")
	}
}

func TestDelimitedUnlimitedRejectsOverflowingLength(t *testing.T) {
	// With no frame bound, a length that does not fit in an int must be an
	// error rather than a slice panic.
	huge := binary.AppendUvarint(nil, 1<<63)
	if _, err := delimitedFrames(t, append(huge, 'x'), 0); err == nil {
		t.Error("a length beyond the buffered data must be an error")
	}
}
