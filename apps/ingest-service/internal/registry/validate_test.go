package registry

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sentinel/ingest-service/internal/drivers"
	_ "github.com/sentinel/ingest-service/internal/source/all"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

func newValidator() *Validator { return &Validator{Catalog: drivers.Default()} }

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"My MQTT Feed":       "my-mqtt-feed",
		"  readsb @ site-1 ": "readsb-site-1",
		"ALLCAPS":            "allcaps",
		"---":                "",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Slugify(strings.Repeat("a", 100)); len(got) != 63 {
		t.Errorf("long slug length = %d", len(got))
	}
}

func TestValidateAcceptsGoodFeed(t *testing.T) {
	f := &Feed{ID: "opensky", Name: "OpenSky", Driver: "opensky",
		Config:     json.RawMessage(`{"intervalSec": 10}`),
		SecretRefs: map[string]string{"clientId": "env:OPENSKY_CLIENT_ID", "clientSecret": "env:OPENSKY_CLIENT_SECRET"}}
	if err := newValidator().Validate(f); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCollectsProblems(t *testing.T) {
	f := &Feed{ID: "Bad Id", Name: " ", Driver: "opensky",
		Config:     json.RawMessage(`{"intervalSec": 0, "clientSecret": "plain"}`),
		SecretRefs: map[string]string{"clientSecret": "s3cret", "other": "env:X"}}
	err := newValidator().Validate(f)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	want := []string{"/id", "/name", "/config/intervalSec", "/config/clientSecret", "/secretRefs/clientSecret", "/secretRefs/other"}
	for _, path := range want {
		found := false
		for _, p := range ve.Problems {
			if p.Path == path {
				found = true
			}
		}
		if !found {
			t.Errorf("missing problem at %s in %+v", path, ve.Problems)
		}
	}
}

func TestValidateUnknownDriver(t *testing.T) {
	err := newValidator().Validate(&Feed{ID: "x", Name: "x", Driver: "teleport"})
	if err == nil || !strings.Contains(err.Error(), "/driver") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidSecretRef(t *testing.T) {
	for ref, ok := range map[string]bool{"env:X": true, "file:/a/b": true, "file:relative": false, "file:/with space": false, "vault:x": false, "": false} {
		if ValidSecretRef(ref) != ok {
			t.Errorf("ValidSecretRef(%q) = %v", ref, !ok)
		}
	}
}

func TestValidateDirectionAndPolicy(t *testing.T) {
	v := newValidator()
	// A source driver cannot be turned around or given a sharing policy.
	f := &Feed{ID: "x", Name: "x", Driver: "opensky", Config: json.RawMessage(`{"direction": "out", "policy": {"entityTypes": ["AIRCRAFT"]}, "outbound": {"queueMax": 500}}`)}
	err := v.Validate(f)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	for _, path := range []string{"/config/direction", "/config/policy", "/config/outbound"} {
		found := false
		for _, p := range ve.Problems {
			if p.Path == path {
				found = true
			}
		}
		if !found {
			t.Errorf("missing problem at %s in %+v", path, ve.Problems)
		}
	}
	// Narrowing to the driver's own direction is fine; a bad word is not.
	if err := v.Validate(&Feed{ID: "x", Name: "x", Driver: "opensky", Config: json.RawMessage(`{"direction": "in"}`)}); err != nil {
		t.Errorf("direction in on a source: %v", err)
	}
	if err := v.Validate(&Feed{ID: "x", Name: "x", Driver: "opensky", Config: json.RawMessage(`{"direction": "sideways"}`)}); err == nil {
		t.Error("bad direction accepted")
	}
	// Effective direction: config narrows "both", never widens "in".
	if got := (&Feed{Config: json.RawMessage(`{"direction":"out"}`)}).Direction(driver.DirectionBoth); got != driver.DirectionOut {
		t.Errorf("narrowed = %s", got)
	}
	if got := (&Feed{Config: json.RawMessage(`{"direction":"both"}`)}).Direction(driver.DirectionIn); got != driver.DirectionIn {
		t.Errorf("widened = %s", got)
	}
	if got := (&Feed{}).Direction(""); got != driver.DirectionIn {
		t.Errorf("default = %s", got)
	}
}

// GET /feeds/ports is a literal route that the router prefers to
// GET /feeds/{id}, so a feed with that id could never be read.
func TestReservedFeedID(t *testing.T) {
	v := newValidator()
	feed := func(id string) *Feed {
		return &Feed{ID: id, Name: "Ports", Driver: "opensky", Config: json.RawMessage(`{"intervalSec": 10}`)}
	}
	err := v.Validate(feed("ports"))
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Problems) != 1 || ve.Problems[0] != (Problem{Path: "/id", Message: "is reserved"}) {
		t.Fatalf("id ports: %v", err)
	}
	// Only that id is taken; the id a name makes of itself ("Ports") is the same one.
	if Slugify("Ports") != "ports" {
		t.Fatalf("Slugify(Ports) = %q", Slugify("Ports"))
	}
	for _, id := range []string{"port", "ports-2", "my-ports", "portsa", "p0rts"} {
		if err := v.Validate(feed(id)); err != nil {
			t.Errorf("id %s: %v", id, err)
		}
	}
	// A malformed id is reported as it was, once.
	err = v.Validate(feed("Ports"))
	if !errors.As(err, &ve) || len(ve.Problems) != 1 || ve.Problems[0].Path != "/id" || ve.Problems[0].Message == "is reserved" {
		t.Errorf("id Ports: %v", err)
	}
}

// problemPaths lists the paths of the problems Validate reports for f.
func problemPaths(t *testing.T, f *Feed) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := newValidator().Validate(f)
	if err == nil {
		return out
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected a ValidationError, got %v", err)
	}
	for _, p := range ve.Problems {
		out[p.Path] = p.Message
	}
	return out
}

// A secret the schema requires under the config (x-secrets requiredWhen, which
// the form enforces) is required by the server too: a TLS listener without its
// certificate would otherwise be created, then fail to start.
func TestSecretsRequiredWhenTheSchemaSaysSo(t *testing.T) {
	const ref = "env:SENTINEL_SECRET_X"
	cases := []struct {
		name    string
		config  string
		refs    map[string]string
		missing []string // paths expected to be reported "is required"
	}{
		{"listen with TLS, no secrets", `{"mode":"listen","port":6000,"tls":{"enabled":true}}`, nil, []string{"/secretRefs/tlsCert", "/secretRefs/tlsKey"}},
		{"listen with TLS, only the cert", `{"mode":"listen","port":6000,"tls":{"enabled":true}}`, map[string]string{"tlsCert": ref}, []string{"/secretRefs/tlsKey"}},
		{"listen with TLS, both", `{"mode":"listen","port":6000,"tls":{"enabled":true}}`, map[string]string{"tlsCert": ref, "tlsKey": ref}, nil},
		{"mutual TLS needs the client CA", `{"mode":"listen","port":6000,"tls":{"enabled":true,"requireClientCert":true}}`, map[string]string{"tlsCert": ref, "tlsKey": ref}, []string{"/secretRefs/clientCa"}},
		{"mutual TLS, all three", `{"mode":"listen","port":6000,"tls":{"enabled":true,"requireClientCert":true}}`, map[string]string{"tlsCert": ref, "tlsKey": ref, "clientCa": ref}, nil},
		{"listen without TLS", `{"mode":"listen","port":6000}`, nil, nil},
		{"TLS explicitly off", `{"mode":"listen","port":6000,"tls":{"enabled":false}}`, nil, nil},
		{"connect with TLS: the client cert is optional", `{"mode":"connect","host":"example.com","port":7000,"tls":{"enabled":true}}`, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := problemPaths(t, &Feed{ID: "t", Name: "T", Driver: "tcp", Config: json.RawMessage(tc.config), SecretRefs: tc.refs})
			for _, path := range tc.missing {
				if got[path] != "is required" {
					t.Errorf("%s: problems = %v, want %q is required", tc.name, got, path)
				}
			}
			for path, msg := range got {
				if msg == "is required" && !contains(tc.missing, path) {
					t.Errorf("%s: %s is reported required, and should not be", tc.name, path)
				}
			}
		})
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// The stream driver's schema states no requiredWhen (a stream TLS listener can
// take its files from certFile and keyFile), so nothing is demanded of it.
func TestSecretsAreNotRequiredWhereTheSchemaSaysNothing(t *testing.T) {
	f := &Feed{ID: "s", Name: "S", Driver: "stream", Config: json.RawMessage(`{"transport":{"kind":"tcpserver","address":"127.0.0.1:6000","tls":{"enabled":true}},"framing":"line","decoder":{"name":"sbs"}}`)}
	for path, msg := range problemPaths(t, f) {
		if strings.HasPrefix(path, "/secretRefs/") {
			t.Errorf("%s: %s", path, msg)
		}
	}
}

// Conditions read as the client reads them: a path walks nested keys, a list of
// values matches any, a missing value counts as its schema default, and every
// condition has to hold.
func TestRequiredSecretsConditions(t *testing.T) {
	info := drivers.Info{ConfigSchema: json.RawMessage(`{
	  "properties": {
	    "mode": {"type":"string","default":"listen"},
	    "level": {"type":"integer"},
	    "net": {"properties": {"tls": {"properties": {"on": {"type":"boolean","default":true}}}}}
	  },
	  "x-secrets": {
	    "a": {"requiredWhen": {"mode": "listen", "net/tls/on": true}},
	    "b": {"requiredWhen": {"mode": ["connect", "relay"]}},
	    "c": {"requiredWhen": {"level": [1, 2]}},
	    "d": {"title": "no condition"}
	  }
	}`)}
	cases := []struct {
		config string
		refs   map[string]string
		want   []string
	}{
		{`{}`, nil, []string{"a"}},
		{`{"net":{"tls":{"on":false}}}`, nil, nil},
		{`{"mode":"relay","net":{"tls":{"on":false}}}`, nil, []string{"b"}},
		{`{"mode":" connect ","net":{"tls":{"on":false}}}`, nil, []string{"b"}},
		{`{"mode":"","net":{"tls":{"on":null}}}`, nil, []string{"a"}},
		{`{"level":2,"net":{"tls":{"on":false}}}`, nil, []string{"c"}},
		{`{"level":3,"net":{"tls":{"on":false}}}`, nil, nil},
		{`{"level":1}`, map[string]string{"a": "env:X"}, []string{"c"}},
		{`{"Mode":"connect","net":{"tls":{"on":false}}}`, nil, []string{"b"}},
		{`[]`, nil, nil},
	}
	for _, tc := range cases {
		got := requiredSecrets(info, json.RawMessage(tc.config), tc.refs)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("config %s, refs %v: required = %v, want %v", tc.config, tc.refs, got, tc.want)
		}
	}
}
