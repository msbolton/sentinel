// Package handler serves the analytics REST API under /api/v1/analytics.
// Every read applies the caller's clearance as a ceiling.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/classification"
	"github.com/sentinel/go-common/httputil"
	"github.com/sentinel/go-common/middleware"

	"github.com/sentinel/analytics-service/internal/baseline"
	"github.com/sentinel/analytics-service/internal/store"
)

// Stats supplies the counters the stats endpoint reports.
type Stats interface {
	Snapshot() map[string]any
}

// CoverageCounter supplies the coverage row count for the stats endpoint.
type CoverageCounter interface {
	Count(ctx context.Context) (int64, error)
}

// Baselines reads baselines; nil, nil means the entity has none.
type Baselines interface {
	Get(ctx context.Context, entityID string) (*baseline.Baseline, error)
	Count(ctx context.Context) (int64, error)
}

// Entities answers the classification of an entity.
type Entities interface {
	Classification(ctx context.Context, entityID string) (string, error)
}

// Findings is the part of the finding store the API uses.
type Findings interface {
	List(ctx context.Context, q store.Query) ([]store.Finding, int64, error)
	Get(ctx context.Context, id string) (*store.Finding, error)
	Review(ctx context.Context, id, status, user string) (*store.Finding, error)
	Stats(ctx context.Context, ceiling string) (map[string]any, error)
}

// Escalator publishes a confirmed finding as an alert.
type Escalator interface {
	Publish(ctx context.Context, f *store.Finding)
}

// AnalyticsHandler holds the collaborators of the API.
type AnalyticsHandler struct {
	stats     Stats
	coverage  CoverageCounter
	baselines Baselines
	entities  Entities
	findings  Findings
	escalator Escalator
	logger    *zap.Logger
}

// NewAnalyticsHandler creates the handler.
func NewAnalyticsHandler(stats Stats, coverage CoverageCounter, baselines Baselines, entities Entities, findings Findings, escalator Escalator, logger *zap.Logger) *AnalyticsHandler {
	return &AnalyticsHandler{stats: stats, coverage: coverage, baselines: baselines, entities: entities, findings: findings, escalator: escalator, logger: logger}
}

// RegisterRoutes registers the analytics endpoints on the mux.
func (h *AnalyticsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/analytics/stats", h.handleStats)
	mux.HandleFunc("GET /api/v1/analytics/findings", h.handleListFindings)
	mux.HandleFunc("GET /api/v1/analytics/findings/{id}", h.handleGetFinding)
	mux.HandleFunc("PATCH /api/v1/analytics/findings/{id}", h.handleReviewFinding)
	mux.HandleFunc("GET /api/v1/analytics/entities/{id}/baseline", h.handleBaseline)
	mux.HandleFunc("GET /api/v1/analytics/entities/{id}/findings", h.handleEntityFindings)
}

// ceilingOf is the caller's clearance as propagated by the gateway; a
// missing header fails closed to UNCLASSIFIED.
func ceilingOf(r *http.Request) string {
	return classification.Normalize(middleware.Classification(r.Context()))
}

// entityVisible reports whether the caller may see an entity at all; an
// entity above the caller's clearance does not exist for them.
func (h *AnalyticsHandler) entityVisible(w http.ResponseWriter, r *http.Request, entityID string) bool {
	level, err := h.entities.Classification(r.Context(), entityID)
	if err != nil {
		h.logger.Error("entity classification lookup failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to resolve entity")
		return false
	}
	if classification.Rank(level) > classification.Rank(ceilingOf(r)) {
		httputil.Error(w, http.StatusNotFound, "Entity not found")
		return false
	}
	return true
}

// findingVisible loads a finding the caller is cleared to see.
func (h *AnalyticsHandler) findingVisible(w http.ResponseWriter, r *http.Request, id string) (*store.Finding, bool) {
	f, err := h.findings.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httputil.Error(w, http.StatusNotFound, "Finding not found")
		return nil, false
	}
	if err != nil {
		h.logger.Error("get finding failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to get finding")
		return nil, false
	}
	if classification.Rank(f.Classification) > classification.Rank(ceilingOf(r)) {
		httputil.Error(w, http.StatusNotFound, "Finding not found")
		return nil, false
	}
	return f, true
}

func (h *AnalyticsHandler) handleStats(w http.ResponseWriter, r *http.Request) {
	out := h.stats.Snapshot()
	if h.coverage != nil {
		if n, err := h.coverage.Count(r.Context()); err == nil {
			out["coverageCells"] = n
		} else {
			h.logger.Warn("coverage count failed", zap.Error(err))
		}
	}
	if h.baselines != nil {
		if n, err := h.baselines.Count(r.Context()); err == nil {
			out["baselines"] = n
		} else {
			h.logger.Warn("baseline count failed", zap.Error(err))
		}
	}
	if h.findings != nil {
		if fs, err := h.findings.Stats(r.Context(), ceilingOf(r)); err == nil {
			out["findings"] = fs
		} else {
			h.logger.Warn("finding stats failed", zap.Error(err))
		}
	}
	httputil.JSON(w, http.StatusOK, out)
}

func (h *AnalyticsHandler) handleBaseline(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !h.entityVisible(w, r, id) {
		return
	}
	b, err := h.baselines.Get(r.Context(), id)
	if err != nil {
		h.logger.Error("get baseline failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to get baseline")
		return
	}
	if b == nil {
		httputil.Error(w, http.StatusNotFound, "No baseline for this entity yet")
		return
	}
	if b.Places == nil {
		b.Places = []baseline.Place{}
	}
	httputil.JSON(w, http.StatusOK, b)
}

func parseFindingQuery(r *http.Request) store.Query {
	q := r.URL.Query()
	p := httputil.ParsePagination(r)
	out := store.Query{
		Status:   strings.TrimSpace(q.Get("status")),
		Severity: strings.TrimSpace(q.Get("severity")),
		EntityID: strings.TrimSpace(q.Get("entityId")),
		Ceiling:  ceilingOf(r),
		Page:     p.Page,
		PageSize: p.PageSize,
	}
	for _, raw := range q["kinds"] {
		for _, k := range strings.Split(raw, ",") {
			if k = strings.ToUpper(strings.TrimSpace(k)); k != "" {
				out.Kinds = append(out.Kinds, k)
			}
		}
	}
	if since := q.Get("since"); since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			out.Since = &t
		}
	}
	return out
}

func (h *AnalyticsHandler) list(w http.ResponseWriter, r *http.Request, q store.Query) {
	items, total, err := h.findings.List(r.Context(), q)
	if err != nil {
		h.logger.Error("list findings failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to list findings")
		return
	}
	httputil.JSON(w, http.StatusOK, httputil.PaginatedResponse{Data: items, Total: total, Page: q.Page, PageSize: q.PageSize})
}

func (h *AnalyticsHandler) handleListFindings(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, parseFindingQuery(r))
}

func (h *AnalyticsHandler) handleEntityFindings(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !h.entityVisible(w, r, id) {
		return
	}
	q := parseFindingQuery(r)
	q.EntityID = id
	h.list(w, r, q)
}

func (h *AnalyticsHandler) handleGetFinding(w http.ResponseWriter, r *http.Request) {
	f, ok := h.findingVisible(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	httputil.JSON(w, http.StatusOK, f)
}

func (h *AnalyticsHandler) handleReviewFinding(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := h.findingVisible(w, r, id); !ok {
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httputil.Error(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	status := strings.ToUpper(strings.TrimSpace(body.Status))
	if status != store.StatusConfirmed && status != store.StatusDismissed {
		httputil.Error(w, http.StatusBadRequest, "status must be CONFIRMED or DISMISSED")
		return
	}
	user := middleware.UserID(r.Context())
	if user == "" {
		user = "unknown"
	}
	f, err := h.findings.Review(r.Context(), id, status, user)
	if errors.Is(err, store.ErrBadTransition) {
		httputil.Error(w, http.StatusConflict, "Finding is already closed, dismissed or confirmed")
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		httputil.Error(w, http.StatusNotFound, "Finding not found")
		return
	}
	if err != nil {
		h.logger.Error("review finding failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to review finding")
		return
	}
	if status == store.StatusConfirmed && h.escalator != nil {
		h.escalator.Publish(r.Context(), f)
	}
	httputil.JSON(w, http.StatusOK, f)
}
