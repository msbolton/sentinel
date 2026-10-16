// Package framing splits a byte stream into frames. Each framer is a
// bufio.SplitFunc so stream transports can scan a connection with it.
package framing

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// Names of the built-in framers.
const (
	Line         = "line"         // newline-delimited text; CR and LF stripped, blank lines dropped by the scanner
	LengthPrefix = "lengthprefix" // 2-byte big-endian length then payload
	Beast        = "beast"        // Mode-S Beast binary: 0x1a-escaped frames
	XMLEvent     = "xmlevent"     // Cursor-on-Target: one <event>...</event> per frame
	Message      = "message"      // message transports: no framing
)

// ErrFrameTooLong is returned when a frame exceeds the configured bound.
var ErrFrameTooLong = errors.New("frame exceeds the maximum size")

// New returns the split function for name. maxFrame bounds a single frame.
func New(name string, maxFrame int) (bufio.SplitFunc, error) {
	switch name {
	case "", Line:
		return lineSplit, nil
	case LengthPrefix:
		return lengthPrefixSplit(maxFrame), nil
	case Beast:
		return beastSplit, nil
	case XMLEvent:
		return xmlEventSplit, nil
	case Message:
		return nil, errors.New(`framing "message" applies to message transports only`)
	}
	return nil, fmt.Errorf("unknown framing %q", name)
}

// Known reports whether name is a framer name (including "message").
func Known(name string) bool {
	switch name {
	case "", Line, LengthPrefix, Beast, XMLEvent, Message:
		return true
	}
	return false
}

// lineSplit is bufio.ScanLines plus a trailing CR strip.
func lineSplit(data []byte, atEOF bool) (int, []byte, error) {
	advance, token, err := bufio.ScanLines(data, atEOF)
	if token != nil {
		token = bytes.TrimRight(token, "\r")
	}
	return advance, token, err
}

func lengthPrefixSplit(maxFrame int) bufio.SplitFunc {
	return func(data []byte, atEOF bool) (int, []byte, error) {
		if len(data) < 2 {
			if atEOF && len(data) > 0 {
				return 0, nil, errors.New("truncated length prefix")
			}
			return 0, nil, nil
		}
		n := int(binary.BigEndian.Uint16(data[:2]))
		if maxFrame > 0 && n > maxFrame {
			return 0, nil, ErrFrameTooLong
		}
		if len(data) < 2+n {
			if atEOF {
				return 0, nil, errors.New("truncated frame")
			}
			return 0, nil, nil
		}
		return 2 + n, data[2 : 2+n], nil
	}
}

// beastSplit frames Mode-S Beast output. A frame starts with 0x1a followed
// by a type byte (0x31 Mode-AC, 0x32 Mode-S short, 0x33 Mode-S long, 0x34
// status); a literal 0x1a inside the frame is escaped as 0x1a 0x1a. The
// token returned is the unescaped frame including the type byte but not
// the leading 0x1a.
func beastSplit(data []byte, atEOF bool) (int, []byte, error) {
	start := bytes.IndexByte(data, 0x1a)
	if start < 0 {
		return len(data), nil, nil // discard garbage before the first marker
	}
	for {
		if start+1 >= len(data) {
			return start, nil, nil
		}
		typ := data[start+1]
		want := 0
		switch typ {
		case 0x31:
			want = 1 + 6 + 1 + 2 // type, timestamp, signal, 2-byte Mode-AC
		case 0x32:
			want = 1 + 6 + 1 + 7
		case 0x33:
			want = 1 + 6 + 1 + 14
		case 0x34:
			want = 1 + 6 + 1 + 14
		default:
			// Not a frame start; look for the next marker.
			next := bytes.IndexByte(data[start+1:], 0x1a)
			if next < 0 {
				return len(data), nil, nil
			}
			start += 1 + next
			continue
		}
		out := make([]byte, 0, want)
		i := start + 1
		for len(out) < want {
			if i >= len(data) {
				if atEOF {
					return len(data), nil, nil
				}
				return start, nil, nil
			}
			b := data[i]
			if b == 0x1a {
				if i+1 >= len(data) {
					if atEOF {
						return len(data), nil, nil
					}
					return start, nil, nil
				}
				if data[i+1] != 0x1a {
					// A new frame began inside this one: drop the partial frame.
					start = i
					out = out[:0]
					if start+1 >= len(data) {
						return start, nil, nil
					}
					typ = data[start+1]
					switch typ {
					case 0x31:
						want = 10
					case 0x32:
						want = 15
					case 0x33, 0x34:
						want = 22
					default:
						return start + 1, nil, nil
					}
					i = start + 1
					continue
				}
				i++
			}
			out = append(out, b)
			i++
		}
		return i, out, nil
	}
}

var (
	eventOpen  = []byte("<event")
	eventClose = []byte("</event>")
)

// xmlEventSplit returns one <event ...>...</event> element per frame and
// also handles the self-closing form <event .../>. Bytes before the opening
// tag (an XML declaration, whitespace, a TAK protocol header) are skipped.
func xmlEventSplit(data []byte, atEOF bool) (int, []byte, error) {
	start := bytes.Index(data, eventOpen)
	if start < 0 {
		if atEOF {
			return len(data), nil, nil
		}
		// Keep a tail that might be a split "<event" prefix.
		keep := len(eventOpen) - 1
		if len(data) > keep {
			return len(data) - keep, nil, nil
		}
		return 0, nil, nil
	}
	rest := data[start:]
	// Find the end of the opening tag to check for self-closing.
	gt := bytes.IndexByte(rest, '>')
	if gt < 0 {
		return start, nil, nil
	}
	if gt > 0 && rest[gt-1] == '/' {
		return start + gt + 1, rest[:gt+1], nil
	}
	end := bytes.Index(rest, eventClose)
	if end < 0 {
		if atEOF {
			return len(data), nil, nil
		}
		return start, nil, nil
	}
	n := end + len(eventClose)
	return start + n, rest[:n], nil
}
