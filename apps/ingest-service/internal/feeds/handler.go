package feeds

import (
	"encoding/json"
	"net/http"
	"strings"

	"go.uber.org/zap"
)

// Handler provides HTTP endpoints for listing and toggling data feeds.
type Handler struct {
	manager *Manager
	logger  *zap.Logger
}

// NewHandler creates a new feeds HTTP handler.
func NewHandler(manager *Manager, logger *zap.Logger) *Handler {
	return &Handler{manager: manager, logger: logger}
}

// RegisterRoutes registers the feed endpoints on the provided ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/feeds", h.handleFeeds)
	mux.HandleFunc("/feeds/", h.handleFeedByID)
}

// toggleRequest is the expected JSON body for PUT /feeds/{id}.
type toggleRequest struct {
	Enabled bool `json:"enabled"`
}

// handleFeeds serves GET /feeds → list all feeds.
func (h *Handler) handleFeeds(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	feeds := h.manager.List()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(feeds); err != nil {
		h.logger.Error("failed to encode feeds response", zap.Error(err))
	}
}

// handleFeedByID serves PUT /feeds/{id} → toggle a feed.
func (h *Handler) handleFeedByID(w http.ResponseWriter, r *http.Request) {
	// Extract the feed ID from the URL path: /feeds/{id}
	id := strings.TrimPrefix(r.URL.Path, "/feeds/")
	if id == "" {
		http.Error(w, "missing feed id", http.StatusBadRequest)
		return
	}

	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req toggleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	status, err := h.manager.SetEnabled(id, req.Enabled)
	if err != nil {
		if strings.Contains(err.Error(), "unknown feed") {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		h.logger.Error("failed to toggle feed", zap.String("id", id), zap.Error(err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		h.logger.Error("failed to encode feed status", zap.Error(err))
	}
}
