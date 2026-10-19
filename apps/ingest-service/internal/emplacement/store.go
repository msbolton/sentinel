package emplacement

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

// Filter narrows List; zero values match everything.
type Filter struct {
	FeedID   string
	Mobility string
	Active   *bool
}

// Store reads and writes emplacements. Deleted emplacements are invisible.
type Store interface {
	List(ctx context.Context, f Filter) ([]Emplacement, error)
	Get(ctx context.Context, id string) (Emplacement, error)
	Create(ctx context.Context, e Emplacement) (Emplacement, error)
	// Update reads the row for update, lets fn change it, and writes it back
	// with the version bumped. ifVersion > 0 refuses with ErrVersionConflict
	// when the stored version differs. An error from fn aborts the write and
	// is returned as it is.
	Update(ctx context.Context, id string, ifVersion int, fn func(*Emplacement) error) (Emplacement, error)
	Delete(ctx context.Context, id string) error
}

// PGStore is the Store on sentinel.emplacements.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore returns a store on pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const columns = `id::text, name, description, mobility, "sensorType",
	ST_Y(position), ST_X(position), altitude, "boresightDeg", "azimuthReference",
	coverage, "defaultSigma", "feedId", classification, exercise, active,
	"lastReportAt", COALESCE("createdBy", ''), "createdAt", "updatedAt", version`

type rowScanner interface{ Scan(dest ...any) error }

func scan(row rowScanner) (Emplacement, error) {
	var e Emplacement
	var lat, lon *float64
	var coverage, sigma []byte
	if err := row.Scan(&e.ID, &e.Name, &e.Description, &e.Mobility, &e.SensorType,
		&lat, &lon, &e.Altitude, &e.BoresightDeg, &e.AzimuthReference,
		&coverage, &sigma, &e.FeedID, &e.Classification, &e.Exercise, &e.Active,
		&e.LastReportAt, &e.CreatedBy, &e.CreatedAt, &e.UpdatedAt, &e.Version); err != nil {
		return Emplacement{}, err
	}
	if lat != nil && lon != nil {
		e.Position = &Point{Lat: *lat, Lon: *lon}
	}
	if err := json.Unmarshal(coverage, &e.Coverage); err != nil {
		return Emplacement{}, fmt.Errorf("decoding coverage of %s: %w", e.ID, err)
	}
	if err := json.Unmarshal(sigma, &e.DefaultSigma); err != nil {
		return Emplacement{}, fmt.Errorf("decoding defaultSigma of %s: %w", e.ID, err)
	}
	return e, nil
}

// mapErr turns the Postgres errors a caller can cause into this package's.
func mapErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505": // unique_violation: the live-name index
			return ErrNameTaken
		case "23503": // foreign_key_violation: feedId
			return &ValidationError{Problems: []Problem{{Path: "/feedId", Message: "names no connection"}}}
		case "22P02": // invalid_text_representation: a malformed uuid
			return ErrNotFound
		}
	}
	return err
}

func latLon(p *Point) (lat, lon *float64) {
	if p == nil {
		return nil, nil
	}
	return &p.Lat, &p.Lon
}

func notify(ctx context.Context, tx pgx.Tx, id, op string) error {
	payload, _ := json.Marshal(map[string]string{"id": id, "op": op})
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

// List returns the live emplacements that match f, by name.
func (s *PGStore) List(ctx context.Context, f Filter) ([]Emplacement, error) {
	q := `SELECT ` + columns + ` FROM sentinel.emplacements WHERE "deletedAt" IS NULL`
	var args []any
	if f.FeedID != "" {
		args = append(args, f.FeedID)
		q += fmt.Sprintf(` AND "feedId" = $%d`, len(args))
	}
	if f.Mobility != "" {
		args = append(args, f.Mobility)
		q += fmt.Sprintf(` AND mobility = $%d`, len(args))
	}
	if f.Active != nil {
		args = append(args, *f.Active)
		q += fmt.Sprintf(` AND active = $%d`, len(args))
	}
	q += ` ORDER BY lower(name), id`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Emplacement{}
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Get returns one live emplacement.
func (s *PGStore) Get(ctx context.Context, id string) (Emplacement, error) {
	e, err := scan(s.pool.QueryRow(ctx, `SELECT `+columns+` FROM sentinel.emplacements WHERE id = $1::uuid AND "deletedAt" IS NULL`, id))
	if err != nil {
		return Emplacement{}, mapErr(err)
	}
	return e, nil
}

// Create inserts e; its id, timestamps and version come from the database.
func (s *PGStore) Create(ctx context.Context, e Emplacement) (Emplacement, error) {
	coverage, _ := json.Marshal(e.Coverage)
	sigma, _ := json.Marshal(e.DefaultSigma)
	lat, lon := latLon(e.Position)
	var out Emplacement
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scan(tx.QueryRow(ctx, `INSERT INTO sentinel.emplacements
			(name, description, mobility, "sensorType", position, altitude, "boresightDeg", "azimuthReference",
			 coverage, "defaultSigma", "feedId", classification, exercise, active, "createdBy")
			VALUES ($1, $2, $3, $4,
			        CASE WHEN $5::float8 IS NULL THEN NULL ELSE ST_SetSRID(ST_MakePoint($6::float8, $5::float8), 4326) END,
			        $7, $8, $9, $10, $11, $12, $13, $14, $15, NULLIF($16, ''))
			RETURNING `+columns,
			e.Name, e.Description, e.Mobility, e.SensorType, lat, lon, e.Altitude, e.BoresightDeg, e.AzimuthReference,
			coverage, sigma, e.FeedID, e.Classification, e.Exercise, e.Active, e.CreatedBy))
		if err != nil {
			return mapErr(err)
		}
		return notify(ctx, tx, out.ID, "upsert")
	})
	return out, err
}

// Update applies fn to the stored emplacement under a row lock.
func (s *PGStore) Update(ctx context.Context, id string, ifVersion int, fn func(*Emplacement) error) (Emplacement, error) {
	var out Emplacement
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		cur, err := scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM sentinel.emplacements WHERE id = $1::uuid AND "deletedAt" IS NULL FOR UPDATE`, id))
		if err != nil {
			return mapErr(err)
		}
		if ifVersion > 0 && cur.Version != ifVersion {
			return ErrVersionConflict
		}
		if err := fn(&cur); err != nil {
			return err
		}
		coverage, _ := json.Marshal(cur.Coverage)
		sigma, _ := json.Marshal(cur.DefaultSigma)
		lat, lon := latLon(cur.Position)
		out, err = scan(tx.QueryRow(ctx, `UPDATE sentinel.emplacements SET
			name = $2, description = $3, mobility = $4, "sensorType" = $5,
			position = CASE WHEN $6::float8 IS NULL THEN NULL ELSE ST_SetSRID(ST_MakePoint($7::float8, $6::float8), 4326) END,
			altitude = $8, "boresightDeg" = $9, "azimuthReference" = $10, coverage = $11, "defaultSigma" = $12,
			"feedId" = $13, classification = $14, exercise = $15, active = $16,
			version = version + 1, "updatedAt" = NOW()
			WHERE id = $1::uuid
			RETURNING `+columns,
			id, cur.Name, cur.Description, cur.Mobility, cur.SensorType, lat, lon, cur.Altitude, cur.BoresightDeg,
			cur.AzimuthReference, coverage, sigma, cur.FeedID, cur.Classification, cur.Exercise, cur.Active))
		if err != nil {
			return mapErr(err)
		}
		return notify(ctx, tx, out.ID, "upsert")
	})
	return out, err
}

// Delete soft-deletes a live emplacement.
func (s *PGStore) Delete(ctx context.Context, id string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE sentinel.emplacements SET "deletedAt" = NOW(), version = version + 1, "updatedAt" = NOW()
			WHERE id = $1::uuid AND "deletedAt" IS NULL`, id)
		if err != nil {
			return mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return notify(ctx, tx, id, "delete")
	})
}

// Toucher records an emplacement's own report: the gRPC driver calls it for
// a mobile emplacement's position reports, throttled.
type Toucher interface {
	Touch(ctx context.Context, id string, pos *Point, alt *float64, at time.Time) error
}

// Touch sets lastReportAt, the position when pos is not nil and the altitude
// when alt is not nil, on a live emplacement. It does not bump the version and sends no notification:
// position reports arrive every second, and every replica refreshing its
// cache that often would be all the cache did. Editors therefore never see a
// version conflict caused by a sensor reporting where it is.
func (s *PGStore) Touch(ctx context.Context, id string, pos *Point, alt *float64, at time.Time) error {
	lat, lon := latLon(pos)
	tag, err := s.pool.Exec(ctx, `UPDATE sentinel.emplacements SET "lastReportAt" = $2,
		position = CASE WHEN $3::float8 IS NULL THEN position ELSE ST_SetSRID(ST_MakePoint($4::float8, $3::float8), 4326) END,
		altitude = CASE WHEN $5::float8 IS NULL THEN altitude ELSE $5::float8 END
		WHERE id = $1::uuid AND "deletedAt" IS NULL`, id, at, lat, lon, alt)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
