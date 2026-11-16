package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/sentinel/go-common/middleware"
	"go.uber.org/zap"
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

func TestLevelsOfFollowTheCallersClearance(t *testing.T) {
	cases := map[string][]string{
		"":             {"UNCLASSIFIED"},
		"garbage":      {"UNCLASSIFIED"},
		"confidential": {"UNCLASSIFIED", "CONFIDENTIAL"},
		"TOP_SECRET":   {"UNCLASSIFIED", "CONFIDENTIAL", "SECRET", "TOP_SECRET"},
	}
	for header, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/links/graph", nil)
		if header != "" {
			r.Header.Set(middleware.HeaderClassification, header)
		}
		var got []string
		middleware.AuthFromHeaders(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got = levelsOf(r)
		})).ServeHTTP(httptest.NewRecorder(), r)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("clearance %q -> %v, want %v", header, got, want)
		}
	}
}

func TestIDsAndLinkTypesAreValidatedBeforeTheStore(t *testing.T) {
	h := NewLinkHandler(nil, zap.NewNop())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	const a = "11111111-1111-4111-8111-111111111111"

	cases := []struct {
		method, target, body string
		want                 int
	}{
		{http.MethodGet, "/api/v1/links?entityId=not-a-uuid", "", http.StatusBadRequest},
		{http.MethodGet, "/api/v1/links/graph?centerId=", "", http.StatusBadRequest},
		{http.MethodGet, "/api/v1/links/graph?centerId=" + a + "&maxDepth=0", "", http.StatusBadRequest},
		{http.MethodGet, "/api/v1/links/shortest-path?from=" + a + "&to=x", "", http.StatusBadRequest},
		{http.MethodDelete, "/api/v1/links/x", "", http.StatusBadRequest},
		{http.MethodPost, "/api/v1/links", `{"sourceEntityId":"x","targetEntityId":"` + a + `","linkType":"ASSOCIATION"}`, http.StatusBadRequest},
		{http.MethodPost, "/api/v1/links", `{"sourceEntityId":"` + a + `","targetEntityId":"` + a + `","linkType":"ASSOCIATION"}`, http.StatusBadRequest},
		{http.MethodPost, "/api/v1/links", `{"sourceEntityId":"` + a + `","targetEntityId":"22222222-2222-4222-8222-222222222222","linkType":"not a type"}`, http.StatusBadRequest},
		{http.MethodPost, "/api/v1/links", `{"sourceEntityId":"` + a + `","targetEntityId":"22222222-2222-4222-8222-222222222222","linkType":"ASSOCIATION","confidence":2}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.target, strings.NewReader(c.body))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s %s -> %d, want %d: %s", c.method, c.target, w.Code, c.want, w.Body.String())
		}
	}
}
