package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/middleware"

	"github.com/sentinel/api-gateway/internal/auth"
)

type echo struct {
	Path    string            `json:"path"`
	Query   string            `json:"query"`
	Headers map[string]string `json:"headers"`
}

func upstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e := echo{Path: r.URL.Path, Query: r.URL.RawQuery, Headers: map[string]string{}}
		for _, h := range []string{middleware.HeaderUserID, middleware.HeaderUserRoles, middleware.HeaderClassification, "X-Forwarded-For"} {
			e.Headers[h] = r.Header.Get(h)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(e)
	}))
}

func send(h http.Handler, req *http.Request) (*httptest.ResponseRecorder, echo) {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var e echo
	_ = json.Unmarshal(rr.Body.Bytes(), &e)
	return rr, e
}

func TestProxy_InjectsIdentityAndStripsSpoofedHeaders(t *testing.T) {
	up := upstream(t)
	defer up.Close()
	h, err := New(Options{Name: "entity-service", Target: up.URL, Logger: zap.NewNop()})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/entities?page=2", nil)
	req.Header.Set(middleware.HeaderUserID, "spoofed")
	req.Header.Set(middleware.HeaderUserRoles, "sentinel-admin")
	user := &auth.User{UserID: "u-9", Roles: []string{"sentinel-analyst", "sentinel-viewer"}, ClassificationLevel: "SECRET"}
	req = req.WithContext(auth.WithUser(req.Context(), user))

	rr, e := send(h, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if e.Path != "/api/v1/entities" || e.Query != "page=2" {
		t.Errorf("path/query = %q %q", e.Path, e.Query)
	}
	if e.Headers[middleware.HeaderUserID] != "u-9" {
		t.Errorf("user id header = %q", e.Headers[middleware.HeaderUserID])
	}
	if e.Headers[middleware.HeaderUserRoles] != "sentinel-analyst,sentinel-viewer" {
		t.Errorf("roles header = %q", e.Headers[middleware.HeaderUserRoles])
	}
	if e.Headers[middleware.HeaderClassification] != "SECRET" {
		t.Errorf("classification header = %q", e.Headers[middleware.HeaderClassification])
	}
}

func TestProxy_NoUserMeansNoIdentityHeaders(t *testing.T) {
	up := upstream(t)
	defer up.Close()
	h, _ := New(Options{Name: "x", Target: up.URL})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/entities", nil)
	req.Header.Set(middleware.HeaderUserID, "spoofed")
	_, e := send(h, req)
	if e.Headers[middleware.HeaderUserID] != "" {
		t.Errorf("spoofed header leaked: %q", e.Headers[middleware.HeaderUserID])
	}
}

func TestProxy_RewritePath(t *testing.T) {
	up := upstream(t)
	defer up.Close()
	h, _ := New(Options{Name: "ingest-service", Target: up.URL, RewritePath: StripPrefix("/api/v1")})
	_, e := send(h, httptest.NewRequest(http.MethodGet, "/api/v1/feeds/abc", nil))
	if e.Path != "/feeds/abc" {
		t.Errorf("rewritten path = %q, want /feeds/abc", e.Path)
	}
	if StripPrefix("/api/v1")("/api/v1") != "/" {
		t.Error("stripping the whole path should yield /")
	}
}

func TestProxy_UpstreamDownReturns503(t *testing.T) {
	up := upstream(t)
	url := up.URL
	up.Close()
	h, _ := New(Options{Name: "alert-service", Target: url, Logger: zap.NewNop()})
	rr, _ := send(h, httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rr.Code)
	}
	var body map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["message"] != "alert-service unavailable" {
		t.Errorf("body = %s", rr.Body.String())
	}
}

func TestProxy_InvalidTarget(t *testing.T) {
	if _, err := New(Options{Name: "x", Target: "not a url"}); err == nil {
		t.Error("expected error for invalid target")
	}
}
