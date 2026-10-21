// Package ingesttoken holds the bearer secrets senders present to Sentinel's
// gRPC connections: one connection each, shown once, stored as a SHA-256,
// revocable, with a classification ceiling no accepted report may exceed.
package ingesttoken

import (
	"errors"
	"time"
)

const (
	// Prefix starts every ingest token.
	Prefix = "si_"
	// Channel is the NOTIFY channel every create and revoke announces
	// itself on, with the payload {"id", "feedId", "op"}.
	Channel = "sentinel_ingest_tokens"
)

// ErrNotFound is returned for an unknown token, or one of another feed.
var ErrNotFound = errors.New("ingest token not found")

// Token is a stored token as its connection's operators see it: never the
// secret, only its display prefix.
type Token struct {
	ID             string     `json:"id"`
	FeedID         string     `json:"feedId"`
	Name           string     `json:"name"`
	Prefix         string     `json:"prefix"`
	Classification string     `json:"classification"`
	CreatedBy      string     `json:"createdBy"`
	CreatedAt      time.Time  `json:"createdAt"`
	LastUsedAt     *time.Time `json:"lastUsedAt"`
	ExpiresAt      *time.Time `json:"expiresAt"`
	RevokedAt      *time.Time `json:"revokedAt"`
}

// Usable reports whether the token may authenticate at now: not revoked and
// not past its expiry.
func (t Token) Usable(now time.Time) bool {
	return t.RevokedAt == nil && (t.ExpiresAt == nil || now.Before(*t.ExpiresAt))
}

// Stored is a token with its hash, as the cache holds it.
type Stored struct {
	Token
	Hash []byte `json:"-"`
}
