//go:build !unix

package transport

// openNonblock is nothing where the system has no such flag.
const openNonblock = 0
