package ingesttoken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store reads and writes ingest tokens.
type Store interface {
	Create(ctx context.Context, t Token, hash []byte) (Token, error)
	List(ctx context.Context, feedID string) ([]Token, error)
	// Revoke marks a token of feedID revoked at at; revoking again keeps the
	// first time. ErrNotFound for an unknown token or one of another feed.
	Revoke(ctx context.Context, feedID, id string, at time.Time) error
	All(ctx context.Context) ([]Stored, error)
	ForFeed(ctx context.Context, feedID string) ([]Stored, error)
	// MarkUsed records last uses; an older time than the stored one is ignored.
	MarkUsed(ctx context.Context, used map[string]time.Time) error
}

// PGStore is the Store on sentinel.ingest_tokens.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore returns a store on pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const columns = `id::text, "feedId", name, prefix, classification, COALESCE("createdBy", ''), "createdAt", "lastUsedAt", "expiresAt", "revokedAt"`

type rowScanner interface{ Scan(dest ...any) error }

func scan(row rowScanner, hash *[]byte) (Token, error) {
	var t Token
	dest := []any{&t.ID, &t.FeedID, &t.Name, &t.Prefix, &t.Classification, &t.CreatedBy, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt, &t.RevokedAt}
	if hash != nil {
		dest = append(dest, hash)
	}
	return t, row.Scan(dest...)
}

func mapErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (pg.Code == "23503" || pg.Code == "22P02") { // unknown feed, malformed uuid
		return ErrNotFound
	}
	return err
}

func notify(ctx context.Context, tx pgx.Tx, id, feedID, op string) error {
	payload, _ := json.Marshal(map[string]string{"id": id, "feedId": feedID, "op": op})
	if _, err := tx.Exec(ctx, "SELECT pg_notify($1, $2)", Channel, string(payload)); err != nil {
		return fmt.Errorf("notifying %s: %w", Channel, err)
	}
	return nil
}

func (s *PGStore) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Create stores t with its hash; id and createdAt come from the database.
func (s *PGStore) Create(ctx context.Context, t Token, hash []byte) (Token, error) {
	var out Token
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scan(tx.QueryRow(ctx, `INSERT INTO sentinel.ingest_tokens
			("feedId", name, prefix, hash, classification, "createdBy", "expiresAt")
			VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7)
			RETURNING `+columns, t.FeedID, t.Name, t.Prefix, hash, t.Classification, t.CreatedBy, t.ExpiresAt), nil)
		if err != nil {
			return mapErr(err)
		}
		return notify(ctx, tx, out.ID, out.FeedID, "create")
	})
	return out, err
}

func (s *PGStore) query(ctx context.Context, withHash bool, where string, args ...any) ([]Stored, error) {
	cols := columns
	if withHash {
		cols += ", hash"
	}
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM sentinel.ingest_tokens `+where+` ORDER BY "createdAt" DESC, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Stored{}
	for rows.Next() {
		var st Stored
		var hp *[]byte
		if withHash {
			hp = &st.Hash
		}
		if st.Token, err = scan(rows, hp); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// List returns a feed's tokens, newest first, without their hashes.
func (s *PGStore) List(ctx context.Context, feedID string) ([]Token, error) {
	st, err := s.query(ctx, false, `WHERE "feedId" = $1`, feedID)
	if err != nil {
		return nil, err
	}
	out := make([]Token, len(st))
	for i := range st {
		out[i] = st[i].Token
	}
	return out, nil
}

// All returns every token with its hash, for the cache.
func (s *PGStore) All(ctx context.Context) ([]Stored, error) { return s.query(ctx, true, "") }

// ForFeed returns a feed's tokens with their hashes, for the cache.
func (s *PGStore) ForFeed(ctx context.Context, feedID string) ([]Stored, error) {
	return s.query(ctx, true, `WHERE "feedId" = $1`, feedID)
}

// Revoke marks a token revoked.
func (s *PGStore) Revoke(ctx context.Context, feedID, id string, at time.Time) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE sentinel.ingest_tokens SET "revokedAt" = COALESCE("revokedAt", $3)
			WHERE id = $1::uuid AND "feedId" = $2`, id, feedID, at)
		if err != nil {
			return mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return notify(ctx, tx, id, feedID, "revoke")
	})
}

// MarkUsed records last-use times in one round trip; it sends no notice.
func (s *PGStore) MarkUsed(ctx context.Context, used map[string]time.Time) error {
	if len(used) == 0 {
		return nil
	}
	b := &pgx.Batch{}
	for id, at := range used {
		b.Queue(`UPDATE sentinel.ingest_tokens SET "lastUsedAt" = GREATEST(COALESCE("lastUsedAt", $2), $2) WHERE id = $1::uuid`, id, at)
	}
	return s.pool.SendBatch(ctx, b).Close()
}
