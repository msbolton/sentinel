package transport

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrOutsideDirs reports a file that is not inside any of the allowed
// folders.
var ErrOutsideDirs = errors.New("file is outside the allowed folders")

// PathUnder reports whether path, cleaned (so that ".." cannot climb out), is
// a file inside one of dirs. A folder is not a file in itself. path must be
// absolute.
func PathUnder(path string, dirs []string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	path = filepath.Clean(path)
	for _, dir := range dirs {
		dir = filepath.Clean(dir)
		if dir == string(filepath.Separator) {
			if path != dir {
				return true
			}
			continue
		}
		if strings.HasPrefix(path, dir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ResolveUnder checks path against dirs lexically, then follows its symbolic
// links and checks where it really lands, so that a link inside a folder that
// leads out of it is refused. Links that stay inside, which is how mounted
// secrets are laid out, are fine, and so is a folder that is itself a link.
// It returns the path to read: the resolved one, so that the file checked is
// the file read. A path that does not exist yet comes back unchanged, for the
// read to report.
func ResolveUnder(path string, dirs []string) (string, error) {
	if !PathUnder(path, dirs) {
		return "", ErrOutsideDirs
	}
	path = filepath.Clean(path) // links are followed from the path the check saw
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
		return "", err
	}
	if PathUnder(resolved, dirs) {
		return resolved, nil
	}
	for _, dir := range dirs {
		if real, err := filepath.EvalSymlinks(dir); err == nil && PathUnder(resolved, []string{real}) {
			return resolved, nil
		}
	}
	return "", ErrOutsideDirs
}

// OutsideMessage is the refusal of a file outside the allowed folders, for
// anything that names a file: a TLS file of a transport, or a file reference.
func OutsideMessage(path string) string {
	return fmt.Sprintf("file %s is outside the folders INGEST_SECRET_FILE_DIRS allows (default /etc/sentinel)", path)
}

// ReadUnder reads path, which must be inside one of dirs. It checks as
// ResolveUnder does, for a clear refusal, and then reads through an os.Root
// opened on the folder, so that a link swapped in between the check and the
// read still cannot lead out of the folder: the read itself is confined. The
// price is that a link inside the folder must be relative and stay inside it
// (Kubernetes' ..data links do); an absolute link is refused even when it
// points back inside.
func ReadUnder(path string, dirs []string) ([]byte, error) {
	if _, err := ResolveUnder(path, dirs); err != nil {
		return nil, err
	}
	path = filepath.Clean(path)
	var lastErr error
	for _, dir := range dirs {
		dir = filepath.Clean(dir)
		rel, err := filepath.Rel(dir, path)
		if err != nil || !PathUnder(path, []string{dir}) {
			continue
		}
		data, err := readIn(dir, rel)
		if err == nil {
			return data, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		return nil, ErrOutsideDirs
	}
	if errors.Is(lastErr, os.ErrNotExist) || errors.Is(lastErr, errNotRegular) || errors.Is(lastErr, errTooLarge) {
		return nil, lastErr
	}
	return nil, fmt.Errorf("%w (a link inside an allowed folder must be relative and stay inside it; an absolute link is refused even when it points back inside)", lastErr)
}

// MaxFileBytes is the most a secret or TLS file read through the folder policy
// may hold. A key, a certificate or a CA bundle is a few kilobytes, so a file
// past this is not one, and is refused instead of being read whole into memory.
const MaxFileBytes = 1 << 20

var (
	errNotRegular = errors.New("not a regular file")
	errTooLarge   = errors.New("file is too large")
)

// readIn reads rel inside dir without leaving it. What it reads is checked on
// the file it opened: a regular file, so that a FIFO (opened for reading it
// would wait for a writer, and the feed with it) or a device is refused; and
// read through a bound of MaxFileBytes, so that a file that grows, or was never
// small, cannot fill the memory. The open does not wait either, for the case of
// a FIFO put in place of the file after the folder was checked.
func readIn(dir, rel string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.OpenFile(rel, os.O_RDONLY|openNonblock, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readBounded(f, rel)
}

// ReadFile reads a file with no folder policy, as os.ReadFile does, but with
// the bounds of the confined read: it must be a regular file (a FIFO is refused
// without waiting for a writer) of at most MaxFileBytes.
func ReadFile(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|openNonblock, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readBounded(f, filepath.Base(path))
}

// readBounded reads an opened file that must be regular and at most
// MaxFileBytes; name is what the errors call it.
func readBounded(f *os.File, name string) ([]byte, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is %w", name, errNotRegular)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes: %w", name, MaxFileBytes, errTooLarge)
	}
	return data, nil
}
