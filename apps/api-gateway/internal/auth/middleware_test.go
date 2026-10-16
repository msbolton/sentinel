package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

var fakeVerifier = VerifierFunc(func(_ context.Context, raw string) (*User, error) {
	switch raw {
	case "analyst-token":
		return &User{UserID: "u1", Username: "analyst", Roles: []string{RoleAnalyst}, ClassificationLevel: "CONFIDENTIAL"}, nil
	case "admin-token":
		return &User{UserID: "u2", Username: "admin", Roles: []string{RoleAdmin}, ClassificationLevel: "TOP_SECRET"}, nil
	}
	return nil, errors.New("bad token")
})

func echoUser(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFrom(r.Context())
	if !ok {
		http.Error(w, "no user", http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(u)
}

func do(h http.Handler, method, target, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestRequireAuth_DevBypassInjectsDevUser(t *testing.T) {
	mw := NewMiddleware(fakeVerifier, true, zap.NewNop())
	rr := do(mw.RequireAuth(http.HandlerFunc(echoUser)), http.MethodGet, "/x", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var u User
	_ = json.Unmarshal(rr.Body.Bytes(), &u)
	if u.Username != "dev-operator" || !u.HasAnyRole(RoleAdmin) || u.ClassificationLevel != "TOP_SECRET" {
		t.Errorf("dev user not injected: %+v", u)
	}
}

func TestRequireAuth_MissingAndInvalidToken(t *testing.T) {
	mw := NewMiddleware(fakeVerifier, false, zap.NewNop())
	h := mw.RequireAuth(http.HandlerFunc(echoUser))

	for name, token := range map[string]string{"missing": "", "invalid": "nope"} {
		t.Run(name, func(t *testing.T) {
			rr := do(h, http.MethodGet, "/x", token)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401", rr.Code)
			}
			var body map[string]interface{}
			_ = json.Unmarshal(rr.Body.Bytes(), &body)
			if body["statusCode"] != float64(401) || body["message"] != "Unauthorized" {
				t.Errorf("body = %s", rr.Body.String())
			}
		})
	}
}

func TestRequireAuth_ValidTokenSetsUser(t *testing.T) {
	mw := NewMiddleware(fakeVerifier, false, zap.NewNop())
	rr := do(mw.RequireAuth(http.HandlerFunc(echoUser)), http.MethodGet, "/x", "analyst-token")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var u User
	_ = json.Unmarshal(rr.Body.Bytes(), &u)
	if u.Username != "analyst" {
		t.Errorf("user = %+v", u)
	}
}

func TestTokenFromRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ws?token=q-token", nil)
	if got := TokenFromRequest(req); got != "q-token" {
		t.Errorf("query token = %q", got)
	}
	req.Header.Set("Authorization", "Bearer h-token")
	if got := TokenFromRequest(req); got != "h-token" {
		t.Errorf("header token = %q", got)
	}
	req.Header.Set("Authorization", "Basic abc")
	if got := TokenFromRequest(req); got != "" {
		t.Errorf("non-bearer should yield empty, got %q", got)
	}
}

func TestRequireRoles(t *testing.T) {
	mw := NewMiddleware(fakeVerifier, false, zap.NewNop())
	h := mw.RequireAuth(mw.RequireRoles(RoleAdmin)(http.HandlerFunc(echoUser)))

	if rr := do(h, http.MethodPost, "/x", "analyst-token"); rr.Code != http.StatusForbidden {
		t.Errorf("analyst should be forbidden, got %d", rr.Code)
	}
	if rr := do(h, http.MethodPost, "/x", "admin-token"); rr.Code != http.StatusOK {
		t.Errorf("admin should pass, got %d", rr.Code)
	}
	// No user on context at all.
	rr := do(mw.RequireRoles(RoleAdmin)(http.HandlerFunc(echoUser)), http.MethodGet, "/x", "")
	if rr.Code != http.StatusForbidden {
		t.Errorf("no user should be 403, got %d", rr.Code)
	}
}

func TestRequireClassification(t *testing.T) {
	mw := NewMiddleware(fakeVerifier, false, zap.NewNop())
	h := mw.RequireAuth(mw.RequireClassification("SECRET")(http.HandlerFunc(echoUser)))

	if rr := do(h, http.MethodDelete, "/x", "analyst-token"); rr.Code != http.StatusForbidden {
		t.Errorf("CONFIDENTIAL user should fail SECRET gate, got %d", rr.Code)
	}
	if rr := do(h, http.MethodDelete, "/x", "admin-token"); rr.Code != http.StatusOK {
		t.Errorf("TOP_SECRET user should pass SECRET gate, got %d", rr.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	for _, prod := range []bool{false, true} {
		h := SecurityHeaders(prod)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
		rr := do(h, http.MethodGet, "/", "")
		if rr.Header().Get("X-Content-Type-Options") != "nosniff" || rr.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("baseline headers missing: %v", rr.Header())
		}
		hsts := rr.Header().Get("Strict-Transport-Security") != ""
		if hsts != prod {
			t.Errorf("production=%v but HSTS present=%v", prod, hsts)
		}
	}
}

type fakeKeys struct{}

func (fakeKeys) ResolveAPIKey(_ context.Context, secret string) (*User, error) {
	if secret == "sk_good" {
		return &User{UserID: "u9", Username: "key-owner", Roles: []string{RoleViewer}, ClassificationLevel: "SECRET", ViaAPIKey: true}, nil
	}
	return nil, errors.New("invalid api key")
}

func TestRequireAuth_APIKeys(t *testing.T) {
	mw := NewMiddleware(fakeVerifier, false, zap.NewNop())
	h := mw.RequireAuth(http.HandlerFunc(echoUser))

	// Without a resolver an sk_ credential is refused, not parsed as a JWT.
	if rr := do(h, http.MethodGet, "/api/v1/export/entities.kml", "sk_good"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("keys disabled: %d", rr.Code)
	}
	mw.UseAPIKeys(fakeKeys{})

	rr := do(h, http.MethodGet, "/api/v1/entities", "sk_good")
	if rr.Code != http.StatusOK {
		t.Fatalf("bearer key: %d %s", rr.Code, rr.Body.String())
	}
	var u User
	_ = json.Unmarshal(rr.Body.Bytes(), &u)
	if u.UserID != "u9" || u.Roles[0] != RoleViewer || u.ClassificationLevel != "SECRET" {
		t.Errorf("principal = %+v", u)
	}
	if rr := do(h, http.MethodGet, "/api/v1/entities", "sk_bad"); rr.Code != http.StatusUnauthorized {
		t.Errorf("bad key: %d", rr.Code)
	}
	// The query parameter form works only on export routes.
	if rr := do(h, http.MethodGet, "/api/v1/export/entities.kml?api_key=sk_good", ""); rr.Code != http.StatusOK {
		t.Errorf("query key on export: %d", rr.Code)
	}
	if rr := do(h, http.MethodGet, "/api/v1/entities?api_key=sk_good", ""); rr.Code != http.StatusUnauthorized {
		t.Errorf("query key elsewhere: %d", rr.Code)
	}
	// A real token still goes through the verifier.
	if rr := do(h, http.MethodGet, "/api/v1/entities", "analyst-token"); rr.Code != http.StatusOK {
		t.Errorf("jwt: %d", rr.Code)
	}
}
