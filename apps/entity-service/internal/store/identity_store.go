package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/sentinel/entity-service/internal/identity"
)

// IdentityStore binds natural identifiers to entities.
type IdentityStore struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

// NewIdentityStore creates an identity store.
func NewIdentityStore(pool *pgxpool.Pool, logger *zap.Logger) *IdentityStore {
	return &IdentityStore{pool: pool, logger: logger}
}

// Lookup returns the entity id for each key that is bound.
func (s *IdentityStore) Lookup(ctx context.Context, keys []identity.Key) (map[identity.Key]string, error) {
	out := map[identity.Key]string{}
	if len(keys) == 0 {
		return out, nil
	}
	types := make([]string, len(keys))
	values := make([]string, len(keys))
	for i, k := range keys {
		types[i], values[i] = k.Type, k.Value
	}
	rows, err := s.pool.Query(ctx, `
		SELECT i."identityType", i."identityValue", i."entityId"
		  FROM sentinel.entity_identities i
		  JOIN unnest($1::text[], $2::text[]) AS k(t, v) ON k.t = i."identityType" AND k.v = i."identityValue"
		  JOIN sentinel.entities e ON e.id = i."entityId" AND e.deleted = false`, types, values)
	if err != nil {
		return nil, fmt.Errorf("lookup identities: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k identity.Key
		var id string
		if err := rows.Scan(&k.Type, &k.Value, &id); err != nil {
			return nil, fmt.Errorf("scan identity: %w", err)
		}
		out[k] = id
	}
	return out, rows.Err()
}

// CreateEntities inserts one entity per unresolved key and binds the key to
// it. Each insert runs under a savepoint: when another writer bound the key
// first, the new row is rolled back and the existing entity id returned.
func (s *IdentityStore) CreateEntities(ctx context.Context, specs []identity.NewEntity) (map[identity.Key]string, map[identity.Key]bool, error) {
	ids, created := map[identity.Key]string{}, map[identity.Key]bool{}
	if len(specs) == 0 {
		return ids, created, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, spec := range specs {
		id, isNew, err := s.createOne(ctx, tx, spec)
		if err != nil {
			return nil, nil, err
		}
		ids[spec.Key] = id
		if isNew {
			created[spec.Key] = true
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("commit: %w", err)
	}
	return ids, created, nil
}

func (s *IdentityStore) createOne(ctx context.Context, tx pgx.Tx, spec identity.NewEntity) (string, bool, error) {
	if _, err := tx.Exec(ctx, "SAVEPOINT create_entity"); err != nil {
		return "", false, err
	}
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		name = spec.SourceEntityID
	}
	entityType := spec.EntityType
	if entityType == "" {
		entityType = "UNKNOWN"
	}
	source := spec.Source
	if source == "" {
		source = "UNSPECIFIED"
	}
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO sentinel.entities
			("entityType", name, source, classification, "feedId", "sourceEntityId", "trackEnvironment",
			 "countryOfOrigin", "primaryIdentityType", "primaryIdentityValue", metadata, affiliations, "ageoutState", exercise, affiliation)
		VALUES ($1, $2, $3, COALESCE(NULLIF(upper($10), ''), 'UNCLASSIFIED'), $4, $5, NULLIF($6, ''), NULLIF($7, ''), $8, $9, '{}'::jsonb, ARRAY[]::text[], 'LIVE', $11, COALESCE(NULLIF($12, ''), 'UNKNOWN'))
		RETURNING id`,
		entityType, name, source, spec.FeedID, spec.SourceEntityID,
		spec.TrackEnvironment, spec.CountryOfOrigin, spec.Key.Type, spec.Key.Value, spec.Classification, spec.Exercise, spec.Affiliation).Scan(&id)
	if err != nil {
		return "", false, fmt.Errorf("insert entity for %s: %w", spec.Key, err)
	}
	var bound string
	err = tx.QueryRow(ctx, `
		INSERT INTO sentinel.entity_identities ("identityType", "identityValue", "entityId", "firstSeenAt", "lastSeenAt")
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT ("identityType", "identityValue") DO NOTHING
		RETURNING "entityId"`, spec.Key.Type, spec.Key.Value, id, spec.SeenAt).Scan(&bound)
	if err == nil {
		if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT create_entity"); err != nil {
			return "", false, err
		}
		return bound, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, fmt.Errorf("bind identity %s: %w", spec.Key, err)
	}
	// Someone bound the key between our lookup and this insert: drop the
	// entity we just made and use theirs.
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT create_entity"); err != nil {
		return "", false, err
	}
	var existing string
	if err := tx.QueryRow(ctx, `SELECT "entityId" FROM sentinel.entity_identities WHERE "identityType" = $1 AND "identityValue" = $2`,
		spec.Key.Type, spec.Key.Value).Scan(&existing); err != nil {
		return "", false, fmt.Errorf("re-reading identity %s: %w", spec.Key, err)
	}
	return existing, false, nil
}

// AttachIdentities binds secondary identities. A key already bound to a
// different entity is left alone and counted as a conflict.
func (s *IdentityStore) AttachIdentities(ctx context.Context, bindings []identity.Binding) (int, error) {
	if len(bindings) == 0 {
		return 0, nil
	}
	seen := map[identity.Key]bool{}
	types, values, ids := []string{}, []string{}, []string{}
	var seenAt []time.Time
	for _, b := range bindings {
		if seen[b.Key] {
			continue
		}
		seen[b.Key] = true
		types = append(types, b.Key.Type)
		values = append(values, b.Key.Value)
		ids = append(ids, b.EntityID)
		seenAt = append(seenAt, b.SeenAt)
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO sentinel.entity_identities ("identityType", "identityValue", "entityId", "firstSeenAt", "lastSeenAt")
		SELECT t, v, e::uuid, at, at FROM unnest($1::text[], $2::text[], $3::text[], $4::timestamptz[]) AS b(t, v, e, at)
		ON CONFLICT ("identityType", "identityValue") DO UPDATE SET "lastSeenAt" = EXCLUDED."lastSeenAt"
		WHERE sentinel.entity_identities."entityId" = EXCLUDED."entityId"`, types, values, ids, seenAt)
	if err != nil {
		return 0, fmt.Errorf("attach identities: %w", err)
	}
	conflicts := len(types) - int(tag.RowsAffected())
	if conflicts > 0 {
		s.logger.Debug("identity conflicts ignored", zap.Int("count", conflicts))
	}
	return conflicts, nil
}

// IdentitiesOf lists the identities bound to an entity.
func (s *IdentityStore) IdentitiesOf(ctx context.Context, entityID string) ([]identity.Key, error) {
	rows, err := s.pool.Query(ctx, `SELECT "identityType", "identityValue" FROM sentinel.entity_identities WHERE "entityId" = $1 ORDER BY "firstSeenAt"`, entityID)
	if err != nil {
		return nil, fmt.Errorf("identities of %s: %w", entityID, err)
	}
	defer rows.Close()
	var out []identity.Key
	for rows.Next() {
		var k identity.Key
		if err := rows.Scan(&k.Type, &k.Value); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
