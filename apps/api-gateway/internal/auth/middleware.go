package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/httputil"
)

// APIKeyPrefix starts every API key secret, so the middleware can tell one
// from a JWT without trying to parse it.
const APIKeyPrefix = "sk_"

// apiKeyQueryPaths are the only paths where a key may travel in the query
// string (KML network links and GIS tools cannot set headers).
const apiKeyQueryPrefix = "/api/v1/export/"

// KeyResolver turns an API key secret into a principal. ErrInvalidKey (or
// any error) denies the request.
type KeyResolver interface {
	ResolveAPIKey(ctx context.Context, secret string) (*User, error)
}

// Middleware provides authentication and authorization handlers.
type Middleware struct {
	verifier  Verifier
	devBypass bool
	audit     *Audit
	logger    *zap.Logger
	keys      KeyResolver
}

// UseAPIKeys enables API-key authentication through r.
func (m *Middleware) UseAPIKeys(r KeyResolver) { m.keys = r }

// credentialFromRequest is TokenFromRequest plus the api_key query parameter
// on export routes.
func credentialFromRequest(r *http.Request) string {
	if raw := TokenFromRequest(r); raw != "" {
		return raw
	}
	if strings.HasPrefix(r.URL.Path, apiKeyQueryPrefix) {
		return strings.TrimSpace(r.URL.Query().Get("api_key"))
	}
	return ""
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

		raw := credentialFromRequest(r)
		if raw == "" {
			m.audit.AccessDenied(r, nil, "missing bearer token")
			httputil.Error(w, http.StatusUnauthorized, "Unauthorized")
			return
		}

		var user *User
		var err error
		if strings.HasPrefix(raw, APIKeyPrefix) {
			if m.keys == nil {
				err = errors.New("api keys not enabled")
			} else {
				user, err = m.keys.ResolveAPIKey(r.Context(), raw)
			}
		} else {
			user, err = m.verifier.Verify(r.Context(), raw)
		}
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

// DenyRoles refuses the request with 403 when the user holds any of the roles.
func (m *Middleware) DenyRoles(message string, roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFrom(r.Context())
			if !ok {
				httputil.Error(w, http.StatusForbidden, "No authenticated user found")
				return
			}
			if len(roles) > 0 && user.HasAnyRole(roles...) {
				m.audit.RoleDenied(r, user, roles)
				httputil.Error(w, http.StatusForbidden, message)
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
