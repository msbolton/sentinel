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

func TestResolveSecrets(t *testing.T) {
	lookup := func(name string) (string, bool) {
		if name == "OPENSKY_CLIENT_ID" {
			return "id-1", true
		}
		return "", false
	}
	got, err := ResolveSecrets(map[string]string{"clientId": "env:OPENSKY_CLIENT_ID"}, lookup)
	if err != nil || got["clientId"] != "id-1" {
		t.Fatalf("got %v err %v", got, err)
	}
	if _, err := ResolveSecrets(map[string]string{"clientSecret": "env:MISSING"}, lookup); err == nil {
		t.Error("missing variable must fail")
	}
	if _, err := ResolveSecrets(map[string]string{"clientSecret": "plain"}, lookup); err == nil {
		t.Error("plain value must fail")
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
	got, err := ResolveSecretsWith(map[string]string{"clientCert": "file:/etc/sentinel/tak/client.pem"}, nil, read)
	if err != nil || !strings.HasPrefix(got["clientCert"], "-----BEGIN CERTIFICATE-----") || strings.HasSuffix(got["clientCert"], "\n") {
		t.Fatalf("got %q err %v", got["clientCert"], err)
	}
	if _, err := ResolveSecretsWith(map[string]string{"k": "file:/etc/sentinel/empty"}, nil, read); err == nil {
		t.Error("empty file must fail")
	}
	if _, err := ResolveSecretsWith(map[string]string{"k": "file:/etc/sentinel/missing"}, nil, read); err == nil {
		t.Error("missing file must fail")
	}
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
