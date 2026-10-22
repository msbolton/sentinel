package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/sentinel/api-gateway/internal/auth"
)

func contains(roles []string, role string) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}

// Geofences are alert rules, and operators draw them: every write to the
// rule routes is open to operators, analysts and admins alike.
func TestRulePoliciesOpenToOperators(t *testing.T) {
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		p, ok := findPolicy(DefaultPolicies, m, "/api/v1/alerts/rules/abc")
		if !ok {
			t.Fatalf("%s rules: no policy", m)
		}
		for _, role := range []string{auth.RoleOperator, auth.RoleAnalyst, auth.RoleAdmin} {
			if !contains(p.Roles, role) {
				t.Fatalf("%s rules: roles %v lack %s", m, p.Roles, role)
			}
		}
	}
}

// policyRequest runs one request as a user with the given roles through
// EnforcePolicies and reports the status and whether the handler was reached.
func policyRequest(t *testing.T, method, path string, roles ...string) (code int, reached bool) {
	t.Helper()
	mw := auth.NewMiddleware(nil, false, zap.NewNop())
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(method, path, nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		UserID: "u1", Username: "u1", Roles: roles,
	}))
	rec := httptest.NewRecorder()
	EnforcePolicies(DefaultPolicies, mw)(next).ServeHTTP(rec, req)
	return rec.Code, reached
}

func TestGuestCannotCreateAPIKey(t *testing.T) {
	code, reached := policyRequest(t, http.MethodPost, "/api/v1/auth/api-keys", auth.RoleViewer, auth.RoleGuest)
	if code != http.StatusForbidden || reached {
		t.Fatalf("guest POST api-keys: code %d, reached %v; want 403, not reached", code, reached)
	}
}

func TestGuestCannotDeleteAPIKey(t *testing.T) {
	code, reached := policyRequest(t, http.MethodDelete, "/api/v1/auth/api-keys/abc", auth.RoleViewer, auth.RoleGuest)
	if code != http.StatusForbidden || reached {
		t.Fatalf("guest DELETE api-keys: code %d, reached %v; want 403, not reached", code, reached)
	}
}

func TestViewerCanStillCreateAPIKey(t *testing.T) {
	code, reached := policyRequest(t, http.MethodPost, "/api/v1/auth/api-keys", auth.RoleViewer)
	if !reached || code != http.StatusNoContent {
		t.Fatalf("viewer POST api-keys: code %d, reached %v; want the handler", code, reached)
	}
}
