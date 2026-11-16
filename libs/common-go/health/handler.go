package health

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

// Checker is a function that returns an error if the dependency is unhealthy.
type Checker func() error

// Handler provides HTTP endpoints for health checks, readiness probes,
// and Prometheus metrics.
type Handler struct {
	serviceName string
	logger      *zap.Logger
	startTime   time.Time
	checkers    []Checker
	names       []string
}

// NewHandler creates a new health handler for the named service. Checkers
// gate readiness; /health stays 200 but reports the failing ones, so a
// dependency outage is visible without the liveness probe restarting the
// process. Use AddCheck to name a checker; unnamed ones are numbered.
func NewHandler(serviceName string, logger *zap.Logger, checkers ...Checker) *Handler {
	h := &Handler{
		serviceName: serviceName,
		logger:      logger,
		startTime:   time.Now(),
	}
	for i, c := range checkers {
		h.AddCheck("check"+strconv.Itoa(i), c)
	}
	return h
}

// AddCheck registers a named readiness checker.
func (h *Handler) AddCheck(name string, check Checker) {
	h.checkers = append(h.checkers, check)
	h.names = append(h.names, name)
}

type healthResponse struct {
	Status  string            `json:"status"`
	Uptime  string            `json:"uptime"`
	Service string            `json:"service"`
	Checks  map[string]string `json:"checks,omitempty"`
}

// failures runs every checker and returns the failed ones by name.
func (h *Handler) failures() map[string]string {
	var failed map[string]string
	for i, check := range h.checkers {
		if err := check(); err != nil {
			if failed == nil {
				failed = map[string]string{}
			}
			failed[h.names[i]] = err.Error()
		}
	}
	return failed
}

// RegisterRoutes registers health, readiness, and metrics routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/ready", h.handleReady)
	mux.Handle("/metrics", promhttp.Handler())
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := healthResponse{
		Status:  "healthy",
		Uptime:  time.Since(h.startTime).Round(time.Second).String(),
		Service: h.serviceName,
	}
	if failed := h.failures(); len(failed) > 0 {
		resp.Status = "degraded"
		resp.Checks = failed
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if failed := h.failures(); len(failed) > 0 {
		resp := healthResponse{
			Status:  "not ready",
			Uptime:  time.Since(h.startTime).Round(time.Second).String(),
			Service: h.serviceName,
			Checks:  failed,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(resp)
		return
	}

	resp := healthResponse{
		Status:  "ready",
		Uptime:  time.Since(h.startTime).Round(time.Second).String(),
		Service: h.serviceName,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
