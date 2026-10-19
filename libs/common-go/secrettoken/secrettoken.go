// Package secrettoken makes and checks the bearer secrets Sentinel issues:
// API keys ("sk_") and ingest tokens ("si_"). A secret is its prefix and 32
// random bytes in unpadded base64url (43 characters). It is shown once; only
// its SHA-256 is stored, and lists show its prefix plus eight characters.
package secrettoken

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strings"
)

// bodyLen is the length of 32 bytes in unpadded base64url.
const bodyLen = 43

// displayLen is how much of the secret after its prefix a list shows.
const displayLen = 8

// Generate makes a new secret with the given prefix, its display form and
// its storage hash.
func Generate(prefix string) (secret, display string, hash []byte, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", nil, err
	}
	secret = prefix + base64.RawURLEncoding.EncodeToString(raw)
	return secret, Display(secret, prefix), Hash(secret), nil
}

// Hash is the storage form of a secret.
func Hash(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// Display is the part of a secret shown in lists so an owner can tell
// secrets apart: the prefix plus eight characters.
func Display(secret, prefix string) string {
	n := len(prefix) + displayLen
	if len(secret) <= n {
		return secret
	}
	return secret[:n]
}

// Valid reports whether a presented secret has the expected shape.
func Valid(secret, prefix string) bool {
	return strings.HasPrefix(secret, prefix) && len(secret) == len(prefix)+bodyLen
}

// Equal compares two hashes in constant time.
func Equal(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }
