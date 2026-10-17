package apikeys

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sentinel/api-gateway/internal/auth"
)

// Store persists keys in sentinel.api_keys.
type Store interface {
	Create(ctx context.Context, k Key, hash []byte) (Key, error)
	ListByUser(ctx context.Context, userID string) ([]Key, error)
	// Revoke marks a key revoked; when owner is "" any key matches (admin).
	Revoke(ctx context.Context, id, owner string) error
	// Lookup finds a live key by its secret and records the use.
	Lookup(ctx context.Context, secret string, now time.Time) (Key, error)
}

// PGStore is the Postgres store.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const columns = `id::text, "userId", username, name, prefix, roles, classification, "createdAt", "expiresAt", "lastUsedAt"`

func scan(row pgx.Row) (Key, error) {
	var k Key
	err := row.Scan(&k.ID, &k.UserID, &k.Username, &k.Name, &k.Prefix, &k.Roles, &k.Classification, &k.CreatedAt, &k.ExpiresAt, &k.LastUsedAt)
	return k, err
}

// Create inserts a key.
func (s *PGStore) Create(ctx context.Context, k Key, hash []byte) (Key, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO sentinel.api_keys ("userId", username, name, prefix, "keyHash", roles, classification, "expiresAt")
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+columns, k.UserID, k.Username, k.Name, k.Prefix, hash, k.Roles, k.Classification, k.ExpiresAt)
	out, err := scan(row)
	if err != nil {
		return Key{}, fmt.Errorf("create api key: %w", err)
	}
	return out, nil
}

// ListByUser lists a user's live keys, newest first.
func (s *PGStore) ListByUser(ctx context.Context, userID string) ([]Key, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+columns+` FROM sentinel.api_keys WHERE "userId" = $1 AND "revokedAt" IS NULL ORDER BY "createdAt" DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		k, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("list api keys: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Revoke marks a key revoked. ErrInvalidKey when nothing matched.
func (s *PGStore) Revoke(ctx context.Context, id, owner string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE sentinel.api_keys SET "revokedAt" = NOW() WHERE id::text = $1 AND "revokedAt" IS NULL AND ($2 = '' OR "userId" = $2)`, id, owner)
	if err != nil {
		return fmt.Errorf("revoke api key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrInvalidKey
	}
	return nil
}

// Lookup resolves a secret to its key and stamps lastUsedAt.
func (s *PGStore) Lookup(ctx context.Context, secret string, now time.Time) (Key, error) {
	if !Valid(secret) {
		return Key{}, ErrInvalidKey
	}
	row := s.pool.QueryRow(ctx, `SELECT `+columns+`, "revokedAt", "keyHash" FROM sentinel.api_keys WHERE "keyHash" = $1`, Hash(secret))
	var k Key
	var revokedAt *time.Time
	var stored []byte
	if err := row.Scan(&k.ID, &k.UserID, &k.Username, &k.Name, &k.Prefix, &k.Roles, &k.Classification, &k.CreatedAt, &k.ExpiresAt, &k.LastUsedAt, &revokedAt, &stored); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Key{}, ErrInvalidKey
		}
		return Key{}, fmt.Errorf("lookup api key: %w", err)
	}
	if !hashEqual(stored, Hash(secret)) || !k.Usable(now, revokedAt) {
		return Key{}, ErrInvalidKey
	}
	_, _ = s.pool.Exec(ctx, `UPDATE sentinel.api_keys SET "lastUsedAt" = $2 WHERE id::text = $1`, k.ID, now)
	return k, nil
}

// Resolver adapts a Store to auth.KeyResolver.
type Resolver struct {
	store Store
	now   func() time.Time
}

// NewResolver builds the resolver the auth middleware calls.
func NewResolver(store Store) *Resolver { return &Resolver{store: store, now: time.Now} }

// ResolveAPIKey implements auth.KeyResolver.
func (r *Resolver) ResolveAPIKey(ctx context.Context, secret string) (*auth.User, error) {
	k, err := r.store.Lookup(ctx, secret, r.now())
	if err != nil {
		return nil, err
	}
	return k.Principal(), nil
}
