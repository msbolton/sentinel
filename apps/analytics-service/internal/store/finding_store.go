package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sentinel/go-common/classification"
)

// Finding statuses.
const (
	StatusOpen      = "OPEN"
	StatusConfirmed = "CONFIRMED"
	StatusDismissed = "DISMISSED"
	StatusClosed    = "CLOSED"
)

// ErrNotFound is returned for a missing finding.
var ErrNotFound = errors.New("finding not found")

// ErrBadTransition is returned for a review the finding's status does not allow.
var ErrBadTransition = errors.New("finding cannot be reviewed from its current status")

// GeoPoint is a position in the API.
type GeoPoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// Finding is one departure from an entity's baseline.
type Finding struct {
	ID             string         `json:"id"`
	EntityID       string         `json:"entityId"`
	Kind           string         `json:"kind"`
	Severity       string         `json:"severity"`
	Score          float64        `json:"score"`
	Summary        string         `json:"summary"`
	Expected       map[string]any `json:"expected"`
	Actual         map[string]any `json:"actual"`
	Thresholds     map[string]any `json:"thresholds"`
	Position       *GeoPoint      `json:"position,omitempty"`
	EvidenceStart  time.Time      `json:"evidenceStart"`
	EvidenceEnd    time.Time      `json:"evidenceEnd"`
	Status         string         `json:"status"`
	ReviewedBy     *string        `json:"reviewedBy,omitempty"`
	ReviewedAt     *time.Time     `json:"reviewedAt,omitempty"`
	AlertID        *string        `json:"alertId,omitempty"`
	Classification string         `json:"classification"`
	Hits           int            `json:"hits"`
	FirstAt        time.Time      `json:"firstAt"`
	LastAt         time.Time      `json:"lastAt"`
	ClosedAt       *time.Time     `json:"closedAt,omitempty"`
}

// OpenParams is a detector hit to record.
type OpenParams struct {
	EntityID       string
	Kind           string
	Severity       string
	Score          float64
	Summary        string
	Expected       map[string]any
	Actual         map[string]any
	Thresholds     map[string]any
	Position       *GeoPoint
	EvidenceStart  time.Time
	EvidenceEnd    time.Time
	Classification string
}

// Query filters a findings list.
type Query struct {
	Status   string
	Kinds    []string
	Severity string
	EntityID string
	Since    *time.Time
	Ceiling  string
	Page     int
	PageSize int
}

// FindingStore keeps sentinel.findings.
type FindingStore struct {
	pool *pgxpool.Pool
}

// NewFindingStore creates a finding store.
func NewFindingStore(pool *pgxpool.Pool) *FindingStore {
	return &FindingStore{pool: pool}
}

// severityRank is the SQL that orders severities, used to raise but never
// lower an open finding's severity.
func severityRank(expr string) string {
	return fmt.Sprintf(`(CASE %s WHEN 'CRITICAL' THEN 5 WHEN 'HIGH' THEN 4 WHEN 'MEDIUM' THEN 3 WHEN 'LOW' THEN 2 ELSE 1 END)`, expr)
}

const findingColumns = `id, "entityId", kind, severity, score, summary, expected, actual, thresholds,
	ST_Y(position), ST_X(position), "evidenceStart", "evidenceEnd", status, "reviewedBy", "reviewedAt",
	"alertId"::text, classification, hits, "firstAt", "lastAt", "closedAt"`

// UpsertOpen records a hit: it opens a finding, or when one is open for
// the same entity and kind, absorbs the hit into it (hits, last time,
// evidence end, latest actual values, the higher severity and score).
// created reports whether a new finding was opened.
func (s *FindingStore) UpsertOpen(ctx context.Context, p OpenParams) (*Finding, bool, error) {
	expected, actual, thresholds, err := encodeJSON3(p.Expected, p.Actual, p.Thresholds)
	if err != nil {
		return nil, false, err
	}
	var lat, lon *float64
	if p.Position != nil {
		lat, lon = &p.Position.Lat, &p.Position.Lon
	}
	level := classification.Normalize(p.Classification)
	sql := `
		INSERT INTO sentinel.findings ("entityId", kind, severity, score, summary, expected, actual, thresholds, position,
			"evidenceStart", "evidenceEnd", classification, "firstAt", "lastAt")
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8::jsonb,
			CASE WHEN $9::float8 IS NULL THEN NULL ELSE ST_SetSRID(ST_MakePoint($10::float8, $9::float8), 4326) END,
			$11, $12, $13, $12, $12)
		ON CONFLICT ("entityId", kind) WHERE status = 'OPEN' DO UPDATE SET
			hits = sentinel.findings.hits + 1,
			"lastAt" = GREATEST(EXCLUDED."lastAt", sentinel.findings."lastAt"),
			"evidenceEnd" = GREATEST(EXCLUDED."evidenceEnd", sentinel.findings."evidenceEnd"),
			actual = EXCLUDED.actual,
			position = COALESCE(EXCLUDED.position, sentinel.findings.position),
			score = GREATEST(EXCLUDED.score, sentinel.findings.score),
			severity = CASE WHEN ` + severityRank("EXCLUDED.severity") + ` > ` + severityRank("sentinel.findings.severity") + ` THEN EXCLUDED.severity ELSE sentinel.findings.severity END,
			summary = CASE WHEN ` + severityRank("EXCLUDED.severity") + ` > ` + severityRank("sentinel.findings.severity") + ` THEN EXCLUDED.summary ELSE sentinel.findings.summary END,
			classification = CASE WHEN sentinel.classification_rank(EXCLUDED.classification) > sentinel.classification_rank(sentinel.findings.classification) THEN EXCLUDED.classification ELSE sentinel.findings.classification END
		RETURNING ` + findingColumns + `, (xmax = 0) AS created`
	row := s.pool.QueryRow(ctx, sql, p.EntityID, p.Kind, p.Severity, p.Score, p.Summary, expected, actual, thresholds,
		lat, lon, p.EvidenceStart.UTC(), p.EvidenceEnd.UTC(), level)
	f, created, err := scanFinding(row, true)
	if err != nil {
		return nil, false, fmt.Errorf("upsert finding: %w", err)
	}
	return f, created, nil
}

// CloseStale closes open findings whose last evidence is older than before.
func (s *FindingStore) CloseStale(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE sentinel.findings SET status = 'CLOSED', "closedAt" = NOW() WHERE status = 'OPEN' AND "lastAt" < $1`, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("close stale findings: %w", err)
	}
	return tag.RowsAffected(), nil
}

// CloseOpen closes the open finding of a kind for an entity, if any.
func (s *FindingStore) CloseOpen(ctx context.Context, entityID, kind string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE sentinel.findings SET status = 'CLOSED', "closedAt" = NOW() WHERE status = 'OPEN' AND "entityId" = $1 AND kind = $2`, entityID, kind)
	if err != nil {
		return fmt.Errorf("close finding: %w", err)
	}
	return nil
}

// SetAlertID records the alert a finding was published as.
func (s *FindingStore) SetAlertID(ctx context.Context, id, alertID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE sentinel.findings SET "alertId" = $2 WHERE id = $1`, id, alertID)
	if err != nil {
		return fmt.Errorf("set alert id: %w", err)
	}
	return nil
}

// Get loads one finding.
func (s *FindingStore) Get(ctx context.Context, id string) (*Finding, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+findingColumns+` FROM sentinel.findings WHERE id = $1`, id)
	f, _, err := scanFinding(row, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get finding: %w", err)
	}
	return f, nil
}

// List returns a page of findings under the caller's ceiling.
func (s *FindingStore) List(ctx context.Context, q Query) ([]Finding, int64, error) {
	where := []string{}
	args := []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if q.Status != "" {
		add(`status = $%d`, strings.ToUpper(q.Status))
	}
	if len(q.Kinds) > 0 {
		add(`kind = ANY($%d)`, q.Kinds)
	}
	if q.Severity != "" {
		add(`severity = $%d`, strings.ToUpper(q.Severity))
	}
	if q.EntityID != "" {
		add(`"entityId" = $%d`, q.EntityID)
	}
	if q.Since != nil {
		add(`"lastAt" >= $%d`, q.Since.UTC())
	}
	add(`sentinel.classification_rank(classification) <= $%d`, classification.Rank(q.Ceiling))
	clause := "WHERE " + strings.Join(where, " AND ")

	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM sentinel.findings `+clause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count findings: %w", err)
	}
	page, size := q.Page, q.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 50
	}
	if size > 500 {
		size = 500
	}
	args = append(args, size, (page-1)*size)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT %s FROM sentinel.findings %s ORDER BY "lastAt" DESC LIMIT $%d OFFSET $%d`,
		findingColumns, clause, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list findings: %w", err)
	}
	defer rows.Close()
	out := []Finding{}
	for rows.Next() {
		f, _, err := scanFinding(rows, false)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *f)
	}
	return out, total, rows.Err()
}

// Review confirms or dismisses a finding. OPEN may become CONFIRMED or
// DISMISSED; CONFIRMED may still be DISMISSED.
func (s *FindingStore) Review(ctx context.Context, id, status, user string) (*Finding, error) {
	status = strings.ToUpper(status)
	if status != StatusConfirmed && status != StatusDismissed {
		return nil, ErrBadTransition
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE sentinel.findings SET status = $2, "reviewedBy" = $3, "reviewedAt" = NOW()
		WHERE id = $1 AND (status = 'OPEN' OR (status = 'CONFIRMED' AND $2 = 'DISMISSED'))
		RETURNING `+findingColumns, id, status, user)
	f, _, err := scanFinding(row, false)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, gerr := s.Get(ctx, id); gerr != nil {
			return nil, gerr
		}
		return nil, ErrBadTransition
	}
	if err != nil {
		return nil, fmt.Errorf("review finding: %w", err)
	}
	return f, nil
}

// Stats summarises findings under a ceiling: open by severity and kind,
// and how many were opened in the last day.
func (s *FindingStore) Stats(ctx context.Context, ceiling string) (map[string]any, error) {
	rank := classification.Rank(ceiling)
	out := map[string]any{}
	bySeverity := map[string]int64{}
	byKind := map[string]int64{}
	rows, err := s.pool.Query(ctx,
		`SELECT severity, kind, COUNT(*) FROM sentinel.findings WHERE status = 'OPEN' AND sentinel.classification_rank(classification) <= $1 GROUP BY severity, kind`, rank)
	if err != nil {
		return nil, fmt.Errorf("finding stats: %w", err)
	}
	defer rows.Close()
	var open int64
	for rows.Next() {
		var sev, kind string
		var n int64
		if err := rows.Scan(&sev, &kind, &n); err != nil {
			return nil, err
		}
		bySeverity[sev] += n
		byKind[kind] += n
		open += n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var last24h int64
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM sentinel.findings WHERE "firstAt" > NOW() - INTERVAL '24 hours' AND sentinel.classification_rank(classification) <= $1`, rank).Scan(&last24h); err != nil {
		return nil, err
	}
	out["open"] = open
	out["openBySeverity"] = bySeverity
	out["openByKind"] = byKind
	out["openedLast24h"] = last24h
	return out, nil
}

func encodeJSON(maps ...map[string]any) ([]string, error) {
	out := make([]string, len(maps))
	for i, m := range maps {
		if m == nil {
			m = map[string]any{}
		}
		b, err := json.Marshal(m)
		if err != nil {
			return nil, fmt.Errorf("encode json: %w", err)
		}
		out[i] = string(b)
	}
	return out, nil
}

func encodeJSON3(a, b, c map[string]any) (string, string, string, error) {
	out, err := encodeJSON(a, b, c)
	if err != nil {
		return "", "", "", err
	}
	return out[0], out[1], out[2], nil
}

func scanFinding(row pgx.Row, withCreated bool) (*Finding, bool, error) {
	var f Finding
	var expected, actual, thresholds []byte
	var lat, lon *float64
	var created bool
	dest := []any{&f.ID, &f.EntityID, &f.Kind, &f.Severity, &f.Score, &f.Summary, &expected, &actual, &thresholds,
		&lat, &lon, &f.EvidenceStart, &f.EvidenceEnd, &f.Status, &f.ReviewedBy, &f.ReviewedAt,
		&f.AlertID, &f.Classification, &f.Hits, &f.FirstAt, &f.LastAt, &f.ClosedAt}
	if withCreated {
		dest = append(dest, &created)
	}
	if err := row.Scan(dest...); err != nil {
		return nil, false, err
	}
	if lat != nil && lon != nil {
		f.Position = &GeoPoint{Lat: *lat, Lon: *lon}
	}
	for _, pair := range []struct {
		raw []byte
		dst *map[string]any
	}{{expected, &f.Expected}, {actual, &f.Actual}, {thresholds, &f.Thresholds}} {
		*pair.dst = map[string]any{}
		if len(pair.raw) > 0 {
			if err := json.Unmarshal(pair.raw, pair.dst); err != nil {
				return nil, false, fmt.Errorf("decode finding json: %w", err)
			}
		}
	}
	return &f, created, nil
}
