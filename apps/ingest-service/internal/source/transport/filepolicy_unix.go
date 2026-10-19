//go:build unix

package transport

import "syscall"

// openNonblock keeps the open of a FIFO from waiting for a writer.
const openNonblock = syscall.O_NONBLOCK
