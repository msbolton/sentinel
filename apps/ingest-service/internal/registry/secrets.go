package registry

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Secret references are indirections, never values: "env:NAME" resolves to
// the environment variable NAME on the replica running the feed, and
// "file:/path" to the contents of that file (how mounted secrets and PEM
// material arrive). Both must be present on every replica that may own the
// feed.
var secretRefRe = regexp.MustCompile(`^(env:[A-Z][A-Z0-9_]*|file:/[^\s]+)$`)

// ValidSecretRef reports whether ref is a supported secret reference.
func ValidSecretRef(ref string) bool { return secretRefRe.MatchString(ref) }

// Lookup resolves a variable name; os.LookupEnv in production.
type Lookup func(name string) (string, bool)

// ReadFile reads a file reference; os.ReadFile in production.
type ReadFile func(path string) ([]byte, error)

// ResolveSecrets turns references into values. A reference that resolves to
// nothing is an error, so a feed never starts with a silently empty secret.
func ResolveSecrets(refs map[string]string, lookup Lookup) (map[string]string, error) {
	return ResolveSecretsWith(refs, lookup, nil)
}

// ResolveSecretsWith is ResolveSecrets with an injectable file reader.
func ResolveSecretsWith(refs map[string]string, lookup Lookup, readFile ReadFile) (map[string]string, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if readFile == nil {
		readFile = os.ReadFile
	}
	out := make(map[string]string, len(refs))
	for key, ref := range refs {
		switch {
		case strings.HasPrefix(ref, "env:"):
			name := strings.TrimPrefix(ref, "env:")
			val, found := lookup(name)
			if !found || val == "" {
				return nil, fmt.Errorf("secret %q: environment variable %s is not set", key, name)
			}
			out[key] = val
		case strings.HasPrefix(ref, "file:"):
			path := strings.TrimPrefix(ref, "file:")
			data, err := readFile(path)
			if err != nil {
				return nil, fmt.Errorf("secret %q: reading %s: %w", key, path, err)
			}
			val := strings.TrimSpace(string(data))
			if val == "" {
				return nil, fmt.Errorf("secret %q: file %s is empty", key, path)
			}
			out[key] = val
		default:
			return nil, fmt.Errorf("secret %q: unsupported reference %q", key, ref)
		}
	}
	return out, nil
}
