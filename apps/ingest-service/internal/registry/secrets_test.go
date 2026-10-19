package registry

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// defaultPolicy is the policy a deployment gets when it sets nothing.
func defaultPolicy() SecretPolicy {
	return SecretPolicy{
		EnvAllow: []string{"OPENSKY_*", "AISSTREAM_*", "SENTINEL_SECRET_*"},
		FileDirs: []string{"/etc/sentinel"},
	}
}

func TestSecretPolicyEnv(t *testing.T) {
	p := defaultPolicy()
	for ref, ok := range map[string]bool{
		"env:OPENSKY_CLIENT_ID":      true,
		"env:AISSTREAM_API_KEY":      true,
		"env:SENTINEL_SECRET_MINE":   true,
		"env:DATABASE_URL":           false,
		"env:INGEST_SERVICE_TOKEN":   false,
		"env:OPENSKY":                false, // the glob is OPENSKY_*, so the bare name is not covered
		"env:XOPENSKY_CLIENT_ID":     false,
		"env:SENTINEL_SECRETS_OTHER": false,
	} {
		err := p.Check(ref)
		if (err == nil) != ok {
			t.Errorf("Check(%q) = %v, want allowed=%v", ref, err, ok)
		}
	}
	// The refusal names the variable and says what decides.
	err := p.Check("env:DATABASE_URL")
	for _, want := range []string{"DATABASE_URL", "SENTINEL_SECRET_…", "OPENSKY_…", "AISSTREAM_…", "/etc/sentinel", "INGEST_SECRET_ENV_ALLOW", "INGEST_SECRET_FILE_DIRS"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("message %v lacks %q", err, want)
		}
	}

	// Exact names and a lone "*".
	exact := SecretPolicy{EnvAllow: []string{"MY_TOKEN"}, FileDirs: []string{"/etc/sentinel"}}
	if exact.Check("env:MY_TOKEN") != nil || exact.Check("env:MY_TOKEN_2") == nil {
		t.Error("an entry without * names exactly one variable")
	}
	any := SecretPolicy{EnvAllow: []string{"*"}, FileDirs: []string{"/etc/sentinel"}}
	if any.Check("env:DATABASE_URL") != nil {
		t.Error("a lone * allows any name")
	}
	if err := any.Check("file:/etc/shadow"); err == nil {
		t.Error("a lone * in EnvAllow must not open the file folders")
	}
}

func TestSecretPolicyFiles(t *testing.T) {
	p := defaultPolicy()
	for ref, ok := range map[string]bool{
		"file:/etc/sentinel/tak/client.pem":       true,
		"file:/etc/sentinel/secrets/listener.key": true,
		"file:/etc/shadow":                        false,
		"file:/etc/sentinel/../shadow":            false,
		"file:/etc/sentinel/tak/../../shadow":     false,
		"file:/etc/sentinelx/key":                 false,
		"file:/etc/sentinel":                      false, // the folder itself is not a file in it
		"file:/etc/sentinel/":                     false,
		"file:/proc/self/environ":                 false,
		"file:relative/path":                      false,
	} {
		err := p.Check(ref)
		if (err == nil) != ok {
			t.Errorf("Check(%q) = %v, want allowed=%v", ref, err, ok)
		}
	}
	err := p.Check("file:/etc/shadow")
	for _, want := range []string{"/etc/shadow", "INGEST_SECRET_FILE_DIRS", "INGEST_SECRET_ENV_ALLOW"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("message %v lacks %q", err, want)
		}
	}
	if err := p.Check("vault:x"); err == nil {
		t.Error("an unsupported scheme is refused")
	}
	// Several folders.
	two := SecretPolicy{EnvAllow: []string{"*"}, FileDirs: []string{"/etc/sentinel", "/run/secrets"}}
	if two.Check("file:/run/secrets/key") != nil || two.Check("file:/run/other/key") == nil {
		t.Error("every listed folder counts, and only those")
	}
}

func TestZeroSecretPolicyIsUnrestricted(t *testing.T) {
	var p SecretPolicy
	for _, ref := range []string{"env:DATABASE_URL", "file:/etc/shadow"} {
		if err := p.Check(ref); err != nil {
			t.Errorf("Check(%q) = %v", ref, err)
		}
	}
}

func TestSecretPolicyValidate(t *testing.T) {
	if err := defaultPolicy().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]SecretPolicy{
		"relative folder":    {EnvAllow: []string{"*"}, FileDirs: []string{"etc/sentinel"}},
		"lower-case name":    {EnvAllow: []string{"my_*"}, FileDirs: []string{"/etc/sentinel"}},
		"star in the middle": {EnvAllow: []string{"MY_*_X"}, FileDirs: []string{"/etc/sentinel"}},
		"empty entry":        {EnvAllow: []string{""}, FileDirs: []string{"/etc/sentinel"}},
	} {
		if err := p.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// The built-in feeds and the seed's rows name only OPENSKY_ and AISSTREAM_
// variables, which the default policy covers; so does what the TAK driver
// and the listeners mount.
func TestBuiltinRefsPassTheDefaultPolicy(t *testing.T) {
	p := defaultPolicy()
	for _, b := range Builtins {
		for key, ref := range b.SecretRefs {
			if err := p.Check(ref); err != nil {
				t.Errorf("built-in %s secret %s: %v", b.ID, key, err)
			}
		}
	}
	for _, ref := range []string{"file:/etc/sentinel/tak/client.pem", "file:/etc/sentinel/secrets/listener.key"} {
		if err := p.Check(ref); err != nil {
			t.Errorf("%s: %v", ref, err)
		}
	}
}

func TestResolveChecksThePolicyFirst(t *testing.T) {
	lookup := func(name string) (string, bool) { return "value-of-" + name, true }
	p := defaultPolicy()
	got, err := p.Resolve(map[string]string{"clientId": "env:OPENSKY_CLIENT_ID"}, lookup, nil)
	if err != nil || got["clientId"] != "value-of-OPENSKY_CLIENT_ID" {
		t.Fatalf("got %v err %v", got, err)
	}
	called := false
	spy := func(name string) (string, bool) { called = true; return "x", true }
	_, err = p.Resolve(map[string]string{"apiKey": "env:DATABASE_URL"}, spy, nil)
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") || !strings.Contains(err.Error(), `"apiKey"`) {
		t.Errorf("err = %v", err)
	}
	if called {
		t.Error("a refused reference must not even be looked up")
	}
	if _, err := p.Resolve(map[string]string{"k": "env:OPENSKY_MISSING"}, func(string) (string, bool) { return "", false }, nil); err == nil {
		t.Error("a missing variable must fail")
	}
	if _, err := p.Resolve(map[string]string{"k": "plain"}, lookup, nil); err == nil {
		t.Error("a plain value must fail")
	}
}

func TestResolveFileSecrets(t *testing.T) {
	files := map[string]string{"/etc/sentinel/tak/client.pem": "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----\n", "/etc/sentinel/empty": "  \n"}
	read := func(path string) ([]byte, error) {
		if v, ok := files[path]; ok {
			return []byte(v), nil
		}
		return nil, errors.New("no such file")
	}
	for name, p := range map[string]SecretPolicy{"zero policy": {}, "default policy": defaultPolicy()} {
		got, err := p.Resolve(map[string]string{"clientCert": "file:/etc/sentinel/tak/client.pem"}, nil, read)
		if err != nil || !strings.HasPrefix(got["clientCert"], "-----BEGIN CERTIFICATE-----") || strings.HasSuffix(got["clientCert"], "\n") {
			t.Fatalf("%s: got %q err %v", name, got["clientCert"], err)
		}
		if _, err := p.Resolve(map[string]string{"k": "file:/etc/sentinel/empty"}, nil, read); err == nil {
			t.Errorf("%s: empty file must fail", name)
		}
		if _, err := p.Resolve(map[string]string{"k": "file:/etc/sentinel/missing"}, nil, read); err == nil {
			t.Errorf("%s: missing file must fail", name)
		}
	}
	if _, err := defaultPolicy().Resolve(map[string]string{"k": "file:/etc/sentinel/../shadow"}, nil, read); err == nil || !strings.Contains(err.Error(), "INGEST_SECRET_FILE_DIRS") {
		t.Errorf("a path out of the folder: %v", err)
	}
}

// A link inside the folder that points outside it passes the lexical check, so
// the resolver follows links and checks again. Links that stay inside, which
// is how mounted Kubernetes secrets are laid out, still work.
func TestResolveRefusesASymlinkOutOfTheFolder(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	for _, d := range []string{filepath.Join(allowed, "data"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(allowed, "plain.txt"), "plain-secret\n")
	write(filepath.Join(allowed, "data", "real.txt"), "real-secret\n")
	write(filepath.Join(outside, "shadow"), "outside-secret\n")
	link := func(target, name string) {
		if err := os.Symlink(target, filepath.Join(allowed, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	link(filepath.Join(outside, "shadow"), "escape")      // absolute target outside
	link(filepath.Join("..", "outside", "shadow"), "rel") // relative target outside
	link(filepath.Join("data", "real.txt"), "inside")     // stays inside
	link(outside, "dirlink")                              // a folder link, then a file in it

	p := SecretPolicy{EnvAllow: []string{"*"}, FileDirs: []string{allowed}}
	resolve := func(name string) (string, error) {
		got, err := p.Resolve(map[string]string{"k": "file:" + filepath.Join(allowed, name)}, nil, nil)
		return got["k"], err
	}
	if v, err := resolve("plain.txt"); err != nil || v != "plain-secret" {
		t.Errorf("plain: %q %v", v, err)
	}
	if v, err := resolve("inside"); err != nil || v != "real-secret" {
		t.Errorf("a link that stays inside: %q %v", v, err)
	}
	for _, name := range []string{"escape", "rel", "dirlink/shadow"} {
		v, err := resolve(name)
		if err == nil || v != "" || !strings.Contains(err.Error(), "INGEST_SECRET_FILE_DIRS") {
			t.Errorf("%s: value %q err %v, want a refusal", name, v, err)
		}
	}
	// The folder may itself be a link (a mount point): what is inside it is still inside.
	viaLink := filepath.Join(root, "mount")
	if err := os.Symlink(allowed, viaLink); err != nil {
		t.Fatal(err)
	}
	pl := SecretPolicy{EnvAllow: []string{"*"}, FileDirs: []string{viaLink}}
	got, err := pl.Resolve(map[string]string{"k": "file:" + filepath.Join(viaLink, "plain.txt")}, nil, nil)
	if err != nil || got["k"] != "plain-secret" {
		t.Errorf("through a linked folder: %v %v", got, err)
	}
}

func TestValidatorAppliesTheSecretPolicy(t *testing.T) {
	feed := func(refs map[string]string) *Feed {
		return &Feed{ID: "opensky", Name: "OpenSky", Driver: "opensky", Config: json.RawMessage(`{"intervalSec": 10}`), SecretRefs: refs}
	}
	v := newValidator()
	v.Secrets = defaultPolicy()
	if err := v.Validate(feed(map[string]string{"clientId": "env:OPENSKY_CLIENT_ID", "clientSecret": "env:SENTINEL_SECRET_X"})); err != nil {
		t.Fatal(err)
	}
	err := v.Validate(feed(map[string]string{"clientId": "env:DATABASE_URL", "clientSecret": "file:/etc/shadow"}))
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v", err)
	}
	byPath := map[string]string{}
	for _, p := range ve.Problems {
		byPath[p.Path] = p.Message
	}
	if m := byPath["/secretRefs/clientId"]; !strings.Contains(m, "DATABASE_URL") || !strings.Contains(m, "INGEST_SECRET_ENV_ALLOW") {
		t.Errorf("clientId: %q", m)
	}
	if m := byPath["/secretRefs/clientSecret"]; !strings.Contains(m, "/etc/shadow") || !strings.Contains(m, "INGEST_SECRET_FILE_DIRS") {
		t.Errorf("clientSecret: %q", m)
	}
	// A value that is not a reference gets the pattern message, once, and it
	// states the policy the same way.
	err = v.Validate(feed(map[string]string{"clientId": "s3cret"}))
	if !errors.As(err, &ve) || len(ve.Problems) != 1 {
		t.Fatalf("err = %v", err)
	}
	if m := ve.Problems[0].Message; strings.Contains(m, "VARIABLE_NAME") || !strings.Contains(m, "SENTINEL_SECRET_…") || !strings.Contains(m, "/etc/sentinel") {
		t.Errorf("pattern message %q", m)
	}
	// The zero policy leaves the validator as it was.
	if err := newValidator().Validate(feed(map[string]string{"clientId": "env:DATABASE_URL"})); err != nil {
		t.Errorf("zero policy: %v", err)
	}
}

// The TLS file fields of a stream transport are read from disk at start, so
// they are held to the same folders, wherever and however the keys are spelled.
func TestValidatorAppliesTheFolderPolicyToStreamTLSFiles(t *testing.T) {
	v := newValidator()
	v.Secrets = defaultPolicy()
	feed := func(tls string) *Feed {
		return &Feed{ID: "s", Name: "s", Driver: "stream", Config: json.RawMessage(
			`{"transport":{"kind":"tcpserver","address":":47100","tls":` + tls + `},"decoder":{"name":"cot"}}`)}
	}
	if err := v.Validate(feed(`{"enabled":true,"certFile":"/etc/sentinel/tak/c.pem","keyFile":"/etc/sentinel/tak/k.pem","caFile":"/etc/sentinel/tak/ca.pem","clientCaFile":"/etc/sentinel/tak/cc.pem"}`)); err != nil {
		t.Fatal(err)
	}
	err := v.Validate(feed(`{"enabled":true,"certFile":"/etc/passwd","keyFile":"/etc/sentinel/../shadow","CAFILE":"/root/ca.pem","clientCaFile":"/tmp/x"}`))
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v", err)
	}
	got := map[string]string{}
	for _, p := range ve.Problems {
		got[p.Path] = p.Message
	}
	for _, field := range []string{"certFile", "keyFile", "caFile", "clientCaFile"} {
		m := got["/config/transport/tls/"+field]
		if !strings.Contains(m, "INGEST_SECRET_FILE_DIRS") {
			t.Errorf("%s: %q (problems %v)", field, m, got)
		}
	}
	// With the zero policy nothing is held back.
	nv := newValidator()
	if err := nv.Validate(feed(`{"enabled":true,"certFile":"/etc/passwd"}`)); err != nil {
		t.Errorf("zero policy: %v", err)
	}
}

// Only the zero policy restricts nothing: one with a list set and the other
// empty allows nothing of the empty kind.
func TestPartialSecretPolicyDeniesTheEmptyKind(t *testing.T) {
	envOnly := SecretPolicy{EnvAllow: []string{"OPENSKY_*"}}
	if envOnly.Unrestricted() || envOnly.Check("env:OPENSKY_X") != nil || envOnly.Check("file:/etc/sentinel/k") == nil {
		t.Error("EnvAllow alone: files must be denied")
	}
	filesOnly := SecretPolicy{FileDirs: []string{"/etc/sentinel"}}
	if filesOnly.Unrestricted() || filesOnly.Check("file:/etc/sentinel/k") != nil || filesOnly.Check("env:OPENSKY_X") == nil {
		t.Error("FileDirs alone: variables must be denied")
	}
	if !(SecretPolicy{}).Unrestricted() {
		t.Error("the zero policy is unrestricted")
	}
	// The validator agrees about TLS files.
	v := newValidator()
	v.Secrets = envOnly
	f := &Feed{ID: "s", Name: "s", Driver: "stream", Config: json.RawMessage(
		`{"transport":{"kind":"tcpserver","address":":47100","tls":{"enabled":true,"caFile":"/etc/sentinel/ca.pem"}},"decoder":{"name":"cot"}}`)}
	var ve *ValidationError
	if err := v.Validate(f); !errors.As(err, &ve) || len(ve.Problems) != 1 || ve.Problems[0].Path != "/config/transport/tls/caFile" {
		t.Errorf("err = %v", err)
	}
}

// A TLS file field is not a secret reference, and its refusal does not say so.
func TestTLSFileRefusalDoesNotCallItASecretReference(t *testing.T) {
	v := newValidator()
	v.Secrets = defaultPolicy()
	f := &Feed{ID: "s", Name: "s", Driver: "stream", Config: json.RawMessage(
		`{"transport":{"kind":"tcpserver","address":":47100","tls":{"enabled":true,"certFile":"/etc/passwd"}},"decoder":{"name":"cot"}}`)}
	var ve *ValidationError
	if err := v.Validate(f); !errors.As(err, &ve) || len(ve.Problems) != 1 {
		t.Fatalf("err = %v", err)
	}
	const want = "file /etc/passwd is outside the folders INGEST_SECRET_FILE_DIRS allows (default /etc/sentinel)"
	if m := ve.Problems[0].Message; m != want {
		t.Errorf("message %q, want %q", m, want)
	}
}

// With no injected reader the file is read confined to its folder: a link that
// stays inside works as before, an absolute one is refused with the reason.
func TestResolveReadsConfinedToTheFolder(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "real"), []byte("real\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(root, "rel")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "abs")); err != nil {
		t.Fatal(err)
	}
	p := SecretPolicy{EnvAllow: []string{"*"}, FileDirs: []string{root}}
	if got, err := p.Resolve(map[string]string{"k": "file:" + filepath.Join(root, "rel")}, nil, nil); err != nil || got["k"] != "real" {
		t.Errorf("relative link: %v %v", got, err)
	}
	if _, err := p.Resolve(map[string]string{"k": "file:" + filepath.Join(root, "abs")}, nil, nil); err == nil || !strings.Contains(err.Error(), "must be relative") {
		t.Errorf("absolute link: %v", err)
	}
}
