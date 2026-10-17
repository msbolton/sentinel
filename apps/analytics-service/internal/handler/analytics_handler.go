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
)

// Stats supplies the counters the stats endpoint reports.
type Stats interface {
	Snapshot() map[string]any
}

// CoverageCounter supplies the coverage row count for the stats endpoint.
type CoverageCounter interface {
	Count(ctx context.Context) (int64, error)
}

// AnalyticsHandler holds the collaborators of the API.
type AnalyticsHandler struct {
	stats    Stats
	coverage CoverageCounter
	logger   *zap.Logger
}

// NewAnalyticsHandler creates the handler.
func NewAnalyticsHandler(stats Stats, coverage CoverageCounter, logger *zap.Logger) *AnalyticsHandler {
	return &AnalyticsHandler{stats: stats, coverage: coverage, logger: logger}
}

// RegisterRoutes registers the analytics endpoints on the mux.
func (h *AnalyticsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/analytics/stats", h.handleStats)
}

// ceilingOf is the caller's clearance as propagated by the gateway; a
// missing header fails closed to UNCLASSIFIED.
func ceilingOf(r *http.Request) string {
	return classification.Normalize(middleware.Classification(r.Context()))
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
	httputil.JSON(w, http.StatusOK, out)
}
