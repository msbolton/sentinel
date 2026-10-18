package server

import (
	"net/http"
	"testing"

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
