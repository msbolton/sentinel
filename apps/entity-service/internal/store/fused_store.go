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
	CircularError    *float64
	Platform         json.RawMessage // {"adsb": {...}} merged into platformData
	Kinematics       json.RawMessage // {"velocity": {...}, "acceleration": {...}}
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
}

// FusedResult is an applied update with the previous liveness state.
type FusedResult struct {
	EntityID      string
	PreviousState string
	Record        *EntityRecord
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
	const params = 19
	values := make([]string, 0, len(updates))
	args := make([]interface{}, 0, len(updates)*params)
	ids := make([]string, 0, len(updates))
	for i, u := range updates {
		b := i * params
		values = append(values, fmt.Sprintf(
			"($%d::uuid, $%d::timestamptz, $%d::text, $%d::text, $%d::text, $%d::text, $%d::text, $%d::text, $%d::double precision, $%d::double precision, $%d::double precision, $%d::double precision, $%d::double precision, $%d::double precision, $%d::text, $%d::text, $%d::double precision, $%d::jsonb, $%d::jsonb)",
			b+1, b+2, b+3, b+4, b+5, b+6, b+7, b+8, b+9, b+10, b+11, b+12, b+13, b+14, b+15, b+16, b+17, b+18, b+19))
		var platform, kinematics interface{}
		if len(u.Platform) > 0 {
			platform = string(u.Platform)
		}
		if len(u.Kinematics) > 0 {
			kinematics = string(u.Kinematics)
		}
		args = append(args, u.EntityID, u.ObservedAt, u.FeedID, u.Source, u.SourceEntityID, u.KeyType, u.KeyValue, u.Name,
			u.Lon, u.Lat, u.Altitude, u.Heading, u.SpeedKnots, u.Course, u.TrackEnvironment, u.CountryOfOrigin, u.CircularError,
			platform, kinematics)
		ids = append(ids, u.EntityID)
	}
	query := fmt.Sprintf(`
		WITH prev AS (
			SELECT id, "ageoutState" FROM sentinel.entities WHERE id = ANY($%d::uuid[])
		)
		UPDATE sentinel.entities AS e
		   SET position = ST_SetSRID(ST_MakePoint(b.lng, b.lat), 4326),
		       altitude = COALESCE(b.altitude, e.altitude),
		       heading = COALESCE(b.heading, e.heading),
		       "speedKnots" = COALESCE(b.speed_knots, e."speedKnots"),
		       course = COALESCE(b.course, e.course),
		       "trackEnvironment" = COALESCE(NULLIF(b.track_env, ''), e."trackEnvironment"),
		       "countryOfOrigin" = COALESCE(NULLIF(b.country, ''), e."countryOfOrigin"),
		       "circularError" = COALESCE(b.circular_err, e."circularError"),
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
		  FROM (VALUES %s) AS b(id, observed_at, feed_id, source, source_entity_id, key_type, key_value, name, lng, lat, altitude, heading, speed_knots, course, track_env, country, circular_err, platform, kinematics)
		 WHERE e.id = b.id AND e.deleted = false
		   AND (e."lastObservedAt" IS NULL OR b.observed_at >= e."lastObservedAt")
		RETURNING %s, (SELECT p."ageoutState" FROM prev p WHERE p.id = e.id)`,
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
	const params = 7
	values := make([]string, 0, len(updates))
	args := make([]interface{}, 0, len(updates)*params)
	ids := make([]string, 0, len(updates))
	for i, u := range updates {
		b := i * params
		values = append(values, fmt.Sprintf("($%d::uuid, $%d::text, $%d::text, $%d::text, $%d::text, $%d::text, $%d::jsonb)", b+1, b+2, b+3, b+4, b+5, b+6, b+7))
		var platform interface{}
		if len(u.Platform) > 0 {
			platform = string(u.Platform)
		}
		args = append(args, u.EntityID, u.FeedID, u.Source, u.SourceEntityID, u.Name, u.CountryOfOrigin, platform)
		ids = append(ids, u.EntityID)
	}
	query := fmt.Sprintf(`
		WITH prev AS (
			SELECT id, "ageoutState" FROM sentinel.entities WHERE id = ANY($%d::uuid[])
		)
		UPDATE sentinel.entities AS e
		   SET name = CASE WHEN e."feedId" = 'manual' OR b.name = '' THEN e.name ELSE b.name END,
		       "countryOfOrigin" = COALESCE(NULLIF(b.country, ''), e."countryOfOrigin"),
		       "platformData" = COALESCE(e."platformData", '{}'::jsonb) || COALESCE(b.platform, '{}'::jsonb),
		       "lastObservationSource" = b.source,
		       "lastFeedId" = b.feed_id,
		       "lastSeenAt" = NOW(),
		       "ageoutState" = 'LIVE'
		  FROM (VALUES %s) AS b(id, feed_id, source, source_entity_id, name, country, platform)
		 WHERE e.id = b.id AND e.deleted = false
		RETURNING %s, (SELECT p."ageoutState" FROM prev p WHERE p.id = e.id)`,
		len(args)+1, strings.Join(values, ","), prefixed(entitySelectColumns, "e."))
	args = append(args, ids)
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("apply static updates: %w", err)
	}
	defer rows.Close()
	return scanFused(rows)
}

func scanFused(rows pgx.Rows) ([]FusedResult, error) {
	var out []FusedResult
	for rows.Next() {
		var rec EntityRecord
		var lat, lng *float64
		var prev *string
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
			&rec.LastObservedAt, &rec.LastFeedID, &rec.PrimaryIdentityType, &rec.PrimaryIdentityValue,
			&prev,
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
