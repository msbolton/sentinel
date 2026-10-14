package auth

import (
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/httputil"
)

// Middleware provides authentication and authorization handlers.
type Middleware struct {
	verifier  Verifier
	devBypass bool
	audit     *Audit
	logger    *zap.Logger
}

// NewMiddleware creates the auth middleware. When devBypass is true every
// request is treated as the dev user and tokens are never checked.
func NewMiddleware(verifier Verifier, devBypass bool, logger *zap.Logger) *Middleware {
	if devBypass {
		logger.Warn("AUTH_DEV_BYPASS enabled: all requests run as the dev user; never use in production")
	}
	return &Middleware{
		verifier:  verifier,
		devBypass: devBypass,
		audit:     NewAudit(logger),
		logger:    logger,
	}
}

// TokenFromRequest extracts a bearer token from the Authorization header, or
// from the `token` query parameter as a fallback for WebSocket clients.
func TokenFromRequest(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		parts := strings.SplitN(h, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return strings.TrimSpace(parts[1])
		}
		return ""
	}
	return r.URL.Query().Get("token")
}

// RequireAuth authenticates the request and stores the user on the context.
func (m *Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.devBypass {
			next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), DevUser())))
			return
		}

		raw := TokenFromRequest(r)
		if raw == "" {
			m.audit.AccessDenied(r, nil, "missing bearer token")
			httputil.Error(w, http.StatusUnauthorized, "Unauthorized")
			return
		}

		user, err := m.verifier.Verify(r.Context(), raw)
		if err != nil {
			m.audit.AccessDenied(r, nil, err.Error())
			httputil.Error(w, http.StatusUnauthorized, "Unauthorized")
			return
		}

		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), user)))
	})
}

// RequireRoles allows the request through when the user holds any of the roles.
func (m *Middleware) RequireRoles(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFrom(r.Context())
			if !ok {
				httputil.Error(w, http.StatusForbidden, "No authenticated user found")
				return
			}
			if len(roles) > 0 && !user.HasAnyRole(roles...) {
				m.audit.RoleCheckFailed(r, user, roles)
				httputil.Error(w, http.StatusForbidden, "Insufficient role permissions for this resource")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireClassification allows the request through when the user's clearance
// is at or above the given level.
func (m *Middleware) RequireClassification(level string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFrom(r.Context())
			if !ok {
				httputil.Error(w, http.StatusForbidden, "No authenticated user found")
				return
			}
			if !user.MeetsClassification(level) {
				m.audit.ClassificationCheckFailed(r, user, level)
				httputil.Error(w, http.StatusForbidden, "Insufficient classification level for this resource")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
