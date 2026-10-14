package handler

import (
	"net/url"
	"reflect"
	"testing"
)

func TestParseLinkTypes(t *testing.T) {
	cases := map[string][]string{
		"":                      nil,
		"types=A":               {"A"},
		"types=A,B":             {"A", "B"},
		"types=A&types=B":       {"A", "B"},
		"types=A,%20B&types=,C": {"A", "B", "C"},
	}
	for raw, want := range cases {
		q, _ := url.ParseQuery(raw)
		if got := parseLinkTypes(q); !reflect.DeepEqual(got, want) {
			t.Errorf("%q -> %v, want %v", raw, got, want)
		}
	}
}

func TestParseMinConfidence(t *testing.T) {
	q, _ := url.ParseQuery("minConfidence=0.75")
	v, err := parseMinConfidence(q)
	if err != nil || v == nil || *v != 0.75 {
		t.Errorf("got %v %v", v, err)
	}
	if v, err := parseMinConfidence(url.Values{}); err != nil || v != nil {
		t.Error("absent should be nil, nil")
	}
	for _, bad := range []string{"minConfidence=1.5", "minConfidence=-0.1", "minConfidence=abc"} {
		q, _ := url.ParseQuery(bad)
		if _, err := parseMinConfidence(q); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestParseMaxDepth(t *testing.T) {
	if d, err := parseMaxDepth(url.Values{}); err != nil || d != 3 {
		t.Errorf("default = %d %v", d, err)
	}
	q, _ := url.ParseQuery("maxDepth=7")
	if d, err := parseMaxDepth(q); err != nil || d != 7 {
		t.Errorf("explicit = %d %v", d, err)
	}
	for _, bad := range []string{"maxDepth=0", "maxDepth=11", "maxDepth=x"} {
		q, _ := url.ParseQuery(bad)
		if _, err := parseMaxDepth(q); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
