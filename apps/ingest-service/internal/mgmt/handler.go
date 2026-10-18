// Package mgmt is the ingest service's management API: feed CRUD backed by
// the registry, driver discovery, and the area-of-interest endpoint the API
// gateway pushes to. Any replica can serve it; feed changes reach the
// owning replica through the registry's notifications.
package mgmt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/middleware"

	"github.com/sentinel/ingest-service/internal/aoi"
	"github.com/sentinel/ingest-service/internal/drivers"
	"github.com/sentinel/ingest-service/internal/registry"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

// maxBoxes bounds an area push; the gateway sends at most two.
const maxBoxes = 4

// FeedStore is the registry access the handler needs.
type FeedStore interface {
	List(ctx context.Context) ([]registry.Feed, error)
	Get(ctx context.Context, id string) (registry.Feed, error)
	CreateGuarded(ctx context.Context, f *registry.Feed, g registry.Guard) error
	UpdateGuarded(ctx context.Context, f *registry.Feed, g registry.Guard) error
	SetEnabled(ctx context.Context, id string, enabled bool) (registry.Feed, error)
	Delete(ctx context.Context, id string) error
	GetArea(ctx context.Context, id string) (registry.Area, error)
	SetArea(ctx context.Context, a registry.Area) error
	LiveReplicas(ctx context.Context) ([]registry.Replica, error)
}

// Runtime is the local-replica fast path: live status for feeds running
// here and direct area pushes.
type Runtime interface {
	SetArea(id string, a registry.Area) (aoi.Status, bool)
	AreaStatus(id string) (aoi.Status, bool)
	Snapshot(id string) (registry.Status, bool)
}

// Handler serves the management routes.
type Handler struct {
	store     FeedStore
	catalog   *drivers.Catalog
	validator *registry.Validator
	runtime   Runtime
	logger    *zap.Logger
	// StaleAfter is how old a status row written by another replica may be
	// before the feed renders as unassigned (three heartbeats, 15 s).
	StaleAfter time.Duration
	// Reserved are the ports a listening connection may not bind (Sentinel's own).
	Reserved registry.PortSet
}

// NewHandler wires the handler. runtime may be nil (status-only replicas).
func NewHandler(store FeedStore, catalog *drivers.Catalog, rt Runtime, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{store: store, catalog: catalog, validator: &registry.Validator{Catalog: catalog}, runtime: rt, logger: logger, StaleAfter: 15 * time.Second}
}

// placement is the mix of live replicas one request sees. known is false when
// the lookup failed, and then nothing is said about where a feed can run.
type placement struct {
	known     bool
	live      int // every live replica
	listeners int // those that take listening connections
	ordinary  int // those that take ordinary feeds: the non-listeners and the shared listeners
}

// placement looks the live replicas up once for a request. A failed lookup
// leaves the mix unknown, so no feed is explained by it either way; reading
// it as no replicas would blame a missing listener for as long as the lookup
// fails.
func (h *Handler) placement(ctx context.Context) placement {
	replicas, err := h.store.LiveReplicas(ctx)
	if err != nil {
		h.logger.Warn("listing live replicas failed", zap.Error(err))
		return placement{}
	}
	p := placement{known: true, live: len(replicas)}
	for _, r := range replicas {
		if r.Listener {
			p.listeners++
		}
		if !r.Listener || r.Shared {
			p.ordinary++
		}
	}
	return p
}

// unassigned returns f with its status replaced by unassigned and the reason
// it has no replica; the rest of a stored status stands.
func unassigned(f registry.Feed, reason string) registry.Feed {
	st := registry.Status{FeedID: f.ID}
	if f.Status != nil {
		st = *f.Status
	}
	st.State, st.Health, st.LastError = registry.StateUnassigned, registry.HealthUnknown, reason
	f.Status = &st
	return f
}

// present overlays the live status when the feed runs on this replica,
// renders a stale row from a silent replica as unassigned, and explains an
// enabled feed that has no replica to run on: a listening connection with no
// listener replica live, and an ordinary feed when the only live replicas are
// dedicated listeners.
func (h *Handler) present(f registry.Feed, p placement) registry.Feed {
	if h.runtime != nil {
		if st, live := h.runtime.Snapshot(f.ID); live {
			st.FeedID = f.ID
			if st.UpdatedAt == nil {
				now := time.Now().UTC()
				st.UpdatedAt = &now
			}
			f.Status = &st
			return f
		}
	}
	if f.Status != nil && f.Status.Owner != "" && f.Status.UpdatedAt != nil && h.StaleAfter > 0 && time.Since(*f.Status.UpdatedAt) > h.StaleAfter {
		st := *f.Status
		st.State = registry.StateUnassigned
		st.Health = registry.HealthUnknown
		f.Status = &st
	}
	// Only a feed not running here gets this far, so a feed this replica runs
	// (LEASE_ENABLED=false) is never explained away. A feed pinned to a
	// replica runs there, whatever the others are.
	if !p.known || !f.Enabled || f.Common().PinnedReplica != "" {
		return f
	}
	if _, _, listening := driver.ListenOf(f.Driver, f.Config); listening {
		if p.listeners == 0 {
			return unassigned(f, registry.NoListenerError)
		}
	} else if p.live > 0 && p.ordinary == 0 {
		return unassigned(f, registry.NoOrdinaryReplicaError)
	}
	return f
}

// RegisterRoutes mounts the routes on mux, each wrapped by guard.
func (h *Handler) RegisterRoutes(mux *http.ServeMux, guard func(http.Handler) http.Handler) {
	if guard == nil {
		guard = func(n http.Handler) http.Handler { return n }
	}
	route := func(pattern string, fn http.HandlerFunc) { mux.Handle(pattern, guard(fn)) }
	route("GET /feeds", h.list)
	route("POST /feeds", h.create)
	route("GET /feeds/{id}", h.get)
	route("PUT /feeds/{id}", h.replace)
	route("PATCH /feeds/{id}", h.patch)
	route("DELETE /feeds/{id}", h.delete)
	route("POST /feeds/{id}/enable", h.setEnabled(true))
	route("POST /feeds/{id}/disable", h.setEnabled(false))
	route("GET /feeds/{id}/status", h.status)
	route("GET /feeds/{id}/area", h.getArea)
	route("PUT /feeds/{id}/area", h.putArea)
	route("GET /drivers", h.listDrivers)
	route("GET /drivers/{id}", h.getDriver)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	feeds, err := h.store.List(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	q := r.URL.Query()
	driver, capability, direction := q.Get("driver"), q.Get("capability"), q.Get("direction")
	var enabled *bool
	if v := q.Get("enabled"); v != "" {
		b := v == "true"
		enabled = &b
	}
	p := h.placement(r.Context())
	out := make([]feedDTO, 0, len(feeds))
	for _, f := range feeds {
		if driver != "" && f.Driver != driver {
			continue
		}
		if enabled != nil && f.Enabled != *enabled {
			continue
		}
		if capability != "" {
			info, ok := h.catalog.Get(f.Driver)
			if !ok || !info.HasCapability(capability) {
				continue
			}
		}
		dto := toFeedDTO(h.present(f, p), h.catalog)
		// direction=in|out lists every feed that moves data that way, so
		// "both" matches either; direction=both matches only "both".
		if direction != "" && !matchesDirection(dto.Direction, direction) {
			continue
		}
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, out)
}

func matchesDirection(have, want string) bool {
	switch want {
	case "in":
		return have == "in" || have == "both"
	case "out":
		return have == "out" || have == "both"
	default:
		return have == want
	}
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	f, err := h.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toFeedDTO(h.present(f, h.placement(r.Context())), h.catalog))
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	f, err := h.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toFeedDTO(h.present(f, h.placement(r.Context())), h.catalog).Status)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ID == "" {
		req.ID = registry.Slugify(req.Name)
	}
	f := registry.Feed{
		ID: req.ID, Name: strings.TrimSpace(req.Name), Description: req.Description, Driver: req.Driver,
		Config: req.Config, SecretRefs: req.SecretRefs, CreatedBy: middleware.UserID(r.Context()),
	}
	if req.Enabled != nil {
		f.Enabled = *req.Enabled
	}
	if err := h.validator.Validate(&f); err != nil {
		h.fail(w, err)
		return
	}
	if problems := h.portProblems(f); len(problems) > 0 {
		h.fail(w, &registry.ValidationError{Problems: problems})
		return
	}
	if err := h.store.CreateGuarded(r.Context(), &f, h.portGuard(f)); err != nil {
		h.fail(w, err)
		return
	}
	h.logger.Info("feed created", zap.String("feed", f.ID), zap.String("driver", f.Driver), zap.String("by", f.CreatedBy))
	writeJSON(w, http.StatusCreated, toFeedDTO(f, h.catalog))
}

func (h *Handler) replace(w http.ResponseWriter, r *http.Request) {
	var req replaceRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Name == nil || req.Config == nil || req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "name, config and enabled are required; use PATCH for partial updates")
		return
	}
	f, err := h.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	f.Name = strings.TrimSpace(*req.Name)
	if req.Description != nil {
		f.Description = *req.Description
	}
	f.Config = req.Config
	f.SecretRefs = req.SecretRefs
	f.Enabled = *req.Enabled
	h.saveUpdate(w, r, &f)
}

func (h *Handler) patch(w http.ResponseWriter, r *http.Request) {
	var req map[string]json.RawMessage
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	for key := range req {
		switch key {
		case "name", "description", "config", "secretRefs", "enabled":
		case "id", "driver", "builtin":
			writeError(w, http.StatusBadRequest, key+" is immutable")
			return
		default:
			writeError(w, http.StatusBadRequest, "unknown field "+key)
			return
		}
	}
	f, err := h.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	if raw, ok := req["name"]; ok {
		if err := json.Unmarshal(raw, &f.Name); err != nil {
			writeError(w, http.StatusBadRequest, "name must be a string")
			return
		}
		f.Name = strings.TrimSpace(f.Name)
	}
	if raw, ok := req["description"]; ok {
		if err := json.Unmarshal(raw, &f.Description); err != nil {
			writeError(w, http.StatusBadRequest, "description must be a string")
			return
		}
	}
	if raw, ok := req["enabled"]; ok {
		if err := json.Unmarshal(raw, &f.Enabled); err != nil {
			writeError(w, http.StatusBadRequest, "enabled must be a boolean")
			return
		}
	}
	if raw, ok := req["secretRefs"]; ok {
		if err := json.Unmarshal(raw, &f.SecretRefs); err != nil {
			writeError(w, http.StatusBadRequest, "secretRefs must be an object of strings")
			return
		}
	}
	if raw, ok := req["config"]; ok {
		merged, err := mergeConfig(f.Config, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		f.Config = merged
	}
	h.saveUpdate(w, r, &f)
}

func (h *Handler) saveUpdate(w http.ResponseWriter, r *http.Request, f *registry.Feed) {
	if err := h.validator.Validate(f); err != nil {
		h.fail(w, err)
		return
	}
	if problems := h.portProblems(*f); len(problems) > 0 {
		h.fail(w, &registry.ValidationError{Problems: problems})
		return
	}
	if err := h.store.UpdateGuarded(r.Context(), f, h.portGuard(*f)); err != nil {
		h.fail(w, err)
		return
	}
	h.logger.Info("feed updated", zap.String("feed", f.ID), zap.Int("version", f.Version))
	writeJSON(w, http.StatusOK, toFeedDTO(*f, h.catalog))
}

// portProblems checks the feed's allowlist, which needs no other feed. Its
// listening port is checked against every other feed by portGuard.
func (h *Handler) portProblems(f registry.Feed) []registry.Problem {
	return registry.CheckAllow(f)
}

// portGuard is the port check the store makes when it saves the proposed feed,
// under a lock so that two saves cannot take the same port. It is nil for a
// connection that does not listen: it has no port to check, and the lock is
// not worth taking.
func (h *Handler) portGuard(proposed registry.Feed) registry.Guard {
	if _, _, listens := driver.ListenOf(proposed.Driver, proposed.Config); !listens {
		return nil
	}
	return func(f registry.Feed, others []registry.Feed) []registry.Problem {
		return registry.CheckPorts(f, others, h.Reserved)
	}
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f, err := h.store.Get(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	if f.Builtin {
		writeError(w, http.StatusForbidden, "built-in feeds cannot be deleted; disable them instead")
		return
	}
	if err := h.store.Delete(r.Context(), id); err != nil {
		h.fail(w, err)
		return
	}
	h.logger.Info("feed deleted", zap.String("feed", id))
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) setEnabled(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := h.store.SetEnabled(r.Context(), r.PathValue("id"), enabled)
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toFeedDTO(f, h.catalog))
	}
}

func (h *Handler) getArea(w http.ResponseWriter, r *http.Request) {
	f, info, ok := h.areaFeed(w, r)
	if !ok {
		return
	}
	_ = info
	area, err := h.store.GetArea(r.Context(), f.ID)
	if err != nil {
		h.fail(w, err)
		return
	}
	if h.runtime != nil {
		if st, live := h.runtime.AreaStatus(f.ID); live {
			writeJSON(w, http.StatusOK, areaFromLive(f, area, st))
			return
		}
	}
	writeJSON(w, http.StatusOK, areaFromRow(f, area))
}

func (h *Handler) putArea(w http.ResponseWriter, r *http.Request) {
	f, info, ok := h.areaFeed(w, r)
	if !ok {
		return
	}
	var req areaRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if msg := validateArea(req); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	area := registry.Area{FeedID: f.ID, Active: req.Active, Clients: req.Clients, Boxes: req.Boxes}
	if area.Boxes == nil {
		area.Boxes = []registry.Box{}
	}
	if ttl := areaTTL(f, info); ttl > 0 {
		exp := time.Now().Add(ttl).UTC()
		area.ExpiresAt = &exp
	}
	if err := h.store.SetArea(r.Context(), area); err != nil {
		h.fail(w, err)
		return
	}
	if h.runtime != nil {
		if st, live := h.runtime.SetArea(f.ID, area); live {
			writeJSON(w, http.StatusOK, areaFromLive(f, area, st))
			return
		}
	}
	writeJSON(w, http.StatusOK, areaFromRow(f, area))
}

// areaTTL is the driver's lease TTL unless the feed config overrides it
// (viewportTtlSec, kept from the first OpenSky integration).
func areaTTL(f registry.Feed, info drivers.Info) time.Duration {
	var c struct {
		ViewportTTLSec int `json:"viewportTtlSec"`
		AreaTTLSec     int `json:"areaTtlSec"`
	}
	_ = json.Unmarshal(f.Config, &c)
	switch {
	case c.AreaTTLSec > 0:
		return time.Duration(c.AreaTTLSec) * time.Second
	case c.ViewportTTLSec > 0:
		return time.Duration(c.ViewportTTLSec) * time.Second
	}
	return info.AreaTTL
}

// areaFeed loads the feed and checks its driver supports areas.
func (h *Handler) areaFeed(w http.ResponseWriter, r *http.Request) (registry.Feed, drivers.Info, bool) {
	f, err := h.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return registry.Feed{}, drivers.Info{}, false
	}
	info, ok := h.catalog.Get(f.Driver)
	if !ok || !info.HasCapability(drivers.CapabilityArea) {
		writeError(w, http.StatusConflict, fmt.Sprintf("feed %s (driver %s) does not take an area of interest", f.ID, f.Driver))
		return registry.Feed{}, drivers.Info{}, false
	}
	return f, info, true
}

func (h *Handler) listDrivers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.catalog.List())
}

func (h *Handler) getDriver(w http.ResponseWriter, r *http.Request) {
	info, ok := h.catalog.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "unknown driver")
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// fail maps registry errors to HTTP statuses.
func (h *Handler) fail(w http.ResponseWriter, err error) {
	var ve *registry.ValidationError
	switch {
	case errors.As(err, &ve):
		writeProblems(w, ve.Problems)
	case errors.Is(err, registry.ErrNotFound):
		writeError(w, http.StatusNotFound, "feed not found")
	case errors.Is(err, registry.ErrConflict):
		writeError(w, http.StatusConflict, "a feed with this id already exists")
	default:
		h.logger.Error("feed request failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func decodeBody(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return errors.New("reading body")
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return errors.New("request body is required")
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	if err := dec.Decode(v); err != nil {
		return errors.New("invalid JSON body")
	}
	return nil
}

func validateArea(req areaRequest) string {
	if len(req.Boxes) > maxBoxes {
		return fmt.Sprintf("at most %d boxes allowed", maxBoxes)
	}
	if req.Active && len(req.Boxes) == 0 {
		return "an active area needs at least one box"
	}
	if req.Clients < 0 {
		return "clients must not be negative"
	}
	for i, b := range req.Boxes {
		bb := aoi.BBox{North: b.North, South: b.South, East: b.East, West: b.West}
		if err := bb.Validate(); err != nil {
			return fmt.Sprintf("box %d: %v", i, err)
		}
	}
	return ""
}

// mergeConfig applies a one-level merge: keys in patch replace keys in
// base; a null in patch removes the key.
func mergeConfig(base, patch json.RawMessage) (json.RawMessage, error) {
	var b, p map[string]json.RawMessage
	if len(base) > 0 {
		if err := json.Unmarshal(base, &b); err != nil {
			return nil, errors.New("stored config is not an object")
		}
	}
	if b == nil {
		b = map[string]json.RawMessage{}
	}
	if err := json.Unmarshal(patch, &p); err != nil {
		return nil, errors.New("config must be an object")
	}
	for k, v := range p {
		if string(v) == "null" {
			delete(b, k)
			continue
		}
		b[k] = v
	}
	return json.Marshal(b)
}
