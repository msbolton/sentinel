package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/sentinel/go-common/classification"
	"github.com/sentinel/go-common/httputil"
	"github.com/sentinel/go-common/middleware"
	"github.com/sentinel/link-analysis-service/internal/store"
	"go.uber.org/zap"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// linkTypePattern accepts the sentinel.v1.LinkType names (COMMUNICATION,
// COMMAND_CONTROL, ...) without the service depending on the proto package.
var linkTypePattern = regexp.MustCompile(`^[A-Z][A-Z_]{0,49}$`)

// levelsOf lists the classification levels the caller may see, from the
// clearance the gateway propagated.
func levelsOf(r *http.Request) []string {
	return classification.AtOrBelow(classification.Normalize(middleware.Classification(r.Context())))
}

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
	if !uuidPattern.MatchString(entityID) {
		httputil.Error(w, http.StatusBadRequest, "entityId must be an entity UUID")
		return
	}

	f, err := parseFilter(r)
	if err != nil {
		httputil.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	links, err := h.store.GetLinks(r.Context(), entityID, f)
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
	if !uuidPattern.MatchString(centerID) {
		httputil.Error(w, http.StatusBadRequest, "centerId must be an entity UUID")
		return
	}

	maxDepth, err := parseMaxDepth(q)
	if err != nil {
		httputil.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	f, err := parseFilter(r)
	if err != nil {
		httputil.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := h.store.GetGraph(r.Context(), centerID, maxDepth, f)
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
	if !uuidPattern.MatchString(fromID) || !uuidPattern.MatchString(toID) {
		httputil.Error(w, http.StatusBadRequest, "from and to must be entity UUIDs")
		return
	}

	result, err := h.store.FindShortestPath(r.Context(), fromID, toID, levelsOf(r))
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

	if !uuidPattern.MatchString(req.SourceEntityID) || !uuidPattern.MatchString(req.TargetEntityID) {
		httputil.Error(w, http.StatusBadRequest, "sourceEntityId and targetEntityId must be entity UUIDs")
		return
	}
	if strings.EqualFold(req.SourceEntityID, req.TargetEntityID) {
		httputil.Error(w, http.StatusBadRequest, "an entity cannot be linked to itself")
		return
	}
	if !linkTypePattern.MatchString(req.LinkType) {
		httputil.Error(w, http.StatusBadRequest, "linkType must be a LinkType name such as ASSOCIATION")
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
	}, levelsOf(r))
	if errors.Is(err, store.ErrNotFound) {
		httputil.Error(w, http.StatusNotFound, "Entity not found")
		return
	}
	if err != nil {
		h.logger.Error("create link failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to create link")
		return
	}

	httputil.JSON(w, http.StatusCreated, link)
}

func (h *LinkHandler) handleDeleteLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		httputil.Error(w, http.StatusBadRequest, "id must be a link UUID")
		return
	}

	if err := h.store.Delete(r.Context(), id, levelsOf(r)); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httputil.Error(w, http.StatusNotFound, "Link not found")
			return
		}
		h.logger.Error("delete link failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to delete link")
		return
	}

	httputil.NoContent(w)
}

// parseFilter reads the type and confidence filters and the caller's clearance.
func parseFilter(r *http.Request) (store.Filter, error) {
	minConfidence, err := parseMinConfidence(r.URL.Query())
	if err != nil {
		return store.Filter{}, err
	}
	return store.Filter{
		LinkTypes:     parseLinkTypes(r.URL.Query()),
		MinConfidence: minConfidence,
		Levels:        levelsOf(r),
	}, nil
}

// parseLinkTypes accepts repeated ?types=A&types=B or comma-separated ?types=A,B.
func parseLinkTypes(q url.Values) []string {
	var out []string
	for _, v := range q["types"] {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// parseMinConfidence reads an optional minConfidence in [0, 1].
func parseMinConfidence(q url.Values) (*float64, error) {
	mc := q.Get("minConfidence")
	if mc == "" {
		return nil, nil
	}
	v, err := strconv.ParseFloat(mc, 64)
	if err != nil || v < 0 || v > 1 {
		return nil, errors.New("minConfidence must be between 0 and 1")
	}
	return &v, nil
}

// parseMaxDepth reads an optional maxDepth in [1, 10], defaulting to 3.
func parseMaxDepth(q url.Values) (int, error) {
	md := q.Get("maxDepth")
	if md == "" {
		return 3, nil
	}
	v, err := strconv.Atoi(md)
	if err != nil || v < 1 || v > 10 {
		return 0, errors.New("maxDepth must be between 1 and 10")
	}
	return v, nil
}
