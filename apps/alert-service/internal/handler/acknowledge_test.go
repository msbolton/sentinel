package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sentinel/go-common/middleware"
)

func viaGateway(r *http.Request, userID string) *http.Request {
	rec := httptest.NewRecorder()
	var out *http.Request
	h := middleware.AuthFromHeaders(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) { out = req }))
	r.Header.Set(middleware.HeaderUserID, userID)
	h.ServeHTTP(rec, r)
	return out
}

func TestAcknowledgingUser(t *testing.T) {
	// The gateway's identity wins over anything in the body.
	r := viaGateway(httptest.NewRequest(http.MethodPatch, "/api/v1/alerts/a/acknowledge", strings.NewReader(`{"userId":"spoof"}`)), "w.operator")
	if got := acknowledgingUser(r); got != "w.operator" {
		t.Errorf("gateway user = %q", got)
	}
	// Direct callers (no gateway) may name themselves in the body.
	r = httptest.NewRequest(http.MethodPatch, "/api/v1/alerts/a/acknowledge", strings.NewReader(`{"userId":"direct"}`))
	if got := acknowledgingUser(r); got != "direct" {
		t.Errorf("body user = %q", got)
	}
	// The client sends an empty body; without a gateway identity there is nobody to record.
	r = httptest.NewRequest(http.MethodPatch, "/api/v1/alerts/a/acknowledge", strings.NewReader(`{}`))
	if got := acknowledgingUser(r); got != "" {
		t.Errorf("empty = %q", got)
	}
	r = httptest.NewRequest(http.MethodPatch, "/api/v1/alerts/a/acknowledge", nil)
	if got := acknowledgingUser(r); got != "" {
		t.Errorf("nil body = %q", got)
	}
}
