package publish

import "errors"

// ErrClosed is returned by Emit after Close.
var ErrClosed = errors.New("publisher closed")
