package auth

import (
	"context"
	"strings"
)

// Role names as issued by the Keycloak realm (config/keycloak/sentinel-realm.json).
const (
	RoleAdmin    = "sentinel-admin"
	RoleAnalyst  = "sentinel-analyst"
	RoleOperator = "sentinel-operator"
	RoleViewer   = "sentinel-viewer"
	// RoleGuest marks a throwaway public-demo user: read-only, and kept off
	// the routes that would let it leave anything behind (see server.Policy.Deny).
	RoleGuest = "sentinel-guest"
)

// User is the authenticated principal attached to a request.
type User struct {
	UserID              string   `json:"userId"`
	Username            string   `json:"username"`
	Email               string   `json:"email"`
	Name                string   `json:"name"`
	Roles               []string `json:"roles"`
	RealmRoles          []string `json:"realmRoles"`
	ClientRoles         []string `json:"clientRoles"`
	ClassificationLevel string   `json:"classificationLevel"`
	// ViaAPIKey marks a principal that authenticated with an API key rather
	// than a user token; such a principal may read but never manage keys.
	ViaAPIKey bool `json:"-"`
}

var classificationHierarchy = map[string]int{
	"UNCLASSIFIED": 0,
	"CONFIDENTIAL": 1,
	"SECRET":       2,
	"TOP_SECRET":   3,
}

// classificationRoles maps the realm roles Keycloak assigns to clearance levels.
var classificationRoles = map[string]string{
	"classification-ts": "TOP_SECRET",
	"classification-s":  "SECRET",
	"classification-c":  "CONFIDENTIAL",
	"classification-u":  "UNCLASSIFIED",
}

// ClassificationFromRoles returns the highest clearance encoded by the given
// realm roles, or "" when none is present.
func ClassificationFromRoles(roles []string) string {
	best, bestRank := "", -1
	for _, r := range roles {
		if level, ok := classificationRoles[r]; ok && ClassificationRank(level) > bestRank {
			best, bestRank = level, ClassificationRank(level)
		}
	}
	return best
}

// ClassificationRank returns the numeric rank of a classification level.
// Unknown levels rank as UNCLASSIFIED.
func ClassificationRank(level string) int {
	return classificationHierarchy[strings.ToUpper(level)]
}

// HasAnyRole reports whether the user holds at least one of the given roles.
func (u *User) HasAnyRole(roles ...string) bool {
	for _, want := range roles {
		for _, have := range u.Roles {
			if have == want {
				return true
			}
		}
	}
	return false
}

// MeetsClassification reports whether the user's clearance is at or above the required level.
func (u *User) MeetsClassification(required string) bool {
	return ClassificationRank(u.ClassificationLevel) >= ClassificationRank(required)
}

type ctxKey struct{}

// WithUser stores the user on the context.
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// UserFrom returns the user stored on the context, if any.
func UserFrom(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(ctxKey{}).(*User)
	return u, ok && u != nil
}

// DevUser is injected when AUTH_DEV_BYPASS is enabled. It mirrors the NestJS dev user.
func DevUser() *User {
	roles := []string{RoleAnalyst, RoleOperator, RoleAdmin}
	return &User{
		UserID:              "00000000-0000-0000-0000-000000000001",
		Username:            "dev-operator",
		Email:               "operator@sentinel.local",
		Name:                "Dev Operator",
		Roles:               append([]string(nil), roles...),
		RealmRoles:          append([]string(nil), roles...),
		ClientRoles:         []string{},
		ClassificationLevel: "TOP_SECRET",
	}
}
