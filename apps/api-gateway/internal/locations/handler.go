package locations

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/httputil"
)

var validCategories = map[string]struct{}{
	"CITY": {}, "MILITARY_BASE": {}, "PORT": {}, "AIRPORT": {}, "CUSTOM": {},
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Handler serves the /api/v1/locations routes.
type Handler struct {
	store  Store
	logger *zap.Logger
}

// NewHandler creates a locations handler.
func NewHandler(store Store, logger *zap.Logger) *Handler {
	return &Handler{store: store, logger: logger}
}

// RegisterRoutes mounts the location routes on mux. Authorization is applied
// by the caller.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/locations", h.handleList)
	mux.HandleFunc("GET /api/v1/locations/{id}", h.handleGet)
	mux.HandleFunc("POST /api/v1/locations", h.handleCreate)
	mux.HandleFunc("PATCH /api/v1/locations/{id}", h.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/locations/{id}", h.handleDelete)
}

type locationRequest struct {
	Name        *string  `json:"name"`
	Description *string  `json:"description"`
	Latitude    *float64 `json:"latitude"`
	Longitude   *float64 `json:"longitude"`
	Altitude    *float64 `json:"altitude"`
	Heading     *float64 `json:"heading"`
	Pitch       *float64 `json:"pitch"`
	Range       *float64 `json:"range"`
	Has3DTiles  *bool    `json:"has3dTiles"`
	Category    *string  `json:"category"`
	CreatedBy   *string  `json:"createdBy"`
}

func (req *locationRequest) toInput() Input {
	return Input{
		Name: req.Name, Description: req.Description, Latitude: req.Latitude, Longitude: req.Longitude,
		Altitude: req.Altitude, Heading: req.Heading, Pitch: req.Pitch, Range: req.Range,
		Has3DTiles: req.Has3DTiles, Category: req.Category, CreatedBy: req.CreatedBy,
	}
}

// validate checks field constraints. When create is true, required fields must
// be present and defaults are filled in.
func (req *locationRequest) validate(create bool) string {
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			return "name should not be empty"
		}
		if len(*req.Name) > 255 {
			return "name must be shorter than or equal to 255 characters"
		}
	} else if create {
		return "name is required"
	}
	if req.Latitude != nil {
		if *req.Latitude < -90 || *req.Latitude > 90 {
			return "latitude must be between -90 and 90"
		}
	} else if create {
		return "latitude is required"
	}
	if req.Longitude != nil {
		if *req.Longitude < -180 || *req.Longitude > 180 {
			return "longitude must be between -180 and 180"
		}
	} else if create {
		return "longitude is required"
	}
	if req.Altitude != nil && *req.Altitude < 0 {
		return "altitude must not be less than 0"
	}
	if req.Range != nil && *req.Range < 0 {
		return "range must not be less than 0"
	}
	if req.Category != nil {
		if _, ok := validCategories[*req.Category]; !ok {
			return "category must be one of CITY, MILITARY_BASE, PORT, AIRPORT, CUSTOM"
		}
	}
	if create {
		fill := func(p **float64, v float64) {
			if *p == nil {
				*p = &v
			}
		}
		fill(&req.Altitude, 1000)
		fill(&req.Heading, 0)
		fill(&req.Pitch, -45)
		fill(&req.Range, 2000)
		if req.Has3DTiles == nil {
			f := false
			req.Has3DTiles = &f
		}
		if req.Category == nil {
			c := "CUSTOM"
			req.Category = &c
		}
	}
	return ""
}

func decodeBody(r *http.Request, dst interface{}) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func pathID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		httputil.Error(w, http.StatusBadRequest, "Validation failed (uuid is expected)")
		return "", false
	}
	return id, true
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	category := q.Get("category")
	if category != "" {
		if _, ok := validCategories[category]; !ok {
			httputil.Error(w, http.StatusBadRequest, "category must be one of CITY, MILITARY_BASE, PORT, AIRPORT, CUSTOM")
			return
		}
	}
	list, err := h.store.List(r.Context(), category, q.Get("search"))
	if err != nil {
		httputil.ErrorWithLogger(w, http.StatusInternalServerError, "Failed to list locations", h.logger, err)
		return
	}
	httputil.JSON(w, http.StatusOK, list)
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	loc, err := h.store.Get(r.Context(), id)
	if err != nil {
		h.respondStoreError(w, id, err, "Failed to get location")
		return
	}
	httputil.JSON(w, http.StatusOK, loc)
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req locationRequest
	if err := decodeBody(r, &req); err != nil {
		httputil.Error(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if msg := req.validate(true); msg != "" {
		httputil.Error(w, http.StatusBadRequest, msg)
		return
	}
	loc, err := h.store.Create(r.Context(), req.toInput())
	if err != nil {
		httputil.ErrorWithLogger(w, http.StatusInternalServerError, "Failed to create location", h.logger, err)
		return
	}
	h.logger.Info("location created", zap.String("id", loc.ID), zap.String("name", loc.Name))
	httputil.JSON(w, http.StatusCreated, loc)
}

func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req locationRequest
	if err := decodeBody(r, &req); err != nil {
		httputil.Error(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if msg := req.validate(false); msg != "" {
		httputil.Error(w, http.StatusBadRequest, msg)
		return
	}
	loc, err := h.store.Update(r.Context(), id, req.toInput())
	if err != nil {
		h.respondStoreError(w, id, err, "Failed to update location")
		return
	}
	h.logger.Info("location updated", zap.String("id", id))
	httputil.JSON(w, http.StatusOK, loc)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.store.Delete(r.Context(), id); err != nil {
		h.respondStoreError(w, id, err, "Failed to delete location")
		return
	}
	h.logger.Info("location deleted", zap.String("id", id))
	httputil.NoContent(w)
}

func (h *Handler) respondStoreError(w http.ResponseWriter, id string, err error, msg string) {
	if errors.Is(err, ErrNotFound) {
		httputil.Error(w, http.StatusNotFound, "Location "+id+" not found")
		return
	}
	httputil.ErrorWithLogger(w, http.StatusInternalServerError, msg, h.logger, err)
}
