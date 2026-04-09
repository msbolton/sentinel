package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/sentinel/go-common/httputil"
	"github.com/sentinel/link-analysis-service/internal/store"
	"go.uber.org/zap"
)

// LinkHandler handles link HTTP endpoints.
type LinkHandler struct {
	store  *store.LinkStore
	logger *zap.Logger
}

// NewLinkHandler creates a new link handler.
func NewLinkHandler(store *store.LinkStore, logger *zap.Logger) *LinkHandler {
	return &LinkHandler{store: store, logger: logger}
}

// RegisterRoutes registers link endpoints on the mux.
func (h *LinkHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/links", h.handleGetLinks)
	mux.HandleFunc("GET /api/v1/links/graph", h.handleGetGraph)
	mux.HandleFunc("GET /api/v1/links/shortest-path", h.handleShortestPath)
	mux.HandleFunc("GET /api/v1/links/communities", h.handleCommunities)
	mux.HandleFunc("POST /api/v1/links", h.handleCreateLink)
	mux.HandleFunc("DELETE /api/v1/links/{id}", h.handleDeleteLink)
}

func (h *LinkHandler) handleGetLinks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	entityID := q.Get("entityId")
	if entityID == "" {
		httputil.Error(w, http.StatusBadRequest, "entityId is required")
		return
	}

	var linkTypes []string
	if types := q.Get("types"); types != "" {
		linkTypes = strings.Split(types, ",")
	}
	if typesArr, ok := q["types"]; ok && len(typesArr) > 1 {
		linkTypes = typesArr
	}

	var minConfidence *float64
	if mc := q.Get("minConfidence"); mc != "" {
		v, err := strconv.ParseFloat(mc, 64)
		if err != nil || v < 0 || v > 1 {
			httputil.Error(w, http.StatusBadRequest, "minConfidence must be between 0 and 1")
			return
		}
		minConfidence = &v
	}

	links, err := h.store.GetLinks(r.Context(), entityID, linkTypes, minConfidence)
	if err != nil {
		h.logger.Error("get links failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to get links")
		return
	}

	httputil.JSON(w, http.StatusOK, links)
}

func (h *LinkHandler) handleGetGraph(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	centerID := q.Get("centerId")
	if centerID == "" {
		httputil.Error(w, http.StatusBadRequest, "centerId is required")
		return
	}

	maxDepth := 3
	if md := q.Get("maxDepth"); md != "" {
		v, err := strconv.Atoi(md)
		if err != nil || v < 1 || v > 10 {
			httputil.Error(w, http.StatusBadRequest, "maxDepth must be between 1 and 10")
			return
		}
		maxDepth = v
	}

	var linkTypes []string
	if types := q.Get("types"); types != "" {
		linkTypes = strings.Split(types, ",")
	}

	var minConfidence *float64
	if mc := q.Get("minConfidence"); mc != "" {
		v, err := strconv.ParseFloat(mc, 64)
		if err != nil || v < 0 || v > 1 {
			httputil.Error(w, http.StatusBadRequest, "minConfidence must be between 0 and 1")
			return
		}
		minConfidence = &v
	}

	result, err := h.store.GetGraph(r.Context(), centerID, maxDepth, linkTypes, minConfidence)
	if err != nil {
		h.logger.Error("get graph failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to get graph")
		return
	}

	httputil.JSON(w, http.StatusOK, result)
}

func (h *LinkHandler) handleShortestPath(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	fromID := q.Get("from")
	toID := q.Get("to")
	if fromID == "" || toID == "" {
		httputil.Error(w, http.StatusBadRequest, "from and to are required")
		return
	}

	result, err := h.store.FindShortestPath(r.Context(), fromID, toID)
	if err != nil {
		h.logger.Error("shortest path failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to find shortest path")
		return
	}

	httputil.JSON(w, http.StatusOK, result)
}

func (h *LinkHandler) handleCommunities(w http.ResponseWriter, r *http.Request) {
	// Community detection is a placeholder — the NestJS version uses AGE label propagation.
	// For the relational fallback, we return an empty result.
	httputil.JSON(w, http.StatusOK, []interface{}{})
}

type createLinkRequest struct {
	SourceEntityID string            `json:"sourceEntityId"`
	TargetEntityID string            `json:"targetEntityId"`
	LinkType       string            `json:"linkType"`
	Confidence     *float64          `json:"confidence"`
	Description    *string           `json:"description"`
	Evidence       []string          `json:"evidence"`
	FirstObserved  *string           `json:"firstObserved"`
	LastObserved   *string           `json:"lastObserved"`
	Metadata       map[string]string `json:"metadata"`
}

func (h *LinkHandler) handleCreateLink(w http.ResponseWriter, r *http.Request) {
	var req createLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.Error(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.SourceEntityID == "" || req.TargetEntityID == "" || req.LinkType == "" {
		httputil.Error(w, http.StatusBadRequest, "sourceEntityId, targetEntityId, and linkType are required")
		return
	}

	confidence := 0.5
	if req.Confidence != nil {
		if *req.Confidence < 0 || *req.Confidence > 1 {
			httputil.Error(w, http.StatusBadRequest, "confidence must be between 0 and 1")
			return
		}
		confidence = *req.Confidence
	}

	link, err := h.store.Create(r.Context(), store.CreateLinkParams{
		SourceEntityID: req.SourceEntityID,
		TargetEntityID: req.TargetEntityID,
		LinkType:       req.LinkType,
		Confidence:     confidence,
		Description:    req.Description,
		Evidence:       req.Evidence,
		FirstObserved:  req.FirstObserved,
		LastObserved:   req.LastObserved,
		Metadata:       req.Metadata,
	})
	if err != nil {
		h.logger.Error("create link failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to create link")
		return
	}

	httputil.JSON(w, http.StatusCreated, link)
}

func (h *LinkHandler) handleDeleteLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.Error(w, http.StatusBadRequest, "id is required")
		return
	}

	if err := h.store.Delete(r.Context(), id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			httputil.Error(w, http.StatusNotFound, "Link not found")
			return
		}
		h.logger.Error("delete link failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to delete link")
		return
	}

	httputil.NoContent(w)
}
