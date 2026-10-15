// Package locations owns the saved camera locations table (sentinel.locations).
package locations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when no location matches the given id.
var ErrNotFound = errors.New("location not found")

// Location mirrors the JSON shape the Angular client expects.
type Location struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	Latitude    float64   `json:"latitude"`
	Longitude   float64   `json:"longitude"`
	Altitude    float64   `json:"altitude"`
	Heading     float64   `json:"heading"`
	Pitch       float64   `json:"pitch"`
	Range       float64   `json:"range"`
	Has3DTiles  bool      `json:"has3dTiles"`
	Category    string    `json:"category"`
	CreatedBy   *string   `json:"createdBy"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Input carries the fields of a create or partial update. Nil means "not provided".
type Input struct {
	Name        *string
	Description *string
	Latitude    *float64
	Longitude   *float64
	Altitude    *float64
	Heading     *float64
	Pitch       *float64
	Range       *float64
	Has3DTiles  *bool
	Category    *string
	CreatedBy   *string
}

// Store is the persistence contract the handler depends on.
type Store interface {
	List(ctx context.Context, category, search string) ([]Location, error)
	Get(ctx context.Context, id string) (*Location, error)
	Create(ctx context.Context, in Input) (*Location, error)
	Update(ctx context.Context, id string, in Input) (*Location, error)
	Delete(ctx context.Context, id string) error
}

const selectColumns = `id, name, description, latitude, longitude, altitude, heading, pitch, "range", "has3dTiles", category, "createdBy", "createdAt", "updatedAt"`

// PGStore is the PostgreSQL implementation of Store.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore creates a store backed by the given pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

// Ping checks database connectivity.
func (s *PGStore) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func scanLocation(row pgx.Row) (*Location, error) {
	var l Location
	err := row.Scan(&l.ID, &l.Name, &l.Description, &l.Latitude, &l.Longitude, &l.Altitude, &l.Heading,
		&l.Pitch, &l.Range, &l.Has3DTiles, &l.Category, &l.CreatedBy, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &l, nil
}

// List returns locations ordered by name, optionally filtered by category and a
// case-insensitive name search.
func (s *PGStore) List(ctx context.Context, category, search string) ([]Location, error) {
	where := []string{}
	args := []interface{}{}
	if category != "" {
		args = append(args, category)
		where = append(where, fmt.Sprintf("category = $%d", len(args)))
	}
	if search != "" {
		args = append(args, "%"+search+"%")
		where = append(where, fmt.Sprintf("name ILIKE $%d", len(args)))
	}
	query := "SELECT " + selectColumns + " FROM sentinel.locations"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY name ASC"

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list locations: %w", err)
	}
	defer rows.Close()

	out := []Location{}
	for rows.Next() {
		l, err := scanLocation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan location: %w", err)
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// Get returns one location by id.
func (s *PGStore) Get(ctx context.Context, id string) (*Location, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+selectColumns+" FROM sentinel.locations WHERE id = $1", id)
	return scanLocation(row)
}

// Create inserts a location. Required fields must already be validated and
// defaults filled by the caller.
func (s *PGStore) Create(ctx context.Context, in Input) (*Location, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO sentinel.locations
			(name, description, latitude, longitude, altitude, heading, pitch, "range", "has3dTiles", category, "createdBy")
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING `+selectColumns,
		deref(in.Name), in.Description, derefF(in.Latitude), derefF(in.Longitude), derefF(in.Altitude),
		derefF(in.Heading), derefF(in.Pitch), derefF(in.Range), in.Has3DTiles != nil && *in.Has3DTiles,
		deref(in.Category), in.CreatedBy,
	)
	l, err := scanLocation(row)
	if err != nil {
		return nil, fmt.Errorf("create location: %w", err)
	}
	return l, nil
}

// Update applies the provided fields and returns the updated row.
func (s *PGStore) Update(ctx context.Context, id string, in Input) (*Location, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE sentinel.locations SET
			name        = COALESCE($2, name),
			description = COALESCE($3, description),
			latitude    = COALESCE($4, latitude),
			longitude   = COALESCE($5, longitude),
			altitude    = COALESCE($6, altitude),
			heading     = COALESCE($7, heading),
			pitch       = COALESCE($8, pitch),
			"range"     = COALESCE($9, "range"),
			"has3dTiles" = COALESCE($10, "has3dTiles"),
			category    = COALESCE($11, category),
			"createdBy" = COALESCE($12, "createdBy")
		WHERE id = $1
		RETURNING `+selectColumns,
		id, in.Name, in.Description, in.Latitude, in.Longitude, in.Altitude, in.Heading, in.Pitch, in.Range,
		in.Has3DTiles, in.Category, in.CreatedBy,
	)
	l, err := scanLocation(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("update location: %w", err)
	}
	return l, nil
}

// Delete removes a location.
func (s *PGStore) Delete(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM sentinel.locations WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("delete location: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefF(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}
