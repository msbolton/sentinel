package registry

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sentinel/ingest-service/internal/drivers"
)

func newValidator() *Validator { return &Validator{Catalog: drivers.Default()} }

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"My MQTT Feed":        "my-mqtt-feed",
		"  readsb @ site-1 ": "readsb-site-1",
		"ALLCAPS":             "allcaps",
		"---":                 "",
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
