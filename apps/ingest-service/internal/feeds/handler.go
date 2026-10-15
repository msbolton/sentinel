package feeds

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/sources"
)

// maxViewportBoxes bounds a viewport push; the gateway sends at most two
// (one split at the antimeridian).
const maxViewportBoxes = 4

// Handler provides HTTP endpoints for listing, creating, and managing data feeds.
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

// createFeedRequest is the expected JSON body for POST /feeds.
type createFeedRequest struct {
	Name          string          `json:"name"`
	ConnectorType string          `json:"connector_type"`
	Format        string          `json:"format"`
	Config        json.RawMessage `json:"config"`
}

// updateFeedRequest is the expected JSON body for PATCH /feeds/{id}.
type updateFeedRequest struct {
	Name   string          `json:"name,omitempty"`
	Format string          `json:"format,omitempty"`
	Config json.RawMessage `json:"config,omitempty"`
}

var validConnectorTypes = map[string]bool{"mqtt": true, "stomp": true, "tcp": true}
var validFormats = map[string]bool{"json": true, "nmea": true, "cot": true, "ais": true, "adsb": true, "link16": true}

// handleFeeds serves GET /feeds and POST /feeds.
func (h *Handler) handleFeeds(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listFeeds(w, r)
	case http.MethodPost:
		h.createFeed(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// listFeeds returns all feeds with health status.
func (h *Handler) listFeeds(w http.ResponseWriter, _ *http.Request) {
	feeds := h.manager.ListWithHealth()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(feeds); err != nil {
		h.logger.Error("failed to encode feeds response", zap.Error(err))
	}
}

// createFeed handles POST /feeds to create a new custom feed.
func (h *Handler) createFeed(w http.ResponseWriter, r *http.Request) {
	var req createFeedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if !validConnectorTypes[req.ConnectorType] {
		http.Error(w, "connector_type must be mqtt, stomp, or tcp", http.StatusBadRequest)
		return
	}
	if !validFormats[req.Format] {
		http.Error(w, "format must be json, nmea, cot, ais, adsb, or link16", http.StatusBadRequest)
		return
	}

	feed, err := h.manager.CreateCustomFeed(r.Context(), req.Name, req.ConnectorType, req.Format, req.Config)
	if err != nil {
		h.logger.Error("failed to create custom feed", zap.Error(err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(feed); err != nil {
		h.logger.Error("failed to encode feed response", zap.Error(err))
	}
}

// viewportRequest is the JSON body for PUT /feeds/{id}/viewport.
type viewportRequest struct {
	Active  bool           `json:"active"`
	Clients int            `json:"clients"`
	Boxes   []sources.BBox `json:"boxes"`
}

// viewportResponse is the JSON body returned by GET and PUT /feeds/{id}/viewport.
type viewportResponse struct {
	Feed                 string         `json:"feed"`
	Enabled              bool           `json:"enabled"`
	Active               bool           `json:"active"`
	Clients              int            `json:"clients"`
	Boxes                []sources.BBox `json:"boxes"`
	AreaSqDeg            float64        `json:"areaSqDeg"`
	EffectiveIntervalSec int            `json:"effectiveIntervalSec"`
	Idle                 bool           `json:"idle"`
	CreditsRemaining     *int           `json:"creditsRemaining"` // OpenSky daily credits left; null when unknown
	LowCredits           bool           `json:"lowCredits"`
	LastPollAt           *time.Time     `json:"lastPollAt"`
	NextPollInSec        int            `json:"nextPollInSec"`
	ExpiresAt            *time.Time     `json:"expiresAt"`
}

// handleFeedByID serves PUT, DELETE, PATCH /feeds/{id} and GET, PUT /feeds/{id}/viewport.
func (h *Handler) handleFeedByID(w http.ResponseWriter, r *http.Request) {
	id, sub, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/feeds/"), "/")
	if id == "" {
		http.Error(w, "missing feed id", http.StatusBadRequest)
		return
	}
	if sub == "viewport" {
		h.handleViewport(w, r, id)
		return
	}
	if sub != "" {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.toggleFeed(w, r, id)
	case http.MethodDelete:
		h.deleteFeed(w, r, id)
	case http.MethodPatch:
		h.updateFeed(w, r, id)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// toggleFeed handles PUT /feeds/{id} to enable/disable a feed.
func (h *Handler) toggleFeed(w http.ResponseWriter, r *http.Request, id string) {
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

// deleteFeed handles DELETE /feeds/{id} to remove a custom feed.
func (h *Handler) deleteFeed(w http.ResponseWriter, r *http.Request, id string) {
	err := h.manager.DeleteCustomFeed(r.Context(), id)
	if err != nil {
		if strings.Contains(err.Error(), "unknown feed") {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if strings.Contains(err.Error(), "cannot delete built-in") {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		h.logger.Error("failed to delete feed", zap.String("id", id), zap.Error(err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// updateFeed handles PATCH /feeds/{id} to update a custom feed's config.
func (h *Handler) updateFeed(w http.ResponseWriter, r *http.Request, id string) {
	var req updateFeedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Format != "" && !validFormats[req.Format] {
		http.Error(w, "format must be json, nmea, cot, ais, adsb, or link16", http.StatusBadRequest)
		return
	}

	feed, err := h.manager.UpdateCustomFeed(r.Context(), id, req.Name, req.Format, req.Config)
	if err != nil {
		if strings.Contains(err.Error(), "unknown feed") {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if strings.Contains(err.Error(), "cannot update built-in") {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		h.logger.Error("failed to update feed", zap.String("id", id), zap.Error(err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(feed); err != nil {
		h.logger.Error("failed to encode feed response", zap.Error(err))
	}
}

// handleViewport serves GET and PUT /feeds/{id}/viewport.
func (h *Handler) handleViewport(w http.ResponseWriter, r *http.Request, id string) {
	var (
		state ViewportState
		err   error
	)
	switch r.Method {
	case http.MethodGet:
		state, err = h.manager.GetViewport(id)
	case http.MethodPut:
		var req viewportRequest
		if decodeErr := json.NewDecoder(r.Body).Decode(&req); decodeErr != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if msg := validateViewport(req); msg != "" {
			http.Error(w, msg, http.StatusBadRequest)
			return
		}
		state, err = h.manager.SetViewport(id, sources.Viewport{Active: req.Active, Clients: req.Clients, Boxes: req.Boxes})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrUnknownFeed):
			http.Error(w, err.Error(), http.StatusNotFound)
		case errors.Is(err, ErrViewportUnsupported):
			http.Error(w, err.Error(), http.StatusConflict)
		default:
			h.logger.Error("viewport request failed", zap.String("id", id), zap.Error(err))
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(toViewportResponse(state)); err != nil {
		h.logger.Error("failed to encode viewport response", zap.Error(err))
	}
}

// validateViewport returns a problem description or "" when the request is valid.
func validateViewport(req viewportRequest) string {
	if len(req.Boxes) > maxViewportBoxes {
		return fmt.Sprintf("at most %d boxes allowed", maxViewportBoxes)
	}
	if req.Active && len(req.Boxes) == 0 {
		return "an active viewport needs at least one box"
	}
	if req.Clients < 0 {
		return "clients must not be negative"
	}
	for i, b := range req.Boxes {
		if err := b.Validate(); err != nil {
			return fmt.Sprintf("box %d: %v", i, err)
		}
	}
	return ""
}

func toViewportResponse(state ViewportState) viewportResponse {
	st := state.Status
	resp := viewportResponse{
		Feed:                 state.Feed,
		Enabled:              state.Enabled,
		Active:               st.Active,
		Clients:              st.Clients,
		Boxes:                st.Boxes,
		AreaSqDeg:            st.AreaSqDeg,
		EffectiveIntervalSec: int(st.EffectiveInterval / time.Second),
		Idle:                 st.Idle,
		LowCredits:           st.LowCredits,
		NextPollInSec:        int((st.NextPollIn + time.Second - 1) / time.Second),
	}
	if st.CreditsRemaining >= 0 {
		c := st.CreditsRemaining
		resp.CreditsRemaining = &c
	}
	if resp.Boxes == nil {
		resp.Boxes = []sources.BBox{}
	}
	if !st.LastPollAt.IsZero() {
		t := st.LastPollAt.UTC()
		resp.LastPollAt = &t
	}
	if !st.ExpiresAt.IsZero() {
		t := st.ExpiresAt.UTC()
		resp.ExpiresAt = &t
	}
	return resp
}
