package health

import (
	"encoding/json"
	"net/http"
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
}

// NewHandler creates a new health handler for the named service.
func NewHandler(serviceName string, logger *zap.Logger, checkers ...Checker) *Handler {
	return &Handler{
		serviceName: serviceName,
		logger:      logger,
		startTime:   time.Now(),
		checkers:    checkers,
	}
}

type healthResponse struct {
	Status  string `json:"status"`
	Uptime  string `json:"uptime"`
	Service string `json:"service"`
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	for _, check := range h.checkers {
		if err := check(); err != nil {
			resp := healthResponse{
				Status:  "not ready",
				Uptime:  time.Since(h.startTime).Round(time.Second).String(),
				Service: h.serviceName,
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(resp)
			return
		}
	}

	resp := healthResponse{
		Status:  "ready",
		Uptime:  time.Since(h.startTime).Round(time.Second).String(),
		Service: h.serviceName,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
