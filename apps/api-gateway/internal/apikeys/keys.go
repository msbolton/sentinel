// Package apikeys issues and checks per-user, read-only API keys for GIS
// tools and KML network links that cannot carry a bearer token. A key is
// shown once; only its SHA-256 is stored. Roles and classification are
// fixed when the key is made (viewer, capped at the issuer's clearance).
package apikeys

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/sentinel/api-gateway/internal/auth"
)

// ErrInvalidKey is returned for an unknown, revoked or expired key.
var ErrInvalidKey = errors.New("invalid api key")

// Key is a stored key as shown to its owner.
type Key struct {
	ID             string     `json:"id"`
	UserID         string     `json:"userId"`
	Username       string     `json:"username"`
	Name           string     `json:"name"`
	Prefix         string     `json:"prefix"`
	Roles          []string   `json:"roles"`
	Classification string     `json:"classification"`
	CreatedAt      time.Time  `json:"createdAt"`
	ExpiresAt      *time.Time `json:"expiresAt,omitempty"`
	LastUsedAt     *time.Time `json:"lastUsedAt,omitempty"`
}

// Created is a freshly issued key with its one-time secret.
type Created struct {
	Key
	Secret string `json:"secret"`
}

// Generate makes a new secret ("sk_" + 32 random bytes, base64url) with its
// display prefix and storage hash.
func Generate() (secret, prefix string, hash []byte, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", nil, err
	}
	secret = auth.APIKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return secret, Prefix(secret), Hash(secret), nil
}

// Hash is the storage form of a secret.
func Hash(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// Prefix is the part of a secret shown in lists so an owner can tell keys
// apart: "sk_" plus eight characters.
func Prefix(secret string) string {
	if len(secret) <= 11 {
		return secret
	}
	return secret[:11]
}

// Valid reports whether a presented secret has the expected shape.
func Valid(secret string) bool {
	return strings.HasPrefix(secret, auth.APIKeyPrefix) && len(secret) == len(auth.APIKeyPrefix)+43
}

// Principal is the user a key acts as: the owner's identity, the viewer
// role only, the classification fixed at issue time.
func (k Key) Principal() *auth.User {
	roles := append([]string(nil), k.Roles...)
	if len(roles) == 0 {
		roles = []string{auth.RoleViewer}
	}
	return &auth.User{
		UserID: k.UserID, Username: k.Username, Roles: roles, RealmRoles: roles,
		ClassificationLevel: k.Classification, ViaAPIKey: true,
	}
}

// Usable reports whether the key may still authenticate at now.
func (k Key) Usable(now time.Time, revokedAt *time.Time) bool {
	if revokedAt != nil {
		return false
	}
	if k.ExpiresAt != nil && !now.Before(*k.ExpiresAt) {
		return false
	}
	return true
}

// hashEqual compares two hashes in constant time.
func hashEqual(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }
