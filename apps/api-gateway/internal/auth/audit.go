package auth

import (
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Audit emits structured security audit events, mirroring the NestJS AuthAuditService.
type Audit struct {
	logger *zap.Logger
}

// NewAudit creates an audit logger.
func NewAudit(logger *zap.Logger) *Audit {
	return &Audit{logger: logger.Named("AuthAudit")}
}

func baseFields(eventType string, r *http.Request, u *User) []zap.Field {
	fields := []zap.Field{
		zap.String("eventType", eventType),
		zap.String("method", r.Method),
		zap.String("resource", r.URL.Path),
		zap.String("ip", clientIP(r)),
		zap.String("userAgent", r.UserAgent()),
		zap.String("timestamp", time.Now().UTC().Format(time.RFC3339)),
	}
	if u != nil {
		fields = append(fields, zap.String("userId", u.UserID), zap.String("username", u.Username))
	}
	return fields
}

// AccessDenied records a request rejected before authorization (missing or invalid token).
func (a *Audit) AccessDenied(r *http.Request, u *User, reason string) {
	a.logger.Warn("access denied", append(baseFields("ACCESS_DENIED", r, u), zap.String("reason", reason))...)
}

// RoleCheckFailed records an authenticated user lacking a required role.
func (a *Audit) RoleCheckFailed(r *http.Request, u *User, required []string) {
	a.logger.Warn("role check failed", append(baseFields("ROLE_CHECK_FAILED", r, u),
		zap.Strings("requiredRoles", required),
		zap.Strings("userRoles", u.Roles),
	)...)
}

// RoleDenied records an authenticated user refused because they hold a role
// the route excludes.
func (a *Audit) RoleDenied(r *http.Request, u *User, denied []string) {
	a.logger.Warn("role denied", append(baseFields("ROLE_DENIED", r, u),
		zap.Strings("deniedRoles", denied),
		zap.Strings("userRoles", u.Roles),
	)...)
}

// ClassificationCheckFailed records an authenticated user lacking the required clearance.
func (a *Audit) ClassificationCheckFailed(r *http.Request, u *User, required string) {
	a.logger.Warn("classification check failed", append(baseFields("CLASSIFICATION_CHECK_FAILED", r, u),
		zap.String("requiredClassification", required),
		zap.String("userClassification", u.ClassificationLevel),
	)...)
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return host
}
