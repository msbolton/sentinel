package registry

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/sentinel/ingest-service/internal/source/transport"
)

// Secret references are indirections, never values: "env:NAME" resolves to
// the environment variable NAME on the replica running the feed, and
// "file:/path" to the contents of that file (how mounted secrets and PEM
// material arrive). Both must be present on every replica that may own the
// feed. A SecretPolicy decides which variables and folders a reference may
// name, because a driver sends its secrets to hosts the operator chooses.
var secretRefRe = regexp.MustCompile(`^(env:[A-Z][A-Z0-9_]*|file:/[^\s]+)$`)

// ValidSecretRef reports whether ref is a supported secret reference.
func ValidSecretRef(ref string) bool { return secretRefRe.MatchString(ref) }

// The secret policy a deployment gets when it sets nothing: the variables of
// the built-in feeds and the operator's own SENTINEL_SECRET_ ones, and the
// folder the TAK key material and the listener secrets are mounted under.
const (
	DefaultSecretEnvAllow = "OPENSKY_*,AISSTREAM_*,SENTINEL_SECRET_*"
	DefaultSecretFileDirs = "/etc/sentinel"
)

// SecretPolicyHint says what a secret reference may name by default and what
// changes it. It ends every refusal, and the pattern message, so that an
// operator who is refused knows where to look.
const SecretPolicyHint = "by default only SENTINEL_SECRET_…, OPENSKY_… and AISSTREAM_… names are allowed, or files under /etc/sentinel; " +
	"INGEST_SECRET_ENV_ALLOW and INGEST_SECRET_FILE_DIRS decide"

// FileOutsideMessage is the refusal of a file path outside the allowed
// folders, whether it is a secret reference or a TLS file of a stream
// transport.
func FileOutsideMessage(path string) string {
	return fmt.Sprintf("file %s is not allowed in a secret reference: %s", path, SecretPolicyHint)
}

// SecretPolicy limits what a secret reference may name. EnvAllow lists
// environment variable names, or prefixes ending in "*" ("OPENSKY_*"); a lone
// "*" allows any name. FileDirs lists the absolute folders a file reference
// must be inside. The zero value restricts nothing, so that tests stay
// simple; the service configuration always fills both lists.
type SecretPolicy struct{ EnvAllow, FileDirs []string }

func (p SecretPolicy) unrestricted() bool { return len(p.EnvAllow) == 0 && len(p.FileDirs) == 0 }

var envAllowEntryRe = regexp.MustCompile(`^([A-Z][A-Z0-9_]*\*?|\*)$`)

// Validate refuses a policy the service must not start with: an allow entry
// that is not a variable name, a prefix ending in "*", or "*", and a folder
// that is not an absolute path.
func (p SecretPolicy) Validate() error {
	for _, e := range p.EnvAllow {
		if !envAllowEntryRe.MatchString(e) {
			return fmt.Errorf("INGEST_SECRET_ENV_ALLOW: %q is not a variable name or a prefix ending in * (like OPENSKY_*)", e)
		}
	}
	for _, d := range p.FileDirs {
		if !strings.HasPrefix(d, "/") {
			return fmt.Errorf("INGEST_SECRET_FILE_DIRS: %q is not an absolute path", d)
		}
	}
	return nil
}

func (p SecretPolicy) envAllowed(name string) bool {
	for _, e := range p.EnvAllow {
		switch {
		case e == "*":
			return true
		case strings.HasSuffix(e, "*"):
			if strings.HasPrefix(name, strings.TrimSuffix(e, "*")) {
				return true
			}
		case e == name:
			return true
		}
	}
	return false
}

// Check returns an error when ref names a variable or file the policy does
// not allow. It reads the reference as written, so a file path is cleaned
// first ("/etc/sentinel/../shadow" is "/etc/shadow"); links are followed only
// when the file is read (Resolve). A reference of another form is refused.
func (p SecretPolicy) Check(ref string) error {
	if p.unrestricted() {
		return nil
	}
	switch {
	case strings.HasPrefix(ref, "env:"):
		if name := strings.TrimPrefix(ref, "env:"); !p.envAllowed(name) {
			return fmt.Errorf("environment variable %s is not allowed in a secret reference: %s", name, SecretPolicyHint)
		}
	case strings.HasPrefix(ref, "file:"):
		if path := strings.TrimPrefix(ref, "file:"); !transport.PathUnder(path, p.FileDirs) {
			return errors.New(FileOutsideMessage(path))
		}
	default:
		return errors.New("unsupported reference: use env:NAME or file:/absolute/path")
	}
	return nil
}

// Lookup resolves a variable name; os.LookupEnv in production.
type Lookup func(name string) (string, bool)

// ReadFile reads a file reference; os.ReadFile in production.
type ReadFile func(path string) ([]byte, error)

// Resolve turns references into values. Each is checked against the policy
// first, and a file is read only after its links are followed and the place
// they lead to is checked too, so a link inside an allowed folder cannot reach
// outside it. A reference that resolves to nothing is an error, so a feed
// never starts with a silently empty secret. A nil lookup or readFile means
// os.LookupEnv or os.ReadFile.
func (p SecretPolicy) Resolve(refs map[string]string, lookup Lookup, readFile ReadFile) (map[string]string, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if readFile == nil {
		readFile = os.ReadFile
	}
	out := make(map[string]string, len(refs))
	for key, ref := range refs {
		if err := p.Check(ref); err != nil {
			return nil, fmt.Errorf("secret %q: %w", key, err)
		}
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
			if !p.unrestricted() {
				resolved, err := transport.ResolveUnder(path, p.FileDirs)
				switch {
				case errors.Is(err, transport.ErrOutsideDirs):
					return nil, fmt.Errorf("secret %q: %s", key, FileOutsideMessage(path))
				case err != nil:
					return nil, fmt.Errorf("secret %q: reading %s: %w", key, path, err)
				}
				path = resolved
			}
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
			return nil, fmt.Errorf("secret %q: unsupported reference: use env:NAME or file:/absolute/path", key)
		}
	}
	return out, nil
}
