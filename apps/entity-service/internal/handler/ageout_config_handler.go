package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/sentinel/go-common/httputil"
	"github.com/sentinel/entity-service/internal/store"
	"go.uber.org/zap"
)

// AgeoutConfigHandler handles ageout config HTTP endpoints.
type AgeoutConfigHandler struct {
	store  *store.AgeoutConfigStore
	logger *zap.Logger
}

// NewAgeoutConfigHandler creates a new ageout config handler.
func NewAgeoutConfigHandler(store *store.AgeoutConfigStore, logger *zap.Logger) *AgeoutConfigHandler {
	return &AgeoutConfigHandler{store: store, logger: logger}
}

// RegisterRoutes registers ageout config endpoints on the mux.
func (h *AgeoutConfigHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/entities/ageout-config", h.handleFindAll)
	mux.HandleFunc("GET /api/v1/entities/ageout-config/{sourceType}", h.handleFindBySourceType)
	mux.HandleFunc("PUT /api/v1/entities/ageout-config", h.handleUpsert)
	mux.HandleFunc("DELETE /api/v1/entities/ageout-config/{id}", h.handleDelete)
}

func (h *AgeoutConfigHandler) handleFindAll(w http.ResponseWriter, r *http.Request) {
	configs, err := h.store.FindAll(r.Context())
	if err != nil {
		h.logger.Error("find all ageout configs failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to get ageout configs")
		return
	}
	httputil.JSON(w, http.StatusOK, configs)
}

func (h *AgeoutConfigHandler) handleFindBySourceType(w http.ResponseWriter, r *http.Request) {
	sourceType := r.PathValue("sourceType")

	var feedID *string
	if v := r.URL.Query().Get("feedId"); v != "" {
		feedID = &v
	}

	config, err := h.store.FindBySourceType(r.Context(), sourceType, feedID)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			httputil.Error(w, http.StatusNotFound, "Config not found")
			return
		}
		h.logger.Error("find ageout config failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to get ageout config")
		return
	}
	httputil.JSON(w, http.StatusOK, config)
}

type upsertConfigRequest struct {
	FeedID            *string `json:"feedId"`
	SourceType        *string `json:"sourceType"`
	StaleThresholdMs  int     `json:"staleThresholdMs"`
	AgeoutThresholdMs int     `json:"ageoutThresholdMs"`
}

func (h *AgeoutConfigHandler) handleUpsert(w http.ResponseWriter, r *http.Request) {
	var req upsertConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.Error(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.StaleThresholdMs < 1000 {
		httputil.Error(w, http.StatusBadRequest, "staleThresholdMs must be at least 1000")
		return
	}
	if req.AgeoutThresholdMs <= req.StaleThresholdMs {
		httputil.Error(w, http.StatusBadRequest, "ageoutThresholdMs must be greater than staleThresholdMs")
		return
	}

	config, err := h.store.Upsert(r.Context(), req.FeedID, req.SourceType, req.StaleThresholdMs, req.AgeoutThresholdMs)
	if err != nil {
		h.logger.Error("upsert ageout config failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to upsert ageout config")
		return
	}
	httputil.JSON(w, http.StatusOK, config)
}

func (h *AgeoutConfigHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.Delete(r.Context(), id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			httputil.Error(w, http.StatusNotFound, "Config not found")
			return
		}
		h.logger.Error("delete ageout config failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to delete ageout config")
		return
	}
	httputil.NoContent(w)
}
