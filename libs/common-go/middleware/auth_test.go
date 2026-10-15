package middleware

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestAuthFromHeaders(t *testing.T) {
	var gotID, gotClass string
	var gotRoles []string
	h := AuthFromHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = UserID(r.Context())
		gotRoles = UserRoles(r.Context())
		gotClass = Classification(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(HeaderUserID, "u-1")
	req.Header.Set(HeaderUserRoles, "sentinel-analyst,sentinel-viewer")
	req.Header.Set(HeaderClassification, "SECRET")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if gotID != "u-1" || gotClass != "SECRET" || !reflect.DeepEqual(gotRoles, []string{"sentinel-analyst", "sentinel-viewer"}) {
		t.Errorf("got %q %v %q", gotID, gotRoles, gotClass)
	}

	// No headers: zero values, never a panic.
	gotID, gotRoles, gotClass = "x", []string{"x"}, "x"
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if gotID != "" || gotRoles != nil || gotClass != "" {
		t.Errorf("without headers got %q %v %q", gotID, gotRoles, gotClass)
	}
}

func TestCORS(t *testing.T) {
	called := false
	h := CORS("http://localhost:4200")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodOptions, "/", nil))
	if rr.Code != http.StatusNoContent || called {
		t.Errorf("preflight: status %d, handler called %v", rr.Code, called)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "http://localhost:4200" {
		t.Errorf("origin header = %q", rr.Header().Get("Access-Control-Allow-Origin"))
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if !called {
		t.Error("GET should reach the handler")
	}
}
