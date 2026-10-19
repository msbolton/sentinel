//go:build !unix

package udp

import (
	"errors"
	"syscall"
)

// receiveBufferSize cannot be read on this platform, so the listener reports a
// receive buffer of 0, which means unknown.
func receiveBufferSize(syscall.Conn) (int, error) { return 0, errors.ErrUnsupported }
