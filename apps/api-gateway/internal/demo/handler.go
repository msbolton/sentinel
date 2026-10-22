package demo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/api-gateway/internal/auth"
	"github.com/sentinel/api-gateway/internal/registration"
	"github.com/sentinel/go-common/httputil"
)

// maxPositionBody caps a position report; a real one is a few dozen bytes.
const maxPositionBody = 1 << 10

// A guest may report its position positionBurst times at once, then once per
// positionWindow/positionBurst (5 s): each report costs an upsert in ingest.
const (
	positionBurst  = 2
	positionWindow = 10 * time.Second
)

// Position sources reported back to the browser.
const (
	sourceBrowser = "browser"
	sourceIP      = "ip"
	sourceNone    = "none"
)

// Handler serves the demo session and position routes.
type Handler struct {
	guests         *Guests
	sites          VisitorSites
	loc            Locator
	visitorsFeedID string
	limiter        *registration.IPLimiter
	positions      *registration.IPLimiter // keyed by guest id
	logger         *zap.Logger
}

// NewHandler creates the demo handler. limiter may be nil, which disables the
// session rate limit; loc may be nil, which never locates.
func NewHandler(g *Guests, sites VisitorSites, loc Locator, visitorsFeedID string, limiter *registration.IPLimiter, logger *zap.Logger) *Handler {
	if loc == nil {
		loc = NopLocator{}
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{guests: g, sites: sites, loc: loc, visitorsFeedID: visitorsFeedID, limiter: limiter,
		positions: registration.NewIPLimiter(positionBurst, positionWindow), logger: logger}
}

// RegisterPublicRoutes mounts the unauthenticated session endpoint.
func (h *Handler) RegisterPublicRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/demo/session", h.handleSession)
}

// RegisterProtectedRoutes mounts the position endpoint. The caller applies
// auth; the handler itself admits only registered guests.
func (h *Handler) RegisterProtectedRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/demo/position", h.handlePosition)
}

func (h *Handler) handleSession(w http.ResponseWriter, r *http.Request) {
	// The limits are keyed on the rightmost X-Forwarded-For entry (the
	// limiter's own Middleware would trust the leftmost, which the client
	// controls), and an IPv6 address by its /64.
	addr := AddressKey(ClientIP(r))
	if h.limiter != nil && !h.limiter.Allow(addr) {
		httputil.Error(w, http.StatusTooManyRequests, "Too many demo sessions from this address")
		return
	}
	s, err := h.guests.Start(r.Context(), addr)
	if errors.Is(err, ErrFull) {
		httputil.JSON(w, http.StatusServiceUnavailable, map[string]string{"error": "full"})
		return
	}
	if errors.Is(err, ErrAddressBusy) {
		httputil.JSON(w, http.StatusTooManyRequests, map[string]string{
			"error":   "busy",
			"message": "Your network already has demo sessions open; try again in a moment",
		})
		return
	}
	if err != nil {
		// Start has already logged the cause.
		httputil.Error(w, http.StatusBadGateway, "Could not start a demo session")
		return
	}
	s.VisitorsFeedID = h.visitorsFeedID
	w.Header().Set("Cache-Control", "no-store")
	httputil.JSON(w, http.StatusOK, s)
}

type positionRequest struct {
	Lat    *float64 `json:"lat"`
	Lon    *float64 `json:"lon"`
	Denied bool     `json:"denied"`
}

// positionResponse tells the visitor where their site was placed: the
// blurred position, the only one the server handles beyond this request.
// The browser blurs before it sends, and keeps its precise fix to itself.
type positionResponse struct {
	Lat           *float64 `json:"lat,omitempty"`
	Lon           *float64 `json:"lon,omitempty"`
	Source        string   `json:"source"`
	EmplacementID string   `json:"emplacementId,omitempty"`
}

func (h *Handler) handlePosition(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || !user.HasAnyRole(auth.RoleGuest) {
		httputil.Error(w, http.StatusForbidden, "Only demo guests report a position")
		return
	}
	guest, ok := h.guests.Lookup(user.UserID)
	if !ok {
		httputil.Error(w, http.StatusForbidden, "Demo session has ended")
		return
	}

	req, ok := decodePosition(w, r)
	if !ok {
		httputil.Error(w, http.StatusBadRequest, "Send {\"lat\":…,\"lon\":…} or {\"denied\":true}")
		return
	}
	if !h.positions.Allow(guest.GuestID) {
		w.Header().Set("Retry-After", "5")
		httputil.Error(w, http.StatusTooManyRequests, "Too many position reports; try again in a few seconds")
		return
	}

	var lat, lon float64
	source := sourceBrowser
	if req.Denied {
		ipLat, ipLon, found := h.loc.Locate(ClientIP(r))
		if !found || !validCoordinate(ipLat, ipLon) {
			httputil.JSON(w, http.StatusOK, positionResponse{Source: sourceNone})
			return
		}
		lat, lon, source = ipLat, ipLon, sourceIP
	} else {
		lat, lon = *req.Lat, *req.Lon
	}

	blurLat, blurLon := Blur(lat, lon)
	empID, err := h.sites.Place(r.Context(), guest.GuestID, blurLat, blurLon)
	if err != nil {
		h.logger.Error("failed to place demo visitor", zap.String("guestId", guest.GuestID), zap.String("source", source), zap.Error(err))
		httputil.Error(w, http.StatusBadGateway, "Could not place your site")
		return
	}
	// Sweep may have ended the guest while Place was in flight, and its Remove
	// may have landed before this Place did. Sweep marks a guest ending before
	// it removes the site, so re-checking after Place catches every such
	// interleaving; take the site down again rather than leave it resurrected.
	if _, live := h.guests.Lookup(user.UserID); !live {
		h.sites.Remove(context.WithoutCancel(r.Context()), guest.GuestID)
		httputil.Error(w, http.StatusForbidden, "Demo session has ended")
		return
	}
	httputil.JSON(w, http.StatusOK, positionResponse{Lat: &blurLat, Lon: &blurLon, Source: source, EmplacementID: empID})
}

// decodePosition reads a body that is exactly one of a valid lat/lon pair or
// {"denied":true}, at most maxPositionBody bytes.
func decodePosition(w http.ResponseWriter, r *http.Request) (positionRequest, bool) {
	var req positionRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPositionBody))
	if err := dec.Decode(&req); err != nil {
		return req, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return req, false // trailing data
	}
	hasCoords := req.Lat != nil || req.Lon != nil
	switch {
	case req.Denied:
		return req, !hasCoords
	case req.Lat == nil || req.Lon == nil:
		return req, false
	default:
		return req, validCoordinate(*req.Lat, *req.Lon)
	}
}

// validCoordinate reports whether lat/lon are finite and on the globe.
func validCoordinate(lat, lon float64) bool {
	if math.IsNaN(lat) || math.IsNaN(lon) || math.IsInf(lat, 0) || math.IsInf(lon, 0) {
		return false
	}
	return math.Abs(lat) <= 90 && math.Abs(lon) <= 180
}
