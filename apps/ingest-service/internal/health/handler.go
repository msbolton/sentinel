package health

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	kafkaproducer "github.com/sentinel/ingest-service/internal/kafka"
)

// Handler provides HTTP endpoints for health checks, readiness probes,
// and Prometheus metrics exposition.
type Handler struct {
	producer  *kafkaproducer.Producer
	logger    *zap.Logger
	startTime time.Time
}

// NewHandler creates a new health/metrics HTTP handler.
func NewHandler(producer *kafkaproducer.Producer, logger *zap.Logger) *Handler {
	return &Handler{
		producer:  producer,
		logger:    logger,
		startTime: time.Now(),
	}
}

// healthResponse is the JSON response for health and readiness endpoints.
type healthResponse struct {
	Status  string `json:"status"`
	Uptime  string `json:"uptime"`
	Service string `json:"service"`
}

// RegisterRoutes registers all HTTP routes on the provided ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/ready", h.handleReady)
	mux.Handle("/metrics", promhttp.Handler())
}

// handleHealth returns a simple health check response. This endpoint always
// returns 200 OK if the service process is running.
func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := healthResponse{
		Status:  "healthy",
		Uptime:  time.Since(h.startTime).Round(time.Second).String(),
		Service: "sentinel-ingest-service",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.logger.Error("failed to write health response", zap.Error(err))
	}
}

// handleReady returns a readiness check that verifies the Kafka producer
// is connected and operational. Returns 503 if Kafka is unreachable.
func (h *Handler) handleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.producer.IsHealthy() {
		resp := healthResponse{
			Status:  "not ready",
			Uptime:  time.Since(h.startTime).Round(time.Second).String(),
			Service: "sentinel-ingest-service",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			h.logger.Error("failed to write readiness response", zap.Error(err))
		}
		return
	}

	resp := healthResponse{
		Status:  "ready",
		Uptime:  time.Since(h.startTime).Round(time.Second).String(),
		Service: "sentinel-ingest-service",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.logger.Error("failed to write readiness response", zap.Error(err))
	}
}
