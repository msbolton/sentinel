// Package handler serves the analytics REST API under /api/v1/analytics.
// Every read applies the caller's clearance as a ceiling.
package handler

import (
	"context"
	"net/http"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/classification"
	"github.com/sentinel/go-common/httputil"
	"github.com/sentinel/go-common/middleware"

	"github.com/sentinel/analytics-service/internal/baseline"
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

// AnalyticsHandler holds the collaborators of the API.
type AnalyticsHandler struct {
	stats     Stats
	coverage  CoverageCounter
	baselines Baselines
	entities  Entities
	logger    *zap.Logger
}

// NewAnalyticsHandler creates the handler.
func NewAnalyticsHandler(stats Stats, coverage CoverageCounter, baselines Baselines, entities Entities, logger *zap.Logger) *AnalyticsHandler {
	return &AnalyticsHandler{stats: stats, coverage: coverage, baselines: baselines, entities: entities, logger: logger}
}

// RegisterRoutes registers the analytics endpoints on the mux.
func (h *AnalyticsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/analytics/stats", h.handleStats)
	mux.HandleFunc("GET /api/v1/analytics/entities/{id}/baseline", h.handleBaseline)
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
