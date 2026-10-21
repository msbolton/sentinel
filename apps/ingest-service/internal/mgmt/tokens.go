package mgmt

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/classification"
	"github.com/sentinel/go-common/middleware"
	"github.com/sentinel/go-common/secrettoken"

	"github.com/sentinel/ingest-service/internal/drivers"
	"github.com/sentinel/ingest-service/internal/ingesttoken"
	"github.com/sentinel/ingest-service/internal/source/driver/grpcin"
)

// TokenStore is what the token routes need of the ingest token store.
type TokenStore interface {
	Create(ctx context.Context, t ingesttoken.Token, hash []byte) (ingesttoken.Token, error)
	List(ctx context.Context, feedID string) ([]ingesttoken.Token, error)
	Revoke(ctx context.Context, feedID, id string, at time.Time) error
}

type createTokenRequest struct {
	Name           string     `json:"name"`
	Classification string     `json:"classification"`
	ExpiresAt      *time.Time `json:"expiresAt"`
}

// createdToken is the one response that carries the secret.
type createdToken struct {
	ingesttoken.Token
	Secret string `json:"token"`
}

// tokenFeed loads the feed a token route names and holds the caller to it:
// operator or admin, a store configured, and a clearance that reaches the
// feed's classification. It writes the refusal and returns ok=false.
// Creating a token also needs a grpc feed.
func (h *Handler) tokenFeed(w http.ResponseWriter, r *http.Request) (feedID, level string, ok bool) {
	if !isOperator(r) {
		writeError(w, http.StatusForbidden, "operator or admin role required")
		return "", "", false
	}
	if h.Tokens == nil {
		writeError(w, http.StatusNotImplemented, "ingest tokens are not enabled")
		return "", "", false
	}
	f, err := h.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return "", "", false
	}
	if !clearedFor(r, f) {
		writeError(w, http.StatusForbidden, "your clearance does not reach this connection's classification")
		return "", "", false
	}
	if f.Driver != grpcin.ID && r.Method == http.MethodPost {
		writeProblems(w, []drivers.Problem{{Path: "/driver", Message: "ingest tokens are for grpc connections"}})
		return "", "", false
	}
	return f.ID, classification.Normalize(f.Common().Classification), true
}

func (h *Handler) listTokens(w http.ResponseWriter, r *http.Request) {
	feedID, _, ok := h.tokenFeed(w, r)
	if !ok {
		return
	}
	list, err := h.Tokens.List(r.Context(), feedID)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Handler) createToken(w http.ResponseWriter, r *http.Request) {
	feedID, feedLevel, ok := h.tokenFeed(w, r)
	if !ok {
		return
	}
	var req createTokenRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var problems []drivers.Problem
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 255 {
		problems = append(problems, drivers.Problem{Path: "/name", Message: "is required, at most 255 bytes"})
	} else if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		problems = append(problems, drivers.Problem{Path: "/name", Message: "must not contain control characters"})
	}
	level := feedLevel
	if req.Classification != "" {
		level = strings.ToUpper(strings.TrimSpace(req.Classification))
	}
	switch {
	case !classification.Valid(level):
		problems = append(problems, drivers.Problem{Path: "/classification", Message: "must be UNCLASSIFIED, CONFIDENTIAL, SECRET or TOP_SECRET"})
	case classification.Rank(level) < classification.Rank(feedLevel):
		problems = append(problems, drivers.Problem{Path: "/classification", Message: "is below the connection's classification, so every report would be refused"})
	case classification.Rank(level) > classification.Rank(middleware.Classification(r.Context())):
		problems = append(problems, drivers.Problem{Path: "/classification", Message: "is above your clearance"})
	}
	if req.ExpiresAt != nil && !req.ExpiresAt.After(time.Now()) {
		problems = append(problems, drivers.Problem{Path: "/expiresAt", Message: "must be in the future"})
	}
	if len(problems) > 0 {
		writeProblems(w, problems)
		return
	}
	secret, display, hash, err := secrettoken.Generate(ingesttoken.Prefix)
	if err != nil {
		h.fail(w, err)
		return
	}
	t, err := h.Tokens.Create(r.Context(), ingesttoken.Token{FeedID: feedID, Name: name, Prefix: display,
		Classification: classification.Normalize(level), CreatedBy: middleware.UserID(r.Context()), ExpiresAt: req.ExpiresAt}, hash)
	if errors.Is(err, ingesttoken.ErrNotFound) {
		// The feed was deleted after tokenFeed read it: the foreign key refused the row.
		writeError(w, http.StatusNotFound, "feed not found")
		return
	}
	if err != nil {
		h.fail(w, err)
		return
	}
	// The log carries the id and the display prefix, never the secret.
	h.logger.Info("ingest token created", zap.String("feed", feedID), zap.String("token", t.ID), zap.String("prefix", t.Prefix))
	writeJSON(w, http.StatusCreated, createdToken{Token: t, Secret: secret})
}

func (h *Handler) revokeToken(w http.ResponseWriter, r *http.Request) {
	feedID, _, ok := h.tokenFeed(w, r)
	if !ok {
		return
	}
	if err := h.Tokens.Revoke(r.Context(), feedID, r.PathValue("tokenId"), time.Now()); err != nil {
		h.fail(w, err)
		return
	}
	h.logger.Info("ingest token revoked", zap.String("feed", feedID), zap.String("token", r.PathValue("tokenId")))
	w.WriteHeader(http.StatusNoContent)
}
