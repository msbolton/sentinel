package transport

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fileTree lays out an allowed folder and a file outside it.
func fileTree(t *testing.T) (allowed, outside string) {
	t.Helper()
	root := t.TempDir()
	allowed = filepath.Join(root, "allowed")
	outside = filepath.Join(root, "outside")
	for _, d := range []string{filepath.Join(allowed, "..2024_01_01", "x"), filepath.Join(outside)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{
		filepath.Join(allowed, "plain"):               "plain",
		filepath.Join(allowed, "..2024_01_01", "key"): "kube-key",
		filepath.Join(outside, "shadow"):              "outside",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, name string) {
		if err := os.Symlink(target, filepath.Join(allowed, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	link("..2024_01_01", "..data")                            // how a mounted Kubernetes secret is laid out
	link(filepath.Join("..data", "key"), "key")               // key -> ..data/key
	link(filepath.Join(outside, "shadow"), "abs-out")         // absolute, outside
	link(filepath.Join("..", "outside", "shadow"), "rel-out") // relative, outside
	link(filepath.Join(allowed, "plain"), "abs-in")           // absolute, back inside
	return allowed, outside
}

func TestReadUnder(t *testing.T) {
	allowed, _ := fileTree(t)
	dirs := []string{allowed}
	for name, want := range map[string]string{"plain": "plain", "key": "kube-key", "..data/key": "kube-key", "../allowed/plain": "plain"} {
		got, err := ReadUnder(filepath.Join(allowed, name), dirs)
		if err != nil || string(got) != want {
			t.Errorf("%s: %q %v, want %q", name, got, err, want)
		}
	}
	for _, name := range []string{"abs-out", "rel-out", "../outside/shadow"} {
		got, err := ReadUnder(filepath.Join(allowed, name), dirs)
		if !errors.Is(err, ErrOutsideDirs) || got != nil {
			t.Errorf("%s: %q %v, want ErrOutsideDirs", name, got, err)
		}
	}
	// An absolute link is refused by the confined read even when it points
	// back inside, and the error says what to do about it.
	if _, err := ReadUnder(filepath.Join(allowed, "abs-in"), dirs); err == nil || !strings.Contains(err.Error(), "must be relative") {
		t.Errorf("an absolute link: %v", err)
	}
	// A file that is not there is reported as missing.
	if _, err := ReadUnder(filepath.Join(allowed, "nope"), dirs); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
	// A folder that is itself a link.
	mount := filepath.Join(filepath.Dir(allowed), "mount")
	if err := os.Symlink(allowed, mount); err != nil {
		t.Skip(err)
	}
	if got, err := ReadUnder(filepath.Join(mount, "key"), []string{mount}); err != nil || string(got) != "kube-key" {
		t.Errorf("through a linked folder: %q %v", got, err)
	}
}

// The read itself is confined to the folder: a path that passed the check and
// was then replaced by a link out of the folder is not followed. The check is
// bypassed here to stand for that replacement.
func TestReadInConfinesTheRead(t *testing.T) {
	allowed, _ := fileTree(t)
	if got, err := readIn(allowed, "key"); err != nil || string(got) != "kube-key" {
		t.Errorf("a link that stays inside: %q %v", got, err)
	}
	for _, rel := range []string{"abs-out", "rel-out", "../outside/shadow"} {
		if got, err := readIn(allowed, rel); err == nil {
			t.Errorf("%s: read %q through the root", rel, got)
		}
	}
}
