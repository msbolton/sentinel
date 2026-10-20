package emplacement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/classification"
	"github.com/sentinel/go-common/middleware"
)

// FeedCheck reports whether a feed exists and takes data in, for binding an
// emplacement to the connection that may report as it.
type FeedCheck func(ctx context.Context, feedID string) (exists, inbound bool, err error)

// Handler serves the emplacement routes of the management API.
type Handler struct {
	store  Store
	feeds  FeedCheck
	logger *zap.Logger
}

// NewHandler wires the handler; logger may be nil.
func NewHandler(store Store, feeds FeedCheck, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{store: store, feeds: feeds, logger: logger}
}

// RegisterRoutes mounts the routes on mux, each wrapped by guard (the
// management API's service-token and role check).
func (h *Handler) RegisterRoutes(mux *http.ServeMux, guard func(http.Handler) http.Handler) {
	route := func(pattern string, fn http.HandlerFunc) { mux.Handle(pattern, guard(fn)) }
	route("GET /emplacements", h.list)
	route("POST /emplacements", h.create)
	route("GET /emplacements/{id}", h.get)
	route("PATCH /emplacements/{id}", h.patch)
	route("DELETE /emplacements/{id}", h.remove)
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func clearance(r *http.Request) int {
	return classification.Rank(middleware.Classification(r.Context()))
}

// visible reports whether the caller's clearance reaches e's classification.
// An emplacement above it does not exist for them: 404, never 403.
func visible(r *http.Request, e Emplacement) bool {
	return classification.Rank(e.Classification) <= clearance(r)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := Filter{FeedID: q.Get("feedId"), Mobility: strings.ToUpper(q.Get("mobility"))}
	if v := q.Get("active"); v != "" {
		b := v == "true"
		f.Active = &b
	}
	list, err := h.store.List(r.Context(), f)
	if err != nil {
		h.fail(w, err)
		return
	}
	out := make([]Emplacement, 0, len(list))
	for _, e := range list {
		if visible(r, e) {
			out = append(out, e)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	e, err := h.store.Get(r.Context(), id)
	if err == nil && !visible(r, e) {
		err = ErrNotFound
	}
	if err != nil {
		h.fail(w, err)
		return
	}
	writeEmplacement(w, http.StatusOK, e)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := decodeBody(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	e := New()
	in.Apply(&e)
	e.CreatedBy = middleware.UserID(r.Context())
	if err := h.check(r, &e); err != nil {
		h.fail(w, err)
		return
	}
	out, err := h.store.Create(r.Context(), e)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.logger.Info("emplacement created", zap.String("id", out.ID), zap.String("name", out.Name))
	writeEmplacement(w, http.StatusCreated, out)
}

func (h *Handler) patch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ifVersion, ok := ifMatch(w, r)
	if !ok {
		return
	}
	var in Input
	if err := decodeBody(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The version is compared here, after the visibility check, not by the
	// store: a store that compares first would answer 412 for an emplacement
	// above the caller's clearance, revealing that it exists and, by trying
	// versions, which one it is. fn runs under the store's row lock, so the
	// check and the write stay atomic.
	out, err := h.store.Update(r.Context(), id, 0, func(e *Emplacement) error {
		if !visible(r, *e) {
			return ErrNotFound
		}
		if ifVersion > 0 && e.Version != ifVersion {
			return ErrVersionConflict
		}
		// A sender reports where its mobile emplacement is; a position set
		// by hand would be overwritten at its next report, so it is ignored
		// while the emplacement stays mobile. A person making it fixed is
		// choosing where it is, and that position applies.
		if e.Mobility == MobilityMobile && e.SenderManaged() &&
			(in.Mobility == nil || strings.ToUpper(strings.TrimSpace(*in.Mobility)) == MobilityMobile) {
			in.Position, in.Altitude = nil, nil
		}
		in.Apply(e)
		return h.check(r, e)
	})
	if err != nil {
		h.fail(w, err)
		return
	}
	writeEmplacement(w, http.StatusOK, out)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	e, err := h.store.Get(r.Context(), id)
	if err == nil && !visible(r, e) {
		err = ErrNotFound
	}
	if err == nil {
		err = h.store.Delete(r.Context(), id)
	}
	if err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CheckWrite validates e as it would be stored and holds it to what the
// writer may write: no classification above ceilingRank, and a binding only
// to a connection that takes data in. The HTTP API passes the caller's
// clearance; the grpc driver passes the sender's token ceiling.
func CheckWrite(ctx context.Context, e *Emplacement, ceilingRank int, feeds FeedCheck) error {
	ve := &ValidationError{}
	if err := Validate(e); err != nil {
		if !errors.As(err, &ve) {
			return err
		}
	}
	if classification.Valid(e.Classification) && classification.Rank(e.Classification) > ceilingRank {
		ve.Problems = append(ve.Problems, Problem{Path: "/classification", Message: "is above your clearance"})
	}
	if e.FeedID != nil {
		exists, inbound, err := feeds(ctx, *e.FeedID)
		switch {
		case err != nil:
			return err
		case !exists:
			ve.Problems = append(ve.Problems, Problem{Path: "/feedId", Message: "names no connection"})
		case !inbound:
			ve.Problems = append(ve.Problems, Problem{Path: "/feedId", Message: "names a connection that does not take data in"})
		}
	}
	if len(ve.Problems) > 0 {
		return ve
	}
	return nil
}

func (h *Handler) check(r *http.Request, e *Emplacement) error {
	return CheckWrite(r.Context(), e, clearance(r), h.feeds)
}

func pathID(w http.ResponseWriter, r *http.Request) (string, bool) {
	// Lower case is the form the rows, the store's notifications and every
	// replica's cache key on; an upper-case uuid names the same row.
	id := strings.ToLower(r.PathValue("id"))
	if !uuidRe.MatchString(id) {
		writeError(w, http.StatusBadRequest, "the id must be a uuid")
		return "", false
	}
	return id, true
}

// ifMatch reads If-Match ("3", 3 or W/"3"); absent means no check (0).
func ifMatch(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		return 0, true
	}
	v, err := strconv.Atoi(strings.Trim(strings.TrimPrefix(raw, "W/"), `"`))
	if err != nil || v < 1 {
		writeError(w, http.StatusBadRequest, "If-Match must be the emplacement's version")
		return 0, false
	}
	return v, true
}

func decodeBody(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return errors.New("reading body")
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %v", err)
	}
	return nil
}

func (h *Handler) fail(w http.ResponseWriter, err error) {
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid emplacement", "details": ve.Problems})
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "emplacement not found")
	case errors.Is(err, ErrVersionConflict):
		writeError(w, http.StatusPreconditionFailed, ErrVersionConflict.Error())
	case errors.Is(err, ErrNameTaken):
		writeError(w, http.StatusConflict, ErrNameTaken.Error())
	default:
		h.logger.Error("emplacement request failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func writeEmplacement(w http.ResponseWriter, status int, e Emplacement) {
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(e.Version)))
	writeJSON(w, status, e)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
