package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// EntityRecord represents an entity row from the database.
type EntityRecord struct {
	ID                    string                 `json:"id"`
	EntityType            string                 `json:"entityType"`
	Name                  string                 `json:"name"`
	Description           *string                `json:"description,omitempty"`
	Source                string                 `json:"source"`
	Classification        string                 `json:"classification"`
	FeedID                *string                `json:"feedId,omitempty"`
	Position              *GeoPoint              `json:"position,omitempty"`
	Heading               *float64               `json:"heading,omitempty"`
	SpeedKnots            *float64               `json:"speedKnots,omitempty"`
	Course                *float64               `json:"course,omitempty"`
	Altitude              *float64               `json:"altitude,omitempty"`
	MilStd2525dSymbol     *string                `json:"milStd2525dSymbol,omitempty"`
	Metadata              map[string]interface{} `json:"metadata"`
	Affiliations          []string               `json:"affiliations"`
	Affiliation           *string                `json:"affiliation,omitempty"`
	IdentityConfidence    *int                   `json:"identityConfidence,omitempty"`
	Characterization      *string                `json:"characterization,omitempty"`
	TrackEnvironment      *string                `json:"trackEnvironment,omitempty"`
	TrackProcessingState  *string                `json:"trackProcessingState,omitempty"`
	OperationalStatus     *string                `json:"operationalStatus,omitempty"`
	DamageAssessment      *string                `json:"damageAssessment,omitempty"`
	DamageConfidence      *int                   `json:"damageConfidence,omitempty"`
	DimensionLength       *float64               `json:"dimensionLength,omitempty"`
	DimensionWidth        *float64               `json:"dimensionWidth,omitempty"`
	DimensionHeight       *float64               `json:"dimensionHeight,omitempty"`
	CountryOfOrigin       *string                `json:"countryOfOrigin,omitempty"`
	CircularError         *float64               `json:"circularError,omitempty"`
	Kinematics            map[string]interface{} `json:"kinematics,omitempty"`
	PlatformData          map[string]interface{} `json:"platformData,omitempty"`
	LastObservationSource *string                `json:"lastObservationSource,omitempty"`
	SourceEntityID        *string                `json:"sourceEntityId,omitempty"`
	AgeoutState           string                 `json:"ageoutState"`
	CreatedAt             string                 `json:"createdAt"`
	UpdatedAt             string                 `json:"updatedAt"`
	LastSeenAt            *string                `json:"lastSeenAt,omitempty"`
	Deleted               bool                   `json:"deleted,omitempty"`
}

// GeoPoint represents a geographic point.
type GeoPoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// EntityWithDistance is an entity with computed distance.
type EntityWithDistance struct {
	EntityRecord
	Distance float64 `json:"distance"`
}

// EntityCount holds grouped entity counts.
type EntityCount struct {
	EntityType     string `json:"entityType"`
	Classification string `json:"classification"`
	Count          int    `json:"count"`
}

// BulkPositionUpdate holds data for a single position update in a bulk operation.
type BulkPositionUpdate struct {
	ID               string
	Lng              float64
	Lat              float64
	Heading          *float64
	SpeedKnots       *float64
	Course           *float64
	Altitude         *float64
	PlatformData     map[string]interface{}
	Kinematics       map[string]interface{}
	TrackEnvironment *string
	CircularError    *float64
}

// ExistingEntityInfo holds minimal info for upsert lookups.
type ExistingEntityInfo struct {
	ID             string
	Name           string
	EntityType     string
	Classification string
	Source         string
	Metadata       map[string]interface{}
	SourceEntityID *string
	AgeoutState    string
}

// QueryParams holds parameters for querying entities.
type QueryParams struct {
	North          *float64
	South          *float64
	East           *float64
	West           *float64
	Types          []string
	Sources        []string
	Classification *string
	Page           int
	PageSize       int
}

// QueryResult holds paginated query results.
type QueryResult struct {
	Data     []EntityRecord `json:"data"`
	Total    int            `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"pageSize"`
}

// EntityStore handles database operations for entities.
type EntityStore struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

// NewEntityStore creates a new entity store.
func NewEntityStore(pool *pgxpool.Pool, logger *zap.Logger) *EntityStore {
	return &EntityStore{pool: pool, logger: logger}
}

// Pool returns the underlying connection pool.
func (s *EntityStore) Pool() *pgxpool.Pool {
	return s.pool
}

// entitySelectColumns are the columns selected in entity queries.
const entitySelectColumns = `
	id, "entityType", name, description, source, classification, "feedId",
	ST_Y(position::geometry) as lat, ST_X(position::geometry) as lng,
	heading, "speedKnots", course, altitude,
	"milStd2525dSymbol", metadata, affiliations,
	affiliation, "identityConfidence", characterization, "trackEnvironment",
	"trackProcessingState", "operationalStatus",
	"damageAssessment", "damageConfidence",
	"dimensionLength", "dimensionWidth", "dimensionHeight",
	"countryOfOrigin", "circularError",
	kinematics, "platformData", "lastObservationSource", "sourceEntityId",
	"ageoutState", "createdAt"::text, "updatedAt"::text, "lastSeenAt"::text, deleted
`

// FindByID returns an entity by ID.
func (s *EntityStore) FindByID(ctx context.Context, id string) (*EntityRecord, error) {
	row := s.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s FROM sentinel.entities WHERE id = $1 AND deleted = false
	`, entitySelectColumns), id)
	return scanEntity(row)
}

// FindWithinBoundingBox returns entities within a bounding box.
func (s *EntityStore) FindWithinBoundingBox(ctx context.Context, p QueryParams) (*QueryResult, error) {
	where := []string{"e.deleted = false", `e."ageoutState" != 'AGED_OUT'`}
	args := []interface{}{}
	argIdx := 1

	hasBBox := p.North != nil && p.South != nil && p.East != nil && p.West != nil
	if hasBBox {
		where = append(where, "e.position IS NOT NULL",
			fmt.Sprintf(`ST_Within(e.position, ST_MakeEnvelope($%d, $%d, $%d, $%d, 4326))`, argIdx, argIdx+1, argIdx+2, argIdx+3))
		args = append(args, *p.West, *p.South, *p.East, *p.North)
		argIdx += 4
	}

	if len(p.Types) > 0 {
		placeholders := make([]string, len(p.Types))
		for i, t := range p.Types {
			placeholders[i] = fmt.Sprintf("$%d", argIdx)
			args = append(args, t)
			argIdx++
		}
		where = append(where, fmt.Sprintf(`e."entityType" IN (%s)`, strings.Join(placeholders, ",")))
	}

	if len(p.Sources) > 0 {
		placeholders := make([]string, len(p.Sources))
		for i, src := range p.Sources {
			placeholders[i] = fmt.Sprintf("$%d", argIdx)
			args = append(args, src)
			argIdx++
		}
		where = append(where, fmt.Sprintf(`e.source IN (%s)`, strings.Join(placeholders, ",")))
	}

	if p.Classification != nil {
		classOrder := map[string]int{"UNCLASSIFIED": 0, "CONFIDENTIAL": 1, "SECRET": 2, "TOP_SECRET": 3}
		ceiling, ok := classOrder[*p.Classification]
		if ok {
			allowed := []string{}
			for c, rank := range classOrder {
				if rank <= ceiling {
					allowed = append(allowed, fmt.Sprintf("$%d", argIdx))
					args = append(args, c)
					argIdx++
				}
			}
			where = append(where, fmt.Sprintf(`e.classification IN (%s)`, strings.Join(allowed, ",")))
		}
	}

	whereClause := strings.Join(where, " AND ")

	// Count.
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM sentinel.entities e WHERE %s`, whereClause)
	var total int
	if err := s.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count entities: %w", err)
	}

	page := p.Page
	if page < 1 {
		page = 1
	}
	pageSize := p.PageSize
	if pageSize < 1 {
		pageSize = 100
	}
	if pageSize > 1000 {
		pageSize = 1000
	}

	offset := (page - 1) * pageSize
	dataQuery := fmt.Sprintf(`
		SELECT %s
		FROM sentinel.entities e
		WHERE %s
		ORDER BY e."lastSeenAt" DESC NULLS LAST
		LIMIT $%d OFFSET $%d
	`, entitySelectColumns, whereClause, argIdx, argIdx+1)
	args = append(args, pageSize, offset)

	rows, err := s.pool.Query(ctx, dataQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query entities: %w", err)
	}
	defer rows.Close()

	entities, err := scanEntities(rows)
	if err != nil {
		return nil, err
	}

	return &QueryResult{Data: entities, Total: total, Page: page, PageSize: pageSize}, nil
}

// FindNearby returns entities near a point, ordered by distance.
func (s *EntityStore) FindNearby(ctx context.Context, lat, lng, radiusMeters float64) ([]EntityWithDistance, error) {
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT %s,
			ST_Distance(
				e.position::geography,
				ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography
			) as distance
		FROM sentinel.entities e
		WHERE e.deleted = false
			AND e."ageoutState" != 'AGED_OUT'
			AND e.position IS NOT NULL
			AND ST_DWithin(
				e.position::geography,
				ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography,
				$3
			)
		ORDER BY distance ASC
	`, entitySelectColumns), lng, lat, radiusMeters)
	if err != nil {
		return nil, fmt.Errorf("find nearby: %w", err)
	}
	defer rows.Close()

	var results []EntityWithDistance
	for rows.Next() {
		var rec EntityRecord
		var lat, lng *float64
		var distance float64
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
			&distance,
		)
		if err != nil {
			return nil, fmt.Errorf("scan nearby entity: %w", err)
		}
		if lat != nil && lng != nil {
			rec.Position = &GeoPoint{Lat: *lat, Lon: *lng}
		}
		results = append(results, EntityWithDistance{EntityRecord: rec, Distance: distance})
	}
	return results, nil
}

// BulkUpdatePositions performs a VALUES-based bulk UPDATE for entity positions.
func (s *EntityStore) BulkUpdatePositions(ctx context.Context, updates []BulkPositionUpdate) error {
	if len(updates) == 0 {
		return nil
	}

	const paramsPerRow = 11
	values := make([]string, 0, len(updates))
	args := make([]interface{}, 0, len(updates)*paramsPerRow)

	for i, u := range updates {
		base := i * paramsPerRow
		values = append(values, fmt.Sprintf(
			"($%d::uuid, $%d::double precision, $%d::double precision, $%d::double precision, $%d::double precision, $%d::double precision, $%d::double precision, $%d::jsonb, $%d::jsonb, $%d::varchar, $%d::double precision)",
			base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9, base+10, base+11,
		))

		platformJSON, _ := json.Marshal(u.PlatformData)
		kinematicsJSON, _ := json.Marshal(u.Kinematics)

		var platformArg, kinematicsArg interface{}
		if u.PlatformData != nil {
			platformArg = string(platformJSON)
		}
		if u.Kinematics != nil {
			kinematicsArg = string(kinematicsJSON)
		}

		args = append(args,
			u.ID, u.Lng, u.Lat, u.Heading, u.SpeedKnots, u.Course, u.Altitude,
			platformArg, kinematicsArg, u.TrackEnvironment, u.CircularError,
		)
	}

	query := fmt.Sprintf(`
		UPDATE sentinel.entities AS e
		SET position = ST_SetSRID(ST_MakePoint(b.lng, b.lat), 4326),
			heading = b.heading,
			"speedKnots" = b.speed_knots,
			course = b.course,
			altitude = b.altitude,
			"platformData" = COALESCE(b.platform_data, e."platformData"),
			kinematics = COALESCE(b.kinematics, e.kinematics),
			"trackEnvironment" = COALESCE(b.track_env, e."trackEnvironment"),
			"circularError" = COALESCE(b.circular_err, e."circularError"),
			"lastSeenAt" = NOW(),
			"ageoutState" = 'LIVE'
		FROM (VALUES %s) AS b(id, lng, lat, heading, speed_knots, course, altitude, platform_data, kinematics, track_env, circular_err)
		WHERE e.id = b.id AND e.deleted = false
	`, strings.Join(values, ","))

	_, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("bulk update positions: %w", err)
	}
	return nil
}

// FindBySourceEntityIDs returns a map of sourceEntityId -> ExistingEntityInfo.
func (s *EntityStore) FindBySourceEntityIDs(ctx context.Context, ids []string) (map[string]*ExistingEntityInfo, error) {
	if len(ids) == 0 {
		return map[string]*ExistingEntityInfo{}, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT id, name, "entityType", classification, source, metadata, "sourceEntityId", "ageoutState"
		FROM sentinel.entities
		WHERE deleted = false AND "sourceEntityId" IN (%s)
	`, strings.Join(placeholders, ","))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("find by source entity ids: %w", err)
	}
	defer rows.Close()

	result := map[string]*ExistingEntityInfo{}
	for rows.Next() {
		var info ExistingEntityInfo
		if err := rows.Scan(&info.ID, &info.Name, &info.EntityType, &info.Classification, &info.Source, &info.Metadata, &info.SourceEntityID, &info.AgeoutState); err != nil {
			return nil, fmt.Errorf("scan existing entity: %w", err)
		}
		if info.SourceEntityID != nil {
			result[*info.SourceEntityID] = &info
		}
	}
	return result, nil
}

// Create inserts a new entity.
func (s *EntityStore) Create(ctx context.Context, rec *EntityRecord) (*EntityRecord, error) {
	metadata, _ := json.Marshal(rec.Metadata)
	platformData, _ := json.Marshal(rec.PlatformData)
	kinematics, _ := json.Marshal(rec.Kinematics)

	var posExpr string
	args := []interface{}{
		rec.EntityType, rec.Name, rec.Description, rec.Source, rec.Classification, rec.FeedID,
		rec.Heading, rec.SpeedKnots, rec.Course, rec.Altitude,
		rec.MilStd2525dSymbol, string(metadata), rec.Affiliations,
		rec.Affiliation, rec.IdentityConfidence, rec.Characterization, rec.TrackEnvironment,
		rec.TrackProcessingState, rec.OperationalStatus,
		rec.DamageAssessment, rec.DamageConfidence,
		rec.DimensionLength, rec.DimensionWidth, rec.DimensionHeight,
		rec.CountryOfOrigin, rec.CircularError,
		string(kinematics), string(platformData), rec.LastObservationSource, rec.SourceEntityID,
	}

	if rec.Position != nil {
		posExpr = fmt.Sprintf("ST_SetSRID(ST_MakePoint($%d, $%d), 4326)", len(args)+1, len(args)+2)
		args = append(args, rec.Position.Lon, rec.Position.Lat)
	} else {
		posExpr = "NULL"
	}

	query := fmt.Sprintf(`
		INSERT INTO sentinel.entities
			("entityType", name, description, source, classification, "feedId",
			 heading, "speedKnots", course, altitude,
			 "milStd2525dSymbol", metadata, affiliations,
			 affiliation, "identityConfidence", characterization, "trackEnvironment",
			 "trackProcessingState", "operationalStatus",
			 "damageAssessment", "damageConfidence",
			 "dimensionLength", "dimensionWidth", "dimensionHeight",
			 "countryOfOrigin", "circularError",
			 kinematics, "platformData", "lastObservationSource", "sourceEntityId",
			 position, "lastSeenAt")
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,
			%s, CASE WHEN %s IS NOT NULL THEN NOW() ELSE NULL END)
		RETURNING %s
	`, posExpr, posExpr, entitySelectColumns)

	row := s.pool.QueryRow(ctx, query, args...)
	return scanEntity(row)
}

// SoftDelete marks an entity as deleted.
func (s *EntityStore) SoftDelete(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sentinel.entities
		SET deleted = true, "deletedAt" = NOW()
		WHERE id = $1 AND deleted = false
	`, id)
	if err != nil {
		return fmt.Errorf("soft delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("entity not found: %s", id)
	}
	return nil
}

// SoftDeleteAll marks all active entities as deleted.
func (s *EntityStore) SoftDeleteAll(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sentinel.entities
		SET deleted = true, "deletedAt" = NOW(), "ageoutState" = 'AGED_OUT'
		WHERE deleted = false
	`)
	if err != nil {
		return 0, fmt.Errorf("soft delete all: %w", err)
	}
	return tag.RowsAffected(), nil
}

// GetEntityCounts returns entity counts grouped by type and classification.
func (s *EntityStore) GetEntityCounts(ctx context.Context) ([]EntityCount, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT "entityType", classification, COUNT(*)
		FROM sentinel.entities
		WHERE deleted = false
		GROUP BY "entityType", classification
	`)
	if err != nil {
		return nil, fmt.Errorf("get entity counts: %w", err)
	}
	defer rows.Close()

	var counts []EntityCount
	for rows.Next() {
		var c EntityCount
		if err := rows.Scan(&c.EntityType, &c.Classification, &c.Count); err != nil {
			return nil, fmt.Errorf("scan count: %w", err)
		}
		counts = append(counts, c)
	}
	return counts, nil
}

// UpdatePosition updates position and kinematic fields for a single entity.
func (s *EntityStore) UpdatePosition(ctx context.Context, id string, lat, lng float64, heading, speedKnots, course, altitude *float64) (*EntityRecord, error) {
	row := s.pool.QueryRow(ctx, fmt.Sprintf(`
		UPDATE sentinel.entities
		SET position = ST_SetSRID(ST_MakePoint($2, $3), 4326),
			heading = COALESCE($4, heading),
			"speedKnots" = COALESCE($5, "speedKnots"),
			course = COALESCE($6, course),
			altitude = COALESCE($7, altitude),
			"lastSeenAt" = NOW(),
			"ageoutState" = 'LIVE'
		WHERE id = $1 AND deleted = false
		RETURNING %s
	`, entitySelectColumns), id, lng, lat, heading, speedKnots, course, altitude)
	return scanEntity(row)
}

// Ping checks database connectivity.
func (s *EntityStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func scanEntity(row pgx.Row) (*EntityRecord, error) {
	var rec EntityRecord
	var lat, lng *float64
	err := row.Scan(
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
	)
	if err != nil {
		return nil, fmt.Errorf("scan entity: %w", err)
	}
	if lat != nil && lng != nil {
		rec.Position = &GeoPoint{Lat: *lat, Lon: *lng}
	}
	return &rec, nil
}

func scanEntities(rows pgx.Rows) ([]EntityRecord, error) {
	var entities []EntityRecord
	for rows.Next() {
		var rec EntityRecord
		var lat, lng *float64
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
		)
		if err != nil {
			return nil, fmt.Errorf("scan entity row: %w", err)
		}
		if lat != nil && lng != nil {
			rec.Position = &GeoPoint{Lat: *lat, Lon: *lng}
		}
		entities = append(entities, rec)
	}
	return entities, nil
}
