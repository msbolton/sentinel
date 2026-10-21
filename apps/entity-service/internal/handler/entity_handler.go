package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/classification"
	"github.com/sentinel/go-common/httputil"
	"github.com/sentinel/go-common/ids"
	"github.com/sentinel/go-common/middleware"

	"github.com/sentinel/entity-service/internal/events"
	"github.com/sentinel/entity-service/internal/store"
)

// CacheInvalidator drops cached identity resolutions; the fusion resolver
// implements it.
type CacheInvalidator interface {
	Invalidate()
}

// EntityHandler handles entity HTTP endpoints.
type EntityHandler struct {
	store        *store.EntityStore
	observations *store.ObservationStore
	events       *events.Publisher
	resolver     CacheInvalidator
	logger       *zap.Logger
}

// NewEntityHandler creates a new entity handler. resolver is invalidated
// after a delete so a deleted entity's identities can bind to a new one.
func NewEntityHandler(store *store.EntityStore, observations *store.ObservationStore, publisher *events.Publisher, resolver CacheInvalidator, logger *zap.Logger) *EntityHandler {
	return &EntityHandler{store: store, observations: observations, events: publisher, resolver: resolver, logger: logger}
}

func (h *EntityHandler) invalidateResolver() {
	if h.resolver != nil {
		h.resolver.Invalidate()
	}
}

// RegisterRoutes registers entity endpoints on the mux.
func (h *EntityHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/entities", h.handleQuery)
	mux.HandleFunc("GET /api/v1/entities/nearby", h.handleNearby)
	mux.HandleFunc("GET /api/v1/entities/counts", h.handleCounts)
	mux.HandleFunc("GET /api/v1/entities/{id}", h.handleGetByID)
	// Registered under a literal segment: "{id}/observations" would be
	// ambiguous with "ageout-config/{sourceType}" in the Go mux.
	mux.HandleFunc("GET /api/v1/entities/observations/{id}", h.handleObservations)
	mux.HandleFunc("POST /api/v1/entities", h.handleCreate)
	mux.HandleFunc("PATCH /api/v1/entities/{id}", h.handleUpdate)
	mux.HandleFunc("PATCH /api/v1/entities/{id}/position", h.handleUpdatePosition)
	mux.HandleFunc("DELETE /api/v1/entities/{id}", h.handleDelete)
	mux.HandleFunc("DELETE /api/v1/entities", h.handleDeleteAll)
}

func (h *EntityHandler) handleQuery(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	params := store.QueryParams{
		Page:     parseIntDefault(q.Get("page"), 1),
		PageSize: parseIntDefault(q.Get("pageSize"), 100),
	}

	if v := q.Get("north"); v != "" {
		f := parseFloat(v)
		params.North = &f
	}
	if v := q.Get("south"); v != "" {
		f := parseFloat(v)
		params.South = &f
	}
	if v := q.Get("east"); v != "" {
		f := parseFloat(v)
		params.East = &f
	}
	if v := q.Get("west"); v != "" {
		f := parseFloat(v)
		params.West = &f
	}

	if types := q["types"]; len(types) > 0 {
		for _, t := range types {
			for _, part := range strings.Split(t, ",") {
				trimmed := strings.TrimSpace(part)
				if trimmed != "" {
					params.Types = append(params.Types, trimmed)
				}
			}
		}
	}

	if sources := q["sources"]; len(sources) > 0 {
		for _, s := range sources {
			for _, part := range strings.Split(s, ",") {
				trimmed := strings.TrimSpace(part)
				if trimmed != "" {
					params.Sources = append(params.Sources, trimmed)
				}
			}
		}
	}

	if feeds := q["feedIds"]; len(feeds) > 0 {
		for _, f := range feeds {
			for _, part := range strings.Split(f, ",") {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					params.FeedIDs = append(params.FeedIDs, trimmed)
				}
			}
		}
	}

	if v := q.Get("classification"); v != "" {
		params.Classification = &v
	}
	params.Ceiling = ceilingOf(r)

	result, err := h.store.FindWithinBoundingBox(r.Context(), params)
	if err != nil {
		h.logger.Error("query entities failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to query entities")
		return
	}

	httputil.JSON(w, http.StatusOK, result)
}

func (h *EntityHandler) handleNearby(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	latStr := q.Get("lat")
	lngStr := q.Get("lng")
	if latStr == "" || lngStr == "" {
		httputil.Error(w, http.StatusBadRequest, "lat and lng are required")
		return
	}

	lat, _ := strconv.ParseFloat(latStr, 64)
	lng, _ := strconv.ParseFloat(lngStr, 64)

	radius := 10000.0
	if v := q.Get("radius"); v != "" {
		r, err := strconv.ParseFloat(v, 64)
		if err == nil && r >= 1 && r <= 500000 {
			radius = r
		}
	}

	results, err := h.store.FindNearby(r.Context(), lat, lng, radius, ceilingOf(r))
	if err != nil {
		h.logger.Error("nearby query failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to find nearby entities")
		return
	}

	httputil.JSON(w, http.StatusOK, results)
}

func (h *EntityHandler) handleCounts(w http.ResponseWriter, r *http.Request) {
	counts, err := h.store.GetEntityCounts(r.Context(), ceilingOf(r))
	if err != nil {
		h.logger.Error("get counts failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to get entity counts")
		return
	}

	httputil.JSON(w, http.StatusOK, counts)
}

// ceilingOf is the caller's clearance as propagated by the gateway; a
// missing header fails closed to UNCLASSIFIED.
func ceilingOf(r *http.Request) string {
	return classification.Normalize(middleware.Classification(r.Context()))
}

// findVisible fetches an entity the caller is cleared to see. Entities above
// the caller's clearance do not exist for them, so both cases are "not found".
func (h *EntityHandler) findVisible(w http.ResponseWriter, r *http.Request, id string) (*store.EntityRecord, bool) {
	entity, err := h.store.FindByID(r.Context(), id)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			httputil.Error(w, http.StatusNotFound, "Entity not found")
			return nil, false
		}
		h.logger.Error("get entity failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to get entity")
		return nil, false
	}
	if classification.Rank(entity.Classification) > classification.Rank(ceilingOf(r)) {
		httputil.Error(w, http.StatusNotFound, "Entity not found")
		return nil, false
	}
	return entity, true
}

func (h *EntityHandler) handleGetByID(w http.ResponseWriter, r *http.Request) {
	entity, ok := h.findVisible(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	httputil.JSON(w, http.StatusOK, entity)
}

func (h *EntityHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createEntityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.Error(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.EntityType == "" || req.Name == "" || req.Source == "" {
		httputil.Error(w, http.StatusBadRequest, "entityType, name, and source are required")
		return
	}

	classification := "UNCLASSIFIED"
	if req.Classification != nil {
		classification = *req.Classification
	}

	feedID := req.FeedID
	if feedID == nil || *feedID == "" {
		manual := ids.FeedIDManual
		feedID = &manual
	}

	rec := &store.EntityRecord{
		EntityType:       req.EntityType,
		Name:             req.Name,
		Description:      req.Description,
		Source:           req.Source,
		Classification:   classification,
		FeedID:           feedID,
		Heading:          req.Heading,
		SpeedKnots:       req.SpeedKnots,
		Course:           req.Course,
		Altitude:         req.Altitude,
		MilStd2525dSymbol: req.MilStd2525dSymbol,
		Metadata:         req.Metadata,
		Affiliations:     req.Affiliations,
		TrackEnvironment: req.TrackEnvironment,
		CountryOfOrigin:  req.CountryOfOrigin,
		SourceEntityID:   req.SourceEntityID,
		CircularError:    req.CircularError,
		DimensionLength:  req.DimensionLength,
		DimensionWidth:   req.DimensionWidth,
	}

	if req.Position != nil {
		rec.Position = &store.GeoPoint{Lat: req.Position.Lat, Lon: req.Position.Lng}
	}

	if rec.Metadata == nil {
		rec.Metadata = map[string]interface{}{}
	}
	if rec.Affiliations == nil {
		rec.Affiliations = []string{}
	}

	entity, err := h.store.Create(r.Context(), rec)
	if err != nil {
		h.logger.Error("create entity failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to create entity")
		return
	}

	h.events.Created(events.EntityProto(entity, nil, nil), nil)

	httputil.JSON(w, http.StatusCreated, entity)
}

// updateEntityRequest carries the operator-owned fields. Feeds never write
// these, so an operator's edit sticks until the next operator edit; the one
// exception is classification, which a feed configured at a higher level
// raises again on its next report.
type updateEntityRequest struct {
	Name              *string  `json:"name"`
	Description       *string  `json:"description"`
	Classification    *string  `json:"classification"`
	Affiliations      []string `json:"affiliations"`
	MilStd2525dSymbol *string  `json:"milStd2525dSymbol"`
}

// validateUpdate normalises the request and lists the fields it changes.
func validateUpdate(req *updateEntityRequest) ([]string, string) {
	var changed []string
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, "name must not be empty"
		}
		req.Name = &name
		changed = append(changed, "name")
	}
	if req.Description != nil {
		changed = append(changed, "description")
	}
	if req.Classification != nil {
		level := strings.ToUpper(strings.TrimSpace(*req.Classification))
		if !store.ValidClassification(level) {
			return nil, "classification must be one of UNCLASSIFIED, CONFIDENTIAL, SECRET, TOP_SECRET"
		}
		req.Classification = &level
		changed = append(changed, "classification")
	}
	if req.Affiliations != nil {
		changed = append(changed, "affiliations")
	}
	if req.MilStd2525dSymbol != nil {
		changed = append(changed, "milStd2525dSymbol")
	}
	if len(changed) == 0 {
		return nil, "no updatable field given (name, description, classification, affiliations, milStd2525dSymbol)"
	}
	return changed, ""
}

func (h *EntityHandler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req updateEntityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.Error(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	changed, problem := validateUpdate(&req)
	if problem != "" {
		httputil.Error(w, http.StatusBadRequest, problem)
		return
	}

	// The caller's clearance is a ceiling: entities above it do not exist
	// for them, and they cannot mark anything above it.
	if req.Classification != nil && classification.Rank(*req.Classification) > classification.Rank(ceilingOf(r)) {
		httputil.Error(w, http.StatusForbidden, "classification exceeds your clearance")
		return
	}
	if _, ok := h.findVisible(w, r, id); !ok {
		return
	}

	entity, err := h.store.UpdateOperatorFields(r.Context(), id, store.OperatorUpdate{
		Name: req.Name, Description: req.Description, Classification: req.Classification,
		Affiliations: req.Affiliations, MilStd2525dSymbol: req.MilStd2525dSymbol,
	})
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			httputil.Error(w, http.StatusNotFound, "Entity not found")
			return
		}
		h.logger.Error("update entity failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to update entity")
		return
	}

	h.events.Updated(events.EntityProto(entity, nil, nil), changed)
	httputil.JSON(w, http.StatusOK, entity)
}

func (h *EntityHandler) handleUpdatePosition(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req updatePositionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.Error(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	entity, err := h.store.UpdatePosition(r.Context(), id, req.Lat, req.Lng, req.Heading, req.SpeedKnots, req.Course, req.Altitude)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			httputil.Error(w, http.StatusNotFound, "Entity not found")
			return
		}
		h.logger.Error("update position failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to update position")
		return
	}

	h.events.Position(events.EntityProto(entity, nil, nil), nil)

	httputil.JSON(w, http.StatusOK, entity)
}

func (h *EntityHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := h.store.SoftDelete(r.Context(), id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			httputil.Error(w, http.StatusNotFound, "Entity not found")
			return
		}
		h.logger.Error("delete entity failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to delete entity")
		return
	}

	h.invalidateResolver()
	h.events.Deleted(id, false, 0)

	httputil.NoContent(w)
}

func (h *EntityHandler) handleDeleteAll(w http.ResponseWriter, r *http.Request) {
	count, err := h.store.SoftDeleteAll(r.Context())
	if err != nil {
		h.logger.Error("delete all entities failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to delete all entities")
		return
	}

	h.invalidateResolver()
	h.events.Deleted("", true, int(count))

	httputil.JSON(w, http.StatusOK, map[string]int64{"deleted": count})
}

// handleObservations lists an entity's stored reports, newest first,
// including the ones that did not move it (duplicates, stale fixes).
func (h *EntityHandler) handleObservations(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := h.findVisible(w, r, id); !ok {
		return
	}
	limit := parseIntDefault(r.URL.Query().Get("limit"), 100)
	rows, err := h.observations.ListByEntity(r.Context(), id, limit)
	if err != nil {
		h.logger.Error("list observations failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to list observations")
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]interface{}{"data": rows, "count": len(rows)})
}

type geoPointDTO struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type createEntityRequest struct {
	EntityType        string                 `json:"entityType"`
	Name              string                 `json:"name"`
	Description       *string                `json:"description"`
	Source            string                 `json:"source"`
	Classification    *string                `json:"classification"`
	FeedID            *string                `json:"feedId"`
	Position          *geoPointDTO           `json:"position"`
	Heading           *float64               `json:"heading"`
	SpeedKnots        *float64               `json:"speedKnots"`
	Course            *float64               `json:"course"`
	Altitude          *float64               `json:"altitude"`
	MilStd2525dSymbol *string                `json:"milStd2525dSymbol"`
	Metadata          map[string]interface{} `json:"metadata"`
	Affiliations      []string               `json:"affiliations"`
	TrackEnvironment  *string                `json:"trackEnvironment"`
	CountryOfOrigin   *string                `json:"countryOfOrigin"`
	PlatformData      map[string]interface{} `json:"platformData"`
	SourceEntityID    *string                `json:"sourceEntityId"`
	CircularError     *float64               `json:"circularError"`
	DimensionLength   *float64               `json:"dimensionLength"`
	DimensionWidth    *float64               `json:"dimensionWidth"`
}

type updatePositionRequest struct {
	Lat        float64  `json:"lat"`
	Lng        float64  `json:"lng"`
	Heading    *float64 `json:"heading"`
	SpeedKnots *float64 `json:"speedKnots"`
	Course     *float64 `json:"course"`
	Altitude   *float64 `json:"altitude"`
}

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
