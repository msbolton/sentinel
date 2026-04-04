package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/sentinel/go-common/httputil"
	"github.com/sentinel/search-service/internal/opensearch"
	"go.uber.org/zap"
)

// SearchHandler handles search HTTP endpoints.
type SearchHandler struct {
	osClient *opensearch.Client
	logger   *zap.Logger
}

// NewSearchHandler creates a new search handler.
func NewSearchHandler(osClient *opensearch.Client, logger *zap.Logger) *SearchHandler {
	return &SearchHandler{osClient: osClient, logger: logger}
}

// RegisterRoutes registers search endpoints on the mux.
func (h *SearchHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/search", h.handleSearch)
	mux.HandleFunc("GET /api/v1/search/nearby", h.handleNearby)
	mux.HandleFunc("GET /api/v1/search/suggest", h.handleSuggest)
	mux.HandleFunc("GET /api/v1/search/facets", h.handleFacets)
}

func (h *SearchHandler) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	query := opensearch.SearchQuery{
		Q:               q.Get("q"),
		Types:           parseStringSlice(q, "types"),
		Sources:         parseStringSlice(q, "sources"),
		Classifications: parseStringSlice(q, "classifications"),
		Page:            parseIntDefault(q.Get("page"), 1),
		PageSize:        parseIntDefault(q.Get("pageSize"), 20),
	}

	if v := q.Get("north"); v != "" {
		f := parseFloat(v)
		query.North = &f
	}
	if v := q.Get("south"); v != "" {
		f := parseFloat(v)
		query.South = &f
	}
	if v := q.Get("east"); v != "" {
		f := parseFloat(v)
		query.East = &f
	}
	if v := q.Get("west"); v != "" {
		f := parseFloat(v)
		query.West = &f
	}

	// Validate bounding box if partially provided.
	if query.North != nil && query.South != nil && *query.South >= *query.North {
		httputil.Error(w, http.StatusBadRequest, "Invalid bounding box: south must be less than north")
		return
	}
	if query.East != nil && query.West != nil && *query.West >= *query.East {
		httputil.Error(w, http.StatusBadRequest, "Invalid bounding box: west must be less than east")
		return
	}

	result, err := h.osClient.Search(r.Context(), query)
	if err != nil {
		h.logger.Error("search failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Search failed")
		return
	}

	httputil.JSON(w, http.StatusOK, result)
}

func (h *SearchHandler) handleNearby(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	latStr := q.Get("lat")
	lngStr := q.Get("lng")
	radiusStr := q.Get("radiusKm")

	if latStr == "" || lngStr == "" || radiusStr == "" {
		httputil.Error(w, http.StatusBadRequest, "lat, lng, and radiusKm are required")
		return
	}

	lat, err := strconv.ParseFloat(latStr, 64)
	if err != nil || lat < -90 || lat > 90 {
		httputil.Error(w, http.StatusBadRequest, "lat must be between -90 and 90")
		return
	}

	lng, err := strconv.ParseFloat(lngStr, 64)
	if err != nil || lng < -180 || lng > 180 {
		httputil.Error(w, http.StatusBadRequest, "lng must be between -180 and 180")
		return
	}

	radiusKm, err := strconv.ParseFloat(radiusStr, 64)
	if err != nil || radiusKm < 0.1 || radiusKm > 20000 {
		httputil.Error(w, http.StatusBadRequest, "radiusKm must be between 0.1 and 20000")
		return
	}

	query := opensearch.NearbyQuery{
		Lat:      lat,
		Lng:      lng,
		RadiusKm: radiusKm,
		Q:        q.Get("q"),
		Page:     parseIntDefault(q.Get("page"), 1),
		PageSize: parseIntDefault(q.Get("pageSize"), 20),
	}

	result, err := h.osClient.SearchNearby(r.Context(), query)
	if err != nil {
		h.logger.Error("nearby search failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Nearby search failed")
		return
	}

	httputil.JSON(w, http.StatusOK, result)
}

func (h *SearchHandler) handleSuggest(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || len(q) < 2 {
		httputil.Error(w, http.StatusBadRequest, "Query must be at least 2 characters")
		return
	}

	suggestions, err := h.osClient.Suggest(r.Context(), q)
	if err != nil {
		h.logger.Error("suggest failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Suggest failed")
		return
	}

	httputil.JSON(w, http.StatusOK, suggestions)
}

func (h *SearchHandler) handleFacets(w http.ResponseWriter, r *http.Request) {
	facets, err := h.osClient.GetFacets(r.Context())
	if err != nil {
		h.logger.Error("get facets failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Get facets failed")
		return
	}

	httputil.JSON(w, http.StatusOK, facets)
}

func parseStringSlice(q map[string][]string, key string) []string {
	values, ok := q[key]
	if !ok || len(values) == 0 {
		return nil
	}
	// Handle both repeated params (?types=A&types=B) and comma-separated (?types=A,B)
	var result []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
	}
	return result
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
