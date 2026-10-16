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

// Roles allowed to change feeds. The gateway enforces the same rule; the
// check here protects the service when it is reached without the gateway.
var mutatingRoles = []string{"sentinel-operator", "sentinel-admin"}

// Auth returns middleware that requires the service token (when configured)
// on every request and an operator or admin identity on mutations. PUT on
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
				if !hasAny(roles, mutatingRoles) {
					writeError(w, http.StatusForbidden, "operator or admin role required")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
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
