package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// AlertRecord represents an alert row from the database.
type AlertRecord struct {
	ID               string            `json:"alertId"`
	AlertType        string            `json:"alertType"`
	Severity         string            `json:"severity"`
	Title            string            `json:"title"`
	Description      *string           `json:"description,omitempty"`
	EntityID         string            `json:"entityId"`
	RelatedEntityIDs []string          `json:"relatedEntityIds"`
	Position         *GeoPoint         `json:"position,omitempty"`
	RuleID           *string           `json:"ruleId,omitempty"`
	Metadata         map[string]string `json:"metadata"`
	CreatedAt        string            `json:"createdAt"`
	AcknowledgedAt   *string           `json:"acknowledgedAt,omitempty"`
	AcknowledgedBy   *string           `json:"acknowledgedBy,omitempty"`
	ResolvedAt       *string           `json:"resolvedAt,omitempty"`
}

// GeoPoint represents a geographic point.
type GeoPoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// AlertRuleRecord represents an alert rule row from the database.
type AlertRuleRecord struct {
	ID                   string                 `json:"id"`
	Name                 string                 `json:"name"`
	RuleType             string                 `json:"ruleType"`
	Config               map[string]interface{} `json:"config"`
	MonitoredEntityTypes []string               `json:"monitoredEntityTypes"`
	Severity             string                 `json:"severity"`
	Enabled              bool                   `json:"enabled"`
	CreatedAt            string                 `json:"createdAt"`
	UpdatedAt            string                 `json:"updatedAt"`
}

// CreateAlertParams holds parameters for creating an alert.
type CreateAlertParams struct {
	AlertType        string
	Severity         string
	Title            string
	Description      string
	EntityID         string
	RelatedEntityIDs []string
	Lat              *float64
	Lng              *float64
	RuleID           *string
	Metadata         map[string]string
}

// CreateRuleParams holds parameters for creating a rule.
type CreateRuleParams struct {
	Name                 string
	RuleType             string
	Config               map[string]interface{}
	MonitoredEntityTypes []string
	Severity             string
	Enabled              bool
}

// UpdateRuleParams holds parameters for updating a rule.
type UpdateRuleParams struct {
	Name                 *string
	RuleType             *string
	Config               map[string]interface{}
	MonitoredEntityTypes []string
	Severity             *string
	Enabled              *bool
}

// AlertQueryParams holds parameters for querying alerts.
type AlertQueryParams struct {
	Severity     *string
	Types        []string
	EntityID     *string
	Acknowledged *bool
	Page         int
	PageSize     int
}

// AlertQueryResult holds paginated alert query results.
type AlertQueryResult struct {
	Data     []AlertRecord `json:"data"`
	Total    int           `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"pageSize"`
}

// AlertStore handles database operations for alerts and rules.
type AlertStore struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

// NewAlertStore creates a new alert store.
func NewAlertStore(pool *pgxpool.Pool, logger *zap.Logger) *AlertStore {
	return &AlertStore{pool: pool, logger: logger}
}

// CreateAlert inserts a new alert and returns the created record.
func (s *AlertStore) CreateAlert(ctx context.Context, p CreateAlertParams) (*AlertRecord, error) {
	relatedIDs := p.RelatedEntityIDs
	if relatedIDs == nil {
		relatedIDs = []string{}
	}
	metadata := p.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}

	var positionExpr string
	args := []interface{}{
		p.AlertType, p.Severity, p.Title, p.Description, p.EntityID,
		relatedIDs, p.RuleID, metadata,
	}

	if p.Lat != nil && p.Lng != nil {
		positionExpr = `ST_SetSRID(ST_MakePoint($9, $10), 4326)`
		args = append(args, *p.Lng, *p.Lat)
	} else {
		positionExpr = "NULL"
	}

	query := fmt.Sprintf(`
		INSERT INTO sentinel.alerts
			("alertType", severity, title, description, "entityId", "relatedEntityIds", position, "ruleId", metadata)
		VALUES ($1, $2, $3, $4, $5, $6, %s, $7, $8)
		RETURNING id, "alertType", severity, title, description, "entityId", "relatedEntityIds",
			ST_Y(position::geometry) as lat, ST_X(position::geometry) as lng,
			"ruleId", metadata, "createdAt"::text,
			"acknowledgedAt"::text, "acknowledgedBy", "resolvedAt"::text
	`, positionExpr)

	row := s.pool.QueryRow(ctx, query, args...)
	return scanAlert(row)
}

// GetAlerts queries alerts with filters and pagination.
func (s *AlertStore) GetAlerts(ctx context.Context, p AlertQueryParams) (*AlertQueryResult, error) {
	where := []string{}
	args := []interface{}{}
	argIdx := 1

	if p.Severity != nil {
		where = append(where, fmt.Sprintf(`severity = $%d`, argIdx))
		args = append(args, *p.Severity)
		argIdx++
	}

	if len(p.Types) > 0 {
		placeholders := make([]string, len(p.Types))
		for i, t := range p.Types {
			placeholders[i] = fmt.Sprintf("$%d", argIdx)
			args = append(args, t)
			argIdx++
		}
		where = append(where, fmt.Sprintf(`"alertType" IN (%s)`, strings.Join(placeholders, ",")))
	}

	if p.EntityID != nil {
		where = append(where, fmt.Sprintf(`"entityId" = $%d`, argIdx))
		args = append(args, *p.EntityID)
		argIdx++
	}

	if p.Acknowledged != nil {
		if *p.Acknowledged {
			where = append(where, `"acknowledgedAt" IS NOT NULL`)
		} else {
			where = append(where, `"acknowledgedAt" IS NULL`)
		}
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	// Count total.
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM sentinel.alerts %s`, whereClause)
	var total int
	if err := s.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count alerts: %w", err)
	}

	page := p.Page
	if page < 1 {
		page = 1
	}
	pageSize := p.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}

	offset := (page - 1) * pageSize
	dataQuery := fmt.Sprintf(`
		SELECT id, "alertType", severity, title, description, "entityId", "relatedEntityIds",
			ST_Y(position::geometry) as lat, ST_X(position::geometry) as lng,
			"ruleId", metadata, "createdAt"::text,
			"acknowledgedAt"::text, "acknowledgedBy", "resolvedAt"::text
		FROM sentinel.alerts
		%s
		ORDER BY "createdAt" DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIdx, argIdx+1)
	args = append(args, pageSize, offset)

	rows, err := s.pool.Query(ctx, dataQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query alerts: %w", err)
	}
	defer rows.Close()

	alerts := []AlertRecord{}
	for rows.Next() {
		rec, err := scanAlertRow(rows)
		if err != nil {
			return nil, err
		}
		alerts = append(alerts, *rec)
	}

	return &AlertQueryResult{
		Data:     alerts,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// GetAlert returns a single alert by ID.
func (s *AlertStore) GetAlert(ctx context.Context, id string) (*AlertRecord, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, "alertType", severity, title, description, "entityId", "relatedEntityIds",
			ST_Y(position::geometry) as lat, ST_X(position::geometry) as lng,
			"ruleId", metadata, "createdAt"::text,
			"acknowledgedAt"::text, "acknowledgedBy", "resolvedAt"::text
		FROM sentinel.alerts
		WHERE id = $1
	`, id)
	return scanAlert(row)
}

// AcknowledgeAlert marks an alert as acknowledged.
func (s *AlertStore) AcknowledgeAlert(ctx context.Context, id, userID string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sentinel.alerts
		SET "acknowledgedAt" = NOW(), "acknowledgedBy" = $2
		WHERE id = $1
	`, id, userID)
	if err != nil {
		return fmt.Errorf("acknowledge alert: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("alert not found: %s", id)
	}
	return nil
}

// ResolveAlert marks an alert as resolved.
func (s *AlertStore) ResolveAlert(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sentinel.alerts
		SET "resolvedAt" = NOW()
		WHERE id = $1
	`, id, )
	if err != nil {
		return fmt.Errorf("resolve alert: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("alert not found: %s", id)
	}
	return nil
}

// CreateRule inserts a new alert rule.
func (s *AlertStore) CreateRule(ctx context.Context, p CreateRuleParams) (*AlertRuleRecord, error) {
	entityTypes := p.MonitoredEntityTypes
	if entityTypes == nil {
		entityTypes = []string{}
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO sentinel.alert_rules
			(name, "ruleType", config, "monitoredEntityTypes", severity, enabled)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, name, "ruleType", config, "monitoredEntityTypes", severity, enabled,
			"createdAt"::text, "updatedAt"::text
	`, p.Name, p.RuleType, p.Config, entityTypes, p.Severity, p.Enabled)

	return scanRule(row)
}

// GetRules returns all alert rules ordered by creation time.
func (s *AlertStore) GetRules(ctx context.Context) ([]AlertRuleRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, "ruleType", config, "monitoredEntityTypes", severity, enabled,
			"createdAt"::text, "updatedAt"::text
		FROM sentinel.alert_rules
		ORDER BY "createdAt" DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("get rules: %w", err)
	}
	defer rows.Close()

	var rules []AlertRuleRecord
	for rows.Next() {
		var r AlertRuleRecord
		if err := rows.Scan(
			&r.ID, &r.Name, &r.RuleType, &r.Config, &r.MonitoredEntityTypes,
			&r.Severity, &r.Enabled, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan rule: %w", err)
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// UpdateRule updates an existing alert rule.
func (s *AlertStore) UpdateRule(ctx context.Context, id string, p UpdateRuleParams) (*AlertRuleRecord, error) {
	sets := []string{`"updatedAt" = NOW()`}
	args := []interface{}{id}
	argIdx := 2

	if p.Name != nil {
		sets = append(sets, fmt.Sprintf(`name = $%d`, argIdx))
		args = append(args, *p.Name)
		argIdx++
	}
	if p.RuleType != nil {
		sets = append(sets, fmt.Sprintf(`"ruleType" = $%d`, argIdx))
		args = append(args, *p.RuleType)
		argIdx++
	}
	if p.Config != nil {
		sets = append(sets, fmt.Sprintf(`config = $%d`, argIdx))
		args = append(args, p.Config)
		argIdx++
	}
	if p.MonitoredEntityTypes != nil {
		sets = append(sets, fmt.Sprintf(`"monitoredEntityTypes" = $%d`, argIdx))
		args = append(args, p.MonitoredEntityTypes)
		argIdx++
	}
	if p.Severity != nil {
		sets = append(sets, fmt.Sprintf(`severity = $%d`, argIdx))
		args = append(args, *p.Severity)
		argIdx++
	}
	if p.Enabled != nil {
		sets = append(sets, fmt.Sprintf(`enabled = $%d`, argIdx))
		args = append(args, *p.Enabled)
		argIdx++
	}

	query := fmt.Sprintf(`
		UPDATE sentinel.alert_rules
		SET %s
		WHERE id = $1
		RETURNING id, name, "ruleType", config, "monitoredEntityTypes", severity, enabled,
			"createdAt"::text, "updatedAt"::text
	`, strings.Join(sets, ", "))

	row := s.pool.QueryRow(ctx, query, args...)
	return scanRule(row)
}

// GetEnabledRules returns enabled rules of a given type, optionally filtered by entity type.
func (s *AlertStore) GetEnabledRules(ctx context.Context, ruleType string, entityType *string) ([]AlertRuleRecord, error) {
	query := `
		SELECT id, name, "ruleType", config, "monitoredEntityTypes", severity, enabled,
			"createdAt"::text, "updatedAt"::text
		FROM sentinel.alert_rules
		WHERE enabled = true AND "ruleType" = $1
	`
	args := []interface{}{ruleType}

	if entityType != nil {
		query += ` AND (cardinality("monitoredEntityTypes") = 0 OR $2 = ANY("monitoredEntityTypes"))`
		args = append(args, *entityType)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get enabled rules: %w", err)
	}
	defer rows.Close()

	var rules []AlertRuleRecord
	for rows.Next() {
		var r AlertRuleRecord
		if err := rows.Scan(
			&r.ID, &r.Name, &r.RuleType, &r.Config, &r.MonitoredEntityTypes,
			&r.Severity, &r.Enabled, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan rule: %w", err)
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// Pool returns the underlying connection pool for direct queries.
func (s *AlertStore) Pool() *pgxpool.Pool {
	return s.pool
}

// Ping checks database connectivity.
func (s *AlertStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func scanAlert(row pgx.Row) (*AlertRecord, error) {
	var rec AlertRecord
	var lat, lng *float64
	err := row.Scan(
		&rec.ID, &rec.AlertType, &rec.Severity, &rec.Title, &rec.Description,
		&rec.EntityID, &rec.RelatedEntityIDs,
		&lat, &lng,
		&rec.RuleID, &rec.Metadata, &rec.CreatedAt,
		&rec.AcknowledgedAt, &rec.AcknowledgedBy, &rec.ResolvedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan alert: %w", err)
	}
	if lat != nil && lng != nil {
		rec.Position = &GeoPoint{Lat: *lat, Lon: *lng}
	}
	return &rec, nil
}

func scanAlertRow(rows pgx.Rows) (*AlertRecord, error) {
	var rec AlertRecord
	var lat, lng *float64
	err := rows.Scan(
		&rec.ID, &rec.AlertType, &rec.Severity, &rec.Title, &rec.Description,
		&rec.EntityID, &rec.RelatedEntityIDs,
		&lat, &lng,
		&rec.RuleID, &rec.Metadata, &rec.CreatedAt,
		&rec.AcknowledgedAt, &rec.AcknowledgedBy, &rec.ResolvedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan alert row: %w", err)
	}
	if lat != nil && lng != nil {
		rec.Position = &GeoPoint{Lat: *lat, Lon: *lng}
	}
	return &rec, nil
}

func scanRule(row pgx.Row) (*AlertRuleRecord, error) {
	var r AlertRuleRecord
	err := row.Scan(
		&r.ID, &r.Name, &r.RuleType, &r.Config, &r.MonitoredEntityTypes,
		&r.Severity, &r.Enabled, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan rule: %w", err)
	}
	return &r, nil
}
