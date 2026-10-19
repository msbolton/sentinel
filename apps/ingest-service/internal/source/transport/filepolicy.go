package transport

import (
	"errors"
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
