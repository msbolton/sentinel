package mgmt

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/sentinel/go-common/middleware"
)

// AuthOptions configures the management API's guard.
type AuthOptions struct {
	// ServiceToken is the shared secret the API gateway presents in the
	// X-Sentinel-Service-Token header. Empty disables the check (development).
	ServiceToken string
}

// Roles that manage connections: they may change feeds and read their dead
// letters. The gateway enforces the same rule for changes; the check here
// protects the service when it is reached without the gateway, and is the only
// check on reading dead letters, which the gateway lets any GET through.
var operatorRoles = []string{"sentinel-operator", "sentinel-admin"}

// Roles that create and edit emplacements (inside their connection, on the
// Connections page), matching the gateway's policy; deleting one needs admin.
var (
	emplacementWriters = []string{"sentinel-operator", "sentinel-admin"}
	adminRoles         = []string{"sentinel-admin"}
)

// required is the roles a mutation needs and the refusal that names them.
func required(r *http.Request) ([]string, string) {
	if r.URL.Path == "/emplacements" || strings.HasPrefix(r.URL.Path, "/emplacements/") {
		if r.Method == http.MethodDelete {
			return adminRoles, "admin role required"
		}
		return emplacementWriters, "operator or admin role required"
	}
	return operatorRoles, "operator or admin role required"
}

// Auth returns middleware that requires the service token (when configured)
// on every request and an operator or admin identity on mutations (operator
// or admin for emplacements; admin to delete one). PUT on
// an area endpoint is exempt from the role check because the gateway's
// viewport sync has no user behind it.
func Auth(opts AuthOptions) func(http.Handler) http.Handler {
	token := []byte(opts.ServiceToken)
	return func(next http.Handler) http.Handler {
		next = middleware.AuthFromHeaders(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(token) > 0 {
				got := []byte(r.Header.Get(middleware.HeaderServiceToken))
				if subtle.ConstantTimeCompare(got, token) != 1 {
					writeError(w, http.StatusUnauthorized, "missing or invalid service token")
					return
				}
			}
			if isMutation(r) && !isAreaPush(r) {
				roles := strings.Split(r.Header.Get(middleware.HeaderUserRoles), ",")
				if want, msg := required(r); !hasAny(roles, want) {
					writeError(w, http.StatusForbidden, msg)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// isOperator reports whether the caller has an operator or admin role.
func isOperator(r *http.Request) bool {
	return hasAny(middleware.UserRoles(r.Context()), operatorRoles)
}

func isMutation(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func isAreaPush(r *http.Request) bool {
	if r.Method != http.MethodPut {
		return false
	}
	return strings.HasSuffix(r.URL.Path, "/area") || strings.HasSuffix(r.URL.Path, "/viewport")
}

func hasAny(have, want []string) bool {
	for _, h := range have {
		h = strings.TrimSpace(h)
		for _, w := range want {
			if h == w {
				return true
			}
		}
	}
	return false
}
