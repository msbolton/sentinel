package middleware

import (
	"context"
	"net/http"
	"strings"
)

// Auth header names propagated by the API gateway.
const (
	HeaderUserID         = "x-sentinel-user-id"
	HeaderUserRoles      = "x-sentinel-user-roles"
	HeaderClassification = "x-sentinel-classification"
)

type contextKey string

const (
	ctxUserID         contextKey = "userID"
	ctxUserRoles      contextKey = "userRoles"
	ctxClassification contextKey = "classification"
)

// AuthFromHeaders extracts auth context from headers set by the API gateway.
func AuthFromHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if userID := r.Header.Get(HeaderUserID); userID != "" {
			ctx = context.WithValue(ctx, ctxUserID, userID)
		}
		if roles := r.Header.Get(HeaderUserRoles); roles != "" {
			ctx = context.WithValue(ctx, ctxUserRoles, strings.Split(roles, ","))
		}
		if classification := r.Header.Get(HeaderClassification); classification != "" {
			ctx = context.WithValue(ctx, ctxClassification, classification)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// UserID returns the authenticated user ID from the request context.
func UserID(ctx context.Context) string {
	if v, ok := ctx.Value(ctxUserID).(string); ok {
		return v
	}
	return ""
}

// UserRoles returns the authenticated user roles from the request context.
func UserRoles(ctx context.Context) []string {
	if v, ok := ctx.Value(ctxUserRoles).([]string); ok {
		return v
	}
	return nil
}

// Classification returns the classification level from the request context.
func Classification(ctx context.Context) string {
	if v, ok := ctx.Value(ctxClassification).(string); ok {
		return v
	}
	return ""
}
