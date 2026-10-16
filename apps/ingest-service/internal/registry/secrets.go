package registry

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Secret references are indirections, never values: "env:NAME" resolves to
// the environment variable NAME on the replica running the feed.
var envRefRe = regexp.MustCompile(`^env:[A-Z][A-Z0-9_]*$`)

// ValidSecretRef reports whether ref is a supported secret reference.
func ValidSecretRef(ref string) bool { return envRefRe.MatchString(ref) }

// Lookup resolves a variable name; os.LookupEnv in production.
type Lookup func(name string) (string, bool)

// ResolveSecrets turns references into values. A reference that resolves to
// nothing is an error, so a feed never starts with a silently empty secret.
func ResolveSecrets(refs map[string]string, lookup Lookup) (map[string]string, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	out := make(map[string]string, len(refs))
	for key, ref := range refs {
		name, ok := strings.CutPrefix(ref, "env:")
		if !ok {
			return nil, fmt.Errorf("secret %q: unsupported reference %q", key, ref)
		}
		val, found := lookup(name)
		if !found || val == "" {
			return nil, fmt.Errorf("secret %q: environment variable %s is not set", key, name)
		}
		out[key] = val
	}
	return out, nil
}
