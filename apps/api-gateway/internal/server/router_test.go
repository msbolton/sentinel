package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/middleware"

	"github.com/sentinel/api-gateway/internal/auth"
	"github.com/sentinel/api-gateway/internal/proxy"
)

var users = map[string]*auth.User{
	"viewer":   {UserID: "v", Username: "viewer", Roles: []string{auth.RoleViewer}, ClassificationLevel: "UNCLASSIFIED"},
	"operator": {UserID: "o", Username: "operator", Roles: []string{auth.RoleOperator}, ClassificationLevel: "UNCLASSIFIED"},
	"analyst":  {UserID: "a", Username: "analyst", Roles: []string{auth.RoleAnalyst}, ClassificationLevel: "CONFIDENTIAL"},
	"lowadmin": {UserID: "l", Username: "lowadmin", Roles: []string{auth.RoleAdmin}, ClassificationLevel: "UNCLASSIFIED"},
	"admin":    {UserID: "d", Username: "admin", Roles: []string{auth.RoleAdmin}, ClassificationLevel: "TOP_SECRET"},
}

var verifier = auth.VerifierFunc(func(_ context.Context, raw string) (*auth.User, error) {
	if u, ok := users[raw]; ok {
		return u, nil
	}
	return nil, errors.New("bad token")
})

type echo struct {
	Path  string `json:"path"`
	User  string `json:"user"`
	Roles string `json:"roles"`
}

func echoServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(echo{Path: r.URL.Path, User: r.Header.Get(middleware.HeaderUserID), Roles: r.Header.Get(middleware.HeaderUserRoles)})
	}))
}

type noopRoutes struct{}

func (noopRoutes) RegisterRoutes(*http.ServeMux) {}

func newGateway(t *testing.T, up *httptest.Server) http.Handler {
	t.Helper()
	mk := func(name string, rewrite func(string) string) http.Handler {
		h, err := proxy.New(proxy.Options{Name: name, Target: up.URL, RewritePath: rewrite})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	return New(Deps{
		Logger:     zap.NewNop(),
		Auth:       auth.NewMiddleware(verifier, false, zap.NewNop()),
		CORSOrigin: "http://localhost:4200",
		Upstreams: Upstreams{
			Entities:  mk("entity-service", nil),
			Tracks:    mk("track-service", nil),
			Alerts:    mk("alert-service", nil),
			Links:     mk("link-analysis-service", nil),
			Search:    mk("search-service", nil),
			Feeds:     mk("ingest-service", proxy.StripPrefix("/api/v1")),
			Analytics: mk("analytics-service", nil),
		},
		Locations:   noopRoutes{},
		ReadyChecks: map[string]func() error{"database": func() error { return nil }},
	})
}

func req(h http.Handler, method, target, token string) (*httptest.ResponseRecorder, echo) {
	r := httptest.NewRequest(method, target, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	var e echo
	_ = json.Unmarshal(rr.Body.Bytes(), &e)
	return rr, e
}

func TestRouter_HealthIsPublic(t *testing.T) {
	up := echoServer(t)
	defer up.Close()
	g := newGateway(t, up)
	for _, p := range []string{"/api/v1/health", "/api/v1/health/ready", "/health", "/ready"} {
		if rr, _ := req(g, http.MethodGet, p, ""); rr.Code != 200 {
			t.Errorf("%s: %d", p, rr.Code)
		}
	}
	rr, _ := req(g, http.MethodGet, "/api/v1/health/ready", "")
	var body map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["service"] != "api-gateway" || body["checks"].(map[string]interface{})["database"] == nil {
		t.Errorf("readiness body = %s", rr.Body.String())
	}
}

func TestRouter_ProtectedRoutesRequireToken(t *testing.T) {
	up := echoServer(t)
	defer up.Close()
	g := newGateway(t, up)
	for _, p := range []string{"/api/v1/entities", "/api/v1/alerts", "/api/v1/links/graph", "/api/v1/search?q=a", "/api/v1/tracks/x", "/api/v1/feeds", "/api/v1/entities/ageout-config"} {
		if rr, _ := req(g, http.MethodGet, p, ""); rr.Code != http.StatusUnauthorized {
			t.Errorf("%s without token: %d", p, rr.Code)
		}
		if rr, _ := req(g, http.MethodGet, p, "viewer"); rr.Code != http.StatusOK {
			t.Errorf("%s as viewer: %d %s", p, rr.Code, rr.Body.String())
		}
	}
}

func TestRouter_ProxiesWithIdentity(t *testing.T) {
	up := echoServer(t)
	defer up.Close()
	g := newGateway(t, up)
	rr, e := req(g, http.MethodGet, "/api/v1/entities/abc", "analyst")
	if rr.Code != 200 || e.Path != "/api/v1/entities/abc" || e.User != "a" || e.Roles != auth.RoleAnalyst {
		t.Errorf("proxy: %d %+v", rr.Code, e)
	}
	_, e = req(g, http.MethodGet, "/api/v1/feeds/f1", "viewer")
	if e.Path != "/feeds/f1" {
		t.Errorf("feeds path = %q, want /feeds/f1", e.Path)
	}
	_, e = req(g, http.MethodGet, "/api/v1/emplacements/e1", "viewer")
	if e.Path != "/emplacements/e1" {
		t.Errorf("emplacements path = %q, want /emplacements/e1", e.Path)
	}
}

func TestRouter_RolePolicies(t *testing.T) {
	up := echoServer(t)
	defer up.Close()
	g := newGateway(t, up)

	cases := []struct {
		method, path, token string
		want                int
	}{
		{http.MethodPost, "/api/v1/entities", "viewer", 403},
		{http.MethodPost, "/api/v1/entities", "analyst", 200},
		{http.MethodPatch, "/api/v1/entities/x/position", "analyst", 200},
		{http.MethodDelete, "/api/v1/entities/x", "analyst", 403},
		{http.MethodDelete, "/api/v1/entities/x", "lowadmin", 403}, // admin but below SECRET
		{http.MethodDelete, "/api/v1/entities/x", "admin", 200},
		{http.MethodDelete, "/api/v1/entities", "admin", 200},
		{http.MethodPut, "/api/v1/entities/ageout-config", "analyst", 403},
		{http.MethodPut, "/api/v1/entities/ageout-config", "admin", 200},
		{http.MethodDelete, "/api/v1/entities/ageout-config/x", "lowadmin", 200}, // operator rule, no clearance gate
		{http.MethodPatch, "/api/v1/alerts/x/acknowledge", "viewer", 403},
		{http.MethodPatch, "/api/v1/alerts/x/acknowledge", "analyst", 200},
		{http.MethodPost, "/api/v1/alerts/rules", "analyst", 200},
		{http.MethodPatch, "/api/v1/analytics/findings/x", "viewer", 403},
		{http.MethodPatch, "/api/v1/analytics/findings/x", "analyst", 200},
		{http.MethodGet, "/api/v1/analytics/findings", "viewer", 200},
		{http.MethodPost, "/api/v1/links", "viewer", 403},
		{http.MethodDelete, "/api/v1/links/x", "analyst", 200},
		{http.MethodPut, "/api/v1/feeds/x", "analyst", 403},
		{http.MethodPut, "/api/v1/feeds/x", "admin", 200},
		{http.MethodGet, "/api/v1/feeds", "viewer", 200},
		{http.MethodGet, "/api/v1/emplacements", "viewer", 200},
		{http.MethodPost, "/api/v1/emplacements", "viewer", 403},
		{http.MethodPost, "/api/v1/emplacements", "analyst", 403},
		{http.MethodPatch, "/api/v1/emplacements/x", "analyst", 403},
		{http.MethodPost, "/api/v1/emplacements", "operator", 200},
		{http.MethodPatch, "/api/v1/emplacements/x", "operator", 200},
		{http.MethodDelete, "/api/v1/emplacements/x", "analyst", 403},
		{http.MethodDelete, "/api/v1/emplacements/x", "lowadmin", 200},
	}
	for _, c := range cases {
		rr, _ := req(g, c.method, c.path, c.token)
		if rr.Code != c.want {
			t.Errorf("%s %s as %s: got %d, want %d (%s)", c.method, c.path, c.token, rr.Code, c.want, rr.Body.String())
		}
	}
}

func TestRouter_SecurityAndCORSHeaders(t *testing.T) {
	up := echoServer(t)
	defer up.Close()
	g := newGateway(t, up)
	rr, _ := req(g, http.MethodGet, "/api/v1/health", "")
	if rr.Header().Get("X-Frame-Options") != "DENY" || rr.Header().Get("Access-Control-Allow-Origin") != "http://localhost:4200" {
		t.Errorf("headers = %v", rr.Header())
	}
	rr, _ = req(g, http.MethodOptions, "/api/v1/entities", "")
	if rr.Code != http.StatusNoContent {
		t.Errorf("preflight should be 204, got %d", rr.Code)
	}
}

func TestFindPolicy_SpecificPrefixWins(t *testing.T) {
	p, ok := findPolicy(DefaultPolicies, http.MethodDelete, "/api/v1/entities/ageout-config/abc")
	if !ok || p.Prefix != "/api/v1/entities/ageout-config" {
		t.Errorf("got %+v", p)
	}
	if _, ok := findPolicy(DefaultPolicies, http.MethodGet, "/api/v1/entities"); ok {
		t.Error("GET entities should have no policy")
	}
	if _, ok := findPolicy(DefaultPolicies, http.MethodPost, "/api/v1/entitiesX"); ok {
		t.Error("prefix must match on path boundary")
	}
}

func TestDriversAreProxiedToIngest(t *testing.T) {
	up := echoServer(t)
	defer up.Close()
	h := newGateway(t, up)
	rr, e := req(h, http.MethodGet, "/api/v1/drivers/opensky", "viewer")
	if rr.Code != http.StatusOK || e.Path != "/drivers/opensky" {
		t.Fatalf("status %d path %q", rr.Code, e.Path)
	}
	if rr, _ := req(h, http.MethodGet, "/api/v1/drivers", ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous drivers status %d", rr.Code)
	}
}
