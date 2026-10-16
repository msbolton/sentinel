package export

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/httputil"

	"github.com/sentinel/api-gateway/internal/auth"
)

const (
	defaultLimit = 1000
	maxLimit     = 5000
	// upstreamPage is how many entities one entity-service call returns.
	upstreamPage = 1000
	defaultRefreshSec = 30
)

// Handler serves /api/v1/export/*.
type Handler struct {
	source Source
	logger *zap.Logger
	// PublicURL is the base URL clients reach the gateway on, for the
	// NetworkLink href ("" = derive from the request).
	PublicURL string
}

// NewHandler builds the handler.
func NewHandler(source Source, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{source: source, logger: logger}
}

// RegisterRoutes mounts the export routes on the protected API mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/export/entities.geojson", h.handleGeoJSON)
	mux.HandleFunc("GET /api/v1/export/entities.kml", h.handleKML)
}

// parseQuery reads bbox=west,south,east,north, types, feeds, page and limit.
func parseQuery(q url.Values) (Query, int, error) {
	out := Query{Page: 1, PageSize: defaultLimit}
	if v := strings.TrimSpace(q.Get("bbox")); v != "" {
		parts := strings.Split(v, ",")
		if len(parts) != 4 {
			return out, 0, fmt.Errorf("bbox must be west,south,east,north")
		}
		var f [4]float64
		for i, p := range parts {
			x, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
			if err != nil {
				return out, 0, fmt.Errorf("bbox must be west,south,east,north")
			}
			f[i] = x
		}
		b := Bounds{West: f[0], South: f[1], East: f[2], North: f[3]}
		if b.South > b.North || b.South < -90 || b.North > 90 || b.West < -180 || b.East > 180 {
			return out, 0, fmt.Errorf("bbox out of range")
		}
		out.BBox = &b
	}
	out.Types = splitList(q["types"])
	out.Feeds = splitList(q["feeds"])
	if v := q.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return out, 0, fmt.Errorf("page must be a positive integer")
		}
		out.Page = n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return out, 0, fmt.Errorf("limit must be a positive integer")
		}
		if n > maxLimit {
			n = maxLimit
		}
		out.PageSize = n
	}
	refresh := defaultRefreshSec
	if v := q.Get("refresh"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return out, 0, fmt.Errorf("refresh must be a positive integer")
		}
		refresh = n
	}
	return out, refresh, nil
}

func splitList(values []string) []string {
	var out []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// fetch gathers one export page, looping over entity-service pages when the
// requested limit is larger than one upstream page.
func (h *Handler) fetch(r *http.Request, u *auth.User, q Query) ([]Entity, int, error) {
	var out []Entity
	total := 0
	first := (q.Page-1)*q.PageSize + 1 // 1-based index of the first wanted entity
	last := q.Page * q.PageSize
	for upPage := (first-1)/upstreamPage + 1; ; upPage++ {
		page, err := h.source.Query(r.Context(), u, Query{BBox: q.BBox, Types: q.Types, Feeds: q.Feeds, Page: upPage, PageSize: upstreamPage})
		if err != nil {
			return nil, 0, err
		}
		total = page.Total
		offset := (upPage - 1) * upstreamPage
		for i, e := range page.Entities {
			idx := offset + i + 1
			if idx >= first && idx <= last {
				out = append(out, e)
			}
		}
		if len(page.Entities) < upstreamPage || offset+len(page.Entities) >= last {
			break
		}
	}
	return out, total, nil
}

func (h *Handler) handleGeoJSON(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFrom(r.Context())
	if !ok {
		httputil.Error(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	q, _, err := parseQuery(r.URL.Query())
	if err != nil {
		httputil.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	entities, total, err := h.fetch(r, u, q)
	if err != nil {
		h.logger.Warn("export failed", zap.Error(err))
		httputil.Error(w, http.StatusBadGateway, "entity service unavailable")
		return
	}
	var buf bytes.Buffer
	if err := WriteGeoJSON(&buf, entities, total); err != nil {
		httputil.Error(w, http.StatusInternalServerError, "encoding failed")
		return
	}
	w.Header().Set("Content-Type", "application/geo+json")
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}

func (h *Handler) handleKML(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFrom(r.Context())
	if !ok {
		httputil.Error(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	q, refresh, err := parseQuery(r.URL.Query())
	if err != nil {
		httputil.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	var buf bytes.Buffer
	if r.URL.Query().Get("link") != "" {
		// A NetworkLink document: the client reopens the plain KML URL on
		// an interval, keeping every other parameter (including api_key).
		if err := WriteNetworkLink(&buf, h.linkHref(r), "SENTINEL live entities", refresh); err != nil {
			httputil.Error(w, http.StatusInternalServerError, "encoding failed")
			return
		}
	} else {
		entities, _, err := h.fetch(r, u, q)
		if err != nil {
			h.logger.Warn("export failed", zap.Error(err))
			httputil.Error(w, http.StatusBadGateway, "entity service unavailable")
			return
		}
		if err := WriteKML(&buf, entities, "SENTINEL live entities"); err != nil {
			httputil.Error(w, http.StatusInternalServerError, "encoding failed")
			return
		}
	}
	w.Header().Set("Content-Type", "application/vnd.google-earth.kml+xml")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}

// linkHref is this request's URL without link and refresh, so the network
// link fetches the same export (same filters, same api_key).
func (h *Handler) linkHref(r *http.Request) string {
	q := r.URL.Query()
	q.Del("link")
	q.Del("refresh")
	base := h.PublicURL
	if base == "" {
		scheme := "http"
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	href := strings.TrimRight(base, "/") + r.URL.Path
	if enc := q.Encode(); enc != "" {
		href += "?" + enc
	}
	return href
}
