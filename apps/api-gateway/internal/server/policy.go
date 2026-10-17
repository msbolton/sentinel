package server

import (
	"net/http"
	"strings"

	"github.com/sentinel/api-gateway/internal/auth"
)

// Policy maps a method and path prefix to the roles allowed to call it.
// A prefix matches the exact path and any sub-path.
type Policy struct {
	Method string
	Prefix string
	Roles  []string
	// Classification, when set, is the minimum clearance required.
	Classification string
}

func (p Policy) matches(method, path string) bool {
	if p.Method != "" && p.Method != method {
		return false
	}
	return path == p.Prefix || strings.HasPrefix(path, p.Prefix+"/")
}

// findPolicy returns the first policy matching the request, if any.
func findPolicy(policies []Policy, method, path string) (Policy, bool) {
	for _, p := range policies {
		if p.matches(method, path) {
			return p, true
		}
	}
	return Policy{}, false
}

var (
	analystOrAdmin  = []string{auth.RoleAnalyst, auth.RoleAdmin}
	operatorOrAdmin = []string{auth.RoleOperator, auth.RoleAdmin}
	adminOnly       = []string{auth.RoleAdmin}
)

// DefaultPolicies reproduces the role rules of the NestJS controllers using the
// role names Keycloak actually issues. Order matters: specific prefixes first.
var DefaultPolicies = []Policy{
	// Entity ageout configuration (operational tuning)
	{Method: http.MethodPut, Prefix: "/api/v1/entities/ageout-config", Roles: operatorOrAdmin},
	{Method: http.MethodDelete, Prefix: "/api/v1/entities/ageout-config", Roles: operatorOrAdmin},
	// Entities
	{Method: http.MethodPost, Prefix: "/api/v1/entities", Roles: analystOrAdmin},
	{Method: http.MethodPut, Prefix: "/api/v1/entities", Roles: analystOrAdmin},
	{Method: http.MethodPatch, Prefix: "/api/v1/entities", Roles: analystOrAdmin},
	{Method: http.MethodDelete, Prefix: "/api/v1/entities", Roles: adminOnly, Classification: "SECRET"},
	// Alerts: acknowledge, resolve, and rule management
	{Method: http.MethodPatch, Prefix: "/api/v1/alerts", Roles: analystOrAdmin},
	{Method: http.MethodPatch, Prefix: "/api/v1/analytics/findings", Roles: analystOrAdmin},
	{Method: http.MethodPost, Prefix: "/api/v1/alerts/rules", Roles: analystOrAdmin},
	{Method: http.MethodPut, Prefix: "/api/v1/alerts/rules", Roles: analystOrAdmin},
	// Links
	{Method: http.MethodPost, Prefix: "/api/v1/links", Roles: analystOrAdmin},
	{Method: http.MethodDelete, Prefix: "/api/v1/links", Roles: analystOrAdmin},
	// Feeds (ingest control)
	{Method: http.MethodPost, Prefix: "/api/v1/feeds", Roles: operatorOrAdmin},
	{Method: http.MethodPut, Prefix: "/api/v1/feeds", Roles: operatorOrAdmin},
	{Method: http.MethodPatch, Prefix: "/api/v1/feeds", Roles: operatorOrAdmin},
	{Method: http.MethodDelete, Prefix: "/api/v1/feeds", Roles: operatorOrAdmin},
	// Locations
	{Method: http.MethodPost, Prefix: "/api/v1/locations", Roles: analystOrAdmin},
	{Method: http.MethodPatch, Prefix: "/api/v1/locations", Roles: analystOrAdmin},
	{Method: http.MethodDelete, Prefix: "/api/v1/locations", Roles: adminOnly},
	// User administration
	{Method: "", Prefix: "/api/v1/auth/pending-registrations", Roles: adminOnly},
	{Method: "", Prefix: "/api/v1/auth/approve-registration", Roles: adminOnly},
	{Method: "", Prefix: "/api/v1/auth/reject-registration", Roles: adminOnly},
	{Method: "", Prefix: "/api/v1/auth/users", Roles: adminOnly},
}

// EnforcePolicies wraps next with a role check derived from the first matching policy.
func EnforcePolicies(policies []Policy, mw *auth.Middleware) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p, ok := findPolicy(policies, r.Method, r.URL.Path); ok {
				h := next
				if p.Classification != "" {
					h = mw.RequireClassification(p.Classification)(h)
				}
				mw.RequireRoles(p.Roles...)(h).ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
