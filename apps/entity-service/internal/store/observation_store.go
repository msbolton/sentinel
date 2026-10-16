package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// ObservationRow is one report to persist.
type ObservationRow struct {
	ObservationID  string
	ObservedAt     time.Time
	ReceivedAt     time.Time
	EntityID       string
	FeedID         string
	Driver         string
	Decoder        string
	SourceEntityID string
	Source         string
	SensorID       string
	Accepted       bool
	RejectReason   string
	EntityType     string
	HasPosition    bool
	Lat, Lon       float64
	Altitude       *float64
	Heading        *float64
	SpeedKnots     *float64
	Course         *float64
	Velocity       *[3]float64
	Acceleration   *[3]float64
	CircularError  *float64
	AltitudeError  *float64
	PosCovariance  []float64
	PosVelCov      []float64
	VelCovariance  []float64
	Identities     json.RawMessage
	Platform       json.RawMessage
	RawContentType string
	Raw            []byte
	RawTruncated   bool
}

// ObservationRecord is one stored report as the API returns it.
type ObservationRecord struct {
	ObservationID  string          `json:"observationId"`
	ObservedAt     string          `json:"observedAt"`
	ReceivedAt     string          `json:"receivedAt"`
	EntityID       string          `json:"entityId"`
	FeedID         string          `json:"feedId"`
	Driver         string          `json:"driver"`
	Decoder        *string         `json:"decoder"`
	SourceEntityID string          `json:"sourceEntityId"`
	Source         string          `json:"source"`
	Accepted       bool            `json:"accepted"`
	RejectReason   *string         `json:"rejectReason"`
	Position       *GeoPoint       `json:"position"`
	Altitude       *float64        `json:"altitude"`
	Heading        *float64        `json:"heading"`
	SpeedKnots     *float64        `json:"speedKnots"`
	Course         *float64        `json:"course"`
	Identities     json.RawMessage `json:"identities"`
	Platform       json.RawMessage `json:"platform"`
	RawContentType *string         `json:"rawContentType"`
	RawTruncated   bool            `json:"rawTruncated"`
}

// ObservationStore writes and reads the observations hypertable.
type ObservationStore struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

// NewObservationStore creates an observation store.
func NewObservationStore(pool *pgxpool.Pool, logger *zap.Logger) *ObservationStore {
	return &ObservationStore{pool: pool, logger: logger}
}

const observationChunk = 1000

// InsertObservations stores rows in chunks; a row whose id already exists
// is skipped, so redelivery is harmless.
func (s *ObservationStore) InsertObservations(ctx context.Context, rows []ObservationRow) error {
	for start := 0; start < len(rows); start += observationChunk {
		end := start + observationChunk
		if end > len(rows) {
			end = len(rows)
		}
		if err := s.insertChunk(ctx, rows[start:end]); err != nil {
			return err
		}
	}
	return nil
}

// insertChunk sends one pgx batch per chunk: one round trip, one statement
// per row, readable SQL.
func (s *ObservationStore) insertChunk(ctx context.Context, rows []ObservationRow) error {
	const q = `
		INSERT INTO sentinel.observations
			("observationId", "observedAt", "receivedAt", "entityId", "feedId", driver, decoder, "sourceEntityId", source, "sensorId",
			 accepted, "rejectReason", "entityType", position, altitude, heading, "speedKnots", course,
			 "velocityNorth", "velocityEast", "velocityUp", "accelNorth", "accelEast", "accelUp", "circularError", "altitudeError",
			 identities, platform, "rawContentType", raw, "rawTruncated")
		VALUES ($1::uuid, $2, $3, $4::uuid, $5, $6, NULLIF($7, ''), $8, $9, NULLIF($10, ''),
			$11, NULLIF($12, ''), NULLIF($13, ''), CASE WHEN $14 THEN ST_SetSRID(ST_MakePoint($15, $16), 4326) END, $17, $18, $19, $20,
			$21, $22, $23, $24, $25, $26, $27, $28,
			$29::jsonb, $30::jsonb, NULLIF($31, ''), $32, $33)
		ON CONFLICT DO NOTHING`
	batch := &pgx.Batch{}
	for _, r := range rows {
		var vn, ve, vu, an, ae, au *float64
		if r.Velocity != nil {
			vn, ve, vu = &r.Velocity[0], &r.Velocity[1], &r.Velocity[2]
		}
		if r.Acceleration != nil {
			an, ae, au = &r.Acceleration[0], &r.Acceleration[1], &r.Acceleration[2]
		}
		identities := r.Identities
		if len(identities) == 0 {
			identities = json.RawMessage("[]")
		}
		var platform interface{}
		if len(r.Platform) > 0 {
			platform = string(r.Platform)
		}
		batch.Queue(q,
			r.ObservationID, r.ObservedAt, r.ReceivedAt, r.EntityID, r.FeedID, r.Driver, r.Decoder, r.SourceEntityID, r.Source, r.SensorID,
			r.Accepted, r.RejectReason, r.EntityType, r.HasPosition, r.Lon, r.Lat, r.Altitude, r.Heading, r.SpeedKnots, r.Course,
			vn, ve, vu, an, ae, au, r.CircularError, r.AltitudeError,
			string(identities), platform, r.RawContentType, r.Raw, r.RawTruncated)
	}
	results := s.pool.SendBatch(ctx, batch)
	defer results.Close()
	for range rows {
		if _, err := results.Exec(); err != nil {
			return fmt.Errorf("insert observation: %w", err)
		}
	}
	return nil
}

// ListByEntity returns an entity's most recent observations, newest first.
func (s *ObservationStore) ListByEntity(ctx context.Context, entityID string, limit int) ([]ObservationRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT "observationId"::text, "observedAt"::text, "receivedAt"::text, "entityId"::text, "feedId", driver, decoder, "sourceEntityId", source,
		       accepted, "rejectReason", ST_Y(position::geometry), ST_X(position::geometry), altitude, heading, "speedKnots", course,
		       identities, platform, "rawContentType", "rawTruncated"
		  FROM sentinel.observations
		 WHERE "entityId" = $1
		 ORDER BY "observedAt" DESC
		 LIMIT $2`, entityID, limit)
	if err != nil {
		return nil, fmt.Errorf("list observations: %w", err)
	}
	defer rows.Close()
	out := []ObservationRecord{}
	for rows.Next() {
		var r ObservationRecord
		var lat, lon *float64
		if err := rows.Scan(&r.ObservationID, &r.ObservedAt, &r.ReceivedAt, &r.EntityID, &r.FeedID, &r.Driver, &r.Decoder, &r.SourceEntityID, &r.Source,
			&r.Accepted, &r.RejectReason, &lat, &lon, &r.Altitude, &r.Heading, &r.SpeedKnots, &r.Course,
			&r.Identities, &r.Platform, &r.RawContentType, &r.RawTruncated); err != nil {
			return nil, fmt.Errorf("scan observation: %w", err)
		}
		if lat != nil && lon != nil {
			r.Position = &GeoPoint{Lat: *lat, Lon: *lon}
		}
		if len(r.Identities) == 0 {
			r.Identities = json.RawMessage("[]")
		}
		if len(r.Platform) == 0 {
			r.Platform = json.RawMessage("null")
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
