package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// FusedUpdate is an accepted-candidate fix for one entity.
type FusedUpdate struct {
	EntityID         string
	ObservedAt       time.Time
	FeedID           string
	Source           string
	SourceEntityID   string
	KeyType          string
	KeyValue         string
	Name             string
	Lat, Lon         float64
	Altitude         *float64
	Heading          *float64
	SpeedKnots       *float64
	Course           *float64
	TrackEnvironment string
	CountryOfOrigin  string
	Affiliation      string
	CircularError    *float64
	Platform         json.RawMessage // {"adsb": {...}} merged into platformData
	Kinematics       json.RawMessage // {"velocity": {...}, "acceleration": {...}}
	Classification   string          // bare Classification name; raises the entity, never lowers it
	Exercise         bool            // marks the entity exercise; never clears the mark
}

// StaticUpdate is a report without a fix: attributes only.
type StaticUpdate struct {
	EntityID        string
	FeedID          string
	Source          string
	SourceEntityID  string
	Name            string
	CountryOfOrigin string
	Platform        json.RawMessage
	Classification  string
	Exercise        bool
}

// FusedResult is an applied update with the previous liveness state and
// classification, so the processor can announce what changed.
type FusedResult struct {
	EntityID               string
	PreviousState          string
	PreviousClassification string
	Record                 *EntityRecord
}

// ApplyFused applies candidate fixes, each only when it is at least as new
// as the entity's last observed time, and returns the rows it changed.
// Rows not returned lost to a newer fix (a stale delivery or a slower feed).
// Feeds never write operator-owned fields; the name follows the feed only
// for feed-created entities.
func (s *EntityStore) ApplyFused(ctx context.Context, updates []FusedUpdate) ([]FusedResult, error) {
	if len(updates) == 0 {
		return nil, nil
	}
	const params = 22
	values := make([]string, 0, len(updates))
	args := make([]interface{}, 0, len(updates)*params)
	ids := make([]string, 0, len(updates))
	for i, u := range updates {
		b := i * params
		values = append(values, fmt.Sprintf(
			"($%d::uuid, $%d::timestamptz, $%d::text, $%d::text, $%d::text, $%d::text, $%d::text, $%d::text, $%d::double precision, $%d::double precision, $%d::double precision, $%d::double precision, $%d::double precision, $%d::double precision, $%d::text, $%d::text, $%d::double precision, $%d::jsonb, $%d::jsonb, $%d::text, $%d::boolean, $%d::text)",
			b+1, b+2, b+3, b+4, b+5, b+6, b+7, b+8, b+9, b+10, b+11, b+12, b+13, b+14, b+15, b+16, b+17, b+18, b+19, b+20, b+21, b+22))
		var platform, kinematics interface{}
		if len(u.Platform) > 0 {
			platform = string(u.Platform)
		}
		if len(u.Kinematics) > 0 {
			kinematics = string(u.Kinematics)
		}
		args = append(args, u.EntityID, u.ObservedAt, u.FeedID, u.Source, u.SourceEntityID, u.KeyType, u.KeyValue, u.Name,
			u.Lon, u.Lat, u.Altitude, u.Heading, u.SpeedKnots, u.Course, u.TrackEnvironment, u.CountryOfOrigin, u.CircularError,
			platform, kinematics, u.Classification, u.Exercise, u.Affiliation)
		ids = append(ids, u.EntityID)
	}
	query := fmt.Sprintf(`
		WITH prev AS (
			SELECT id, "ageoutState", classification FROM sentinel.entities WHERE id = ANY($%d::uuid[])
		)
		UPDATE sentinel.entities AS e
		   SET position = ST_SetSRID(ST_MakePoint(b.lng, b.lat), 4326),
		       classification = sentinel.classification_max(e.classification, b.classification),
		       exercise = e.exercise OR b.exercise,
		       altitude = COALESCE(b.altitude, e.altitude),
		       heading = COALESCE(b.heading, e.heading),
		       "speedKnots" = COALESCE(b.speed_knots, e."speedKnots"),
		       course = COALESCE(b.course, e.course),
		       "trackEnvironment" = COALESCE(NULLIF(b.track_env, ''), e."trackEnvironment"),
		       "countryOfOrigin" = COALESCE(NULLIF(b.country, ''), e."countryOfOrigin"),
		       affiliation = COALESCE(NULLIF(b.affiliation, ''), e.affiliation),
		       "circularError" = CASE
		           WHEN b.circular_err IS NOT NULL THEN b.circular_err
		           WHEN e.kinematics ? 'positionCovariance' OR e."lastFeedId" = b.feed_id THEN NULL
		           ELSE e."circularError"
		       END,
		       "platformData" = COALESCE(e."platformData", '{}'::jsonb) || COALESCE(b.platform, '{}'::jsonb),
		       kinematics = COALESCE(b.kinematics, e.kinematics),
		       name = CASE WHEN e."feedId" = 'manual' OR b.name = '' THEN e.name ELSE b.name END,
		       "sourceEntityId" = b.source_entity_id,
		       "lastObservationSource" = b.source,
		       "lastFeedId" = b.feed_id,
		       "lastObservedAt" = b.observed_at,
		       "primaryIdentityType" = b.key_type,
		       "primaryIdentityValue" = b.key_value,
		       "lastSeenAt" = NOW(),
		       "ageoutState" = 'LIVE'
		  FROM (VALUES %s) AS b(id, observed_at, feed_id, source, source_entity_id, key_type, key_value, name, lng, lat, altitude, heading, speed_knots, course, track_env, country, circular_err, platform, kinematics, classification, exercise, affiliation)
		 WHERE e.id = b.id AND e.deleted = false
		   AND (e."lastObservedAt" IS NULL OR b.observed_at >= e."lastObservedAt")
		RETURNING %s, (SELECT p."ageoutState" FROM prev p WHERE p.id = e.id), (SELECT p.classification FROM prev p WHERE p.id = e.id)`,
		len(args)+1, strings.Join(values, ","), prefixed(entitySelectColumns, "e."))
	args = append(args, ids)
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("apply fused updates: %w", err)
	}
	defer rows.Close()
	return scanFused(rows)
}

// ApplyStatic merges attribute-only reports (name, platform detail,
// country) and marks the entity seen without moving it.
func (s *EntityStore) ApplyStatic(ctx context.Context, updates []StaticUpdate) ([]FusedResult, error) {
	if len(updates) == 0 {
		return nil, nil
	}
	const params = 9
	values := make([]string, 0, len(updates))
	args := make([]interface{}, 0, len(updates)*params)
	ids := make([]string, 0, len(updates))
	for i, u := range updates {
		b := i * params
		values = append(values, fmt.Sprintf("($%d::uuid, $%d::text, $%d::text, $%d::text, $%d::text, $%d::text, $%d::jsonb, $%d::text, $%d::boolean)", b+1, b+2, b+3, b+4, b+5, b+6, b+7, b+8, b+9))
		var platform interface{}
		if len(u.Platform) > 0 {
			platform = string(u.Platform)
		}
		args = append(args, u.EntityID, u.FeedID, u.Source, u.SourceEntityID, u.Name, u.CountryOfOrigin, platform, u.Classification, u.Exercise)
		ids = append(ids, u.EntityID)
	}
	query := fmt.Sprintf(`
		WITH prev AS (
			SELECT id, "ageoutState", classification FROM sentinel.entities WHERE id = ANY($%d::uuid[])
		)
		UPDATE sentinel.entities AS e
		   SET name = CASE WHEN e."feedId" = 'manual' OR b.name = '' THEN e.name ELSE b.name END,
		       classification = sentinel.classification_max(e.classification, b.classification),
		       exercise = e.exercise OR b.exercise,
		       "countryOfOrigin" = COALESCE(NULLIF(b.country, ''), e."countryOfOrigin"),
		       "platformData" = COALESCE(e."platformData", '{}'::jsonb) || COALESCE(b.platform, '{}'::jsonb),
		       "lastObservationSource" = b.source,
		       "lastFeedId" = b.feed_id,
		       "lastSeenAt" = NOW(),
		       "ageoutState" = 'LIVE'
		  FROM (VALUES %s) AS b(id, feed_id, source, source_entity_id, name, country, platform, classification, exercise)
		 WHERE e.id = b.id AND e.deleted = false
		RETURNING %s, (SELECT p."ageoutState" FROM prev p WHERE p.id = e.id), (SELECT p.classification FROM prev p WHERE p.id = e.id)`,
		len(args)+1, strings.Join(values, ","), prefixed(entitySelectColumns, "e."))
	args = append(args, ids)
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("apply static updates: %w", err)
	}
	defer rows.Close()
	return scanFused(rows)
}

// MarkExercise sets the exercise mark on entities that lack it and returns
// the rows it changed. It is for exercise reports that did not win the fix
// (a duplicate in their batch, or older than the entity's last fix), which
// ApplyFused never carries. The mark is never cleared.
func (s *EntityStore) MarkExercise(ctx context.Context, ids []string) ([]EntityRecord, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		UPDATE sentinel.entities
		   SET exercise = true, "updatedAt" = NOW()
		 WHERE id = ANY($1::uuid[]) AND NOT exercise AND deleted = false
		RETURNING %s`, entitySelectColumns), ids)
	if err != nil {
		return nil, fmt.Errorf("mark exercise: %w", err)
	}
	defer rows.Close()
	return scanEntities(rows)
}

func scanFused(rows pgx.Rows) ([]FusedResult, error) {
	var out []FusedResult
	for rows.Next() {
		var rec EntityRecord
		var lat, lng *float64
		var prev, prevClass *string
		err := rows.Scan(
			&rec.ID, &rec.EntityType, &rec.Name, &rec.Description, &rec.Source, &rec.Classification, &rec.FeedID,
			&lat, &lng,
			&rec.Heading, &rec.SpeedKnots, &rec.Course, &rec.Altitude,
			&rec.MilStd2525dSymbol, &rec.Metadata, &rec.Affiliations,
			&rec.Affiliation, &rec.IdentityConfidence, &rec.Characterization, &rec.TrackEnvironment,
			&rec.TrackProcessingState, &rec.OperationalStatus,
			&rec.DamageAssessment, &rec.DamageConfidence,
			&rec.DimensionLength, &rec.DimensionWidth, &rec.DimensionHeight,
			&rec.CountryOfOrigin, &rec.CircularError,
			&rec.Kinematics, &rec.PlatformData, &rec.LastObservationSource, &rec.SourceEntityID,
			&rec.AgeoutState, &rec.CreatedAt, &rec.UpdatedAt, &rec.LastSeenAt, &rec.Deleted,
			&rec.LastObservedAt, &rec.LastFeedID, &rec.PrimaryIdentityType, &rec.PrimaryIdentityValue, &rec.Exercise,
			&prev, &prevClass,
		)
		if err != nil {
			return nil, fmt.Errorf("scan fused row: %w", err)
		}
		if lat != nil && lng != nil {
			rec.Position = &GeoPoint{Lat: *lat, Lon: *lng}
		}
		r := FusedResult{EntityID: rec.ID, Record: &rec}
		if prev != nil {
			r.PreviousState = *prev
		}
		if prevClass != nil {
			r.PreviousClassification = *prevClass
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// prefixed qualifies every bare column in a select list with alias.
func prefixed(columns, alias string) string {
	var out []string
	for _, col := range strings.Split(columns, ",") {
		col = strings.TrimSpace(col)
		if col == "" {
			continue
		}
		if strings.HasPrefix(col, "ST_") {
			col = strings.Replace(col, "(position", "("+alias+"position", 1)
			out = append(out, col)
			continue
		}
		out = append(out, alias+col)
	}
	return strings.Join(out, ", ")
}
