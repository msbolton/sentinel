package apikeys

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/classification"
	"github.com/sentinel/go-common/httputil"

	"github.com/sentinel/api-gateway/internal/auth"
)

const (
	maxNameLength  = 100
	maxExpiryDays  = 3650
	defaultKeyRole = auth.RoleViewer
)

// Handler serves /api/v1/auth/api-keys for the authenticated user.
type Handler struct {
	store  Store
	logger *zap.Logger
	now    func() time.Time
}

// NewHandler builds the handler.
func NewHandler(store Store, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{store: store, logger: logger, now: time.Now}
}

// RegisterRoutes mounts the routes on the protected API mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/auth/api-keys", h.handleList)
	mux.HandleFunc("POST /api/v1/auth/api-keys", h.handleCreate)
	mux.HandleFunc("DELETE /api/v1/auth/api-keys/{id}", h.handleRevoke)
}

type createRequest struct {
	Name           string `json:"name"`
	Classification string `json:"classification"`
	ExpiresInDays  int    `json:"expiresInDays"`
}

// user returns the caller, refusing principals that are themselves keys.
func (h *Handler) user(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	u, ok := auth.UserFrom(r.Context())
	if !ok {
		httputil.Error(w, http.StatusUnauthorized, "Unauthorized")
		return nil, false
	}
	if u.ViaAPIKey {
		httputil.Error(w, http.StatusForbidden, "API keys cannot manage API keys; sign in to do that")
		return nil, false
	}
	return u, true
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	u, ok := h.user(w, r)
	if !ok {
		return
	}
	keys, err := h.store.ListByUser(r.Context(), u.UserID)
	if err != nil {
		h.logger.Error("list api keys failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to list API keys")
		return
	}
	httputil.JSON(w, http.StatusOK, keys)
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	u, ok := h.user(w, r)
	if !ok {
		return
	}
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.Error(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > maxNameLength {
		httputil.Error(w, http.StatusBadRequest, "name is required (at most 100 characters)")
		return
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > maxExpiryDays {
		httputil.Error(w, http.StatusBadRequest, "expiresInDays must be between 0 and 3650")
		return
	}
	// A key never outranks its owner.
	level := classification.Normalize(u.ClassificationLevel)
	if req.Classification != "" {
		if !classification.Valid(req.Classification) {
			httputil.Error(w, http.StatusBadRequest, "classification must be UNCLASSIFIED, CONFIDENTIAL, SECRET or TOP_SECRET")
			return
		}
		level = classification.Min(req.Classification, level)
	}
	secret, prefix, hash, err := Generate()
	if err != nil {
		h.logger.Error("generating api key failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to create API key")
		return
	}
	k := Key{UserID: u.UserID, Username: u.Username, Name: req.Name, Prefix: prefix, Roles: []string{defaultKeyRole}, Classification: level}
	if req.ExpiresInDays > 0 {
		exp := h.now().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		k.ExpiresAt = &exp
	}
	stored, err := h.store.Create(r.Context(), k, hash)
	if err != nil {
		h.logger.Error("create api key failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to create API key")
		return
	}
	h.logger.Info("api key created", zap.String("userId", u.UserID), zap.String("keyId", stored.ID), zap.String("classification", level))
	httputil.JSON(w, http.StatusCreated, Created{Key: stored, Secret: secret})
}

func (h *Handler) handleRevoke(w http.ResponseWriter, r *http.Request) {
	u, ok := h.user(w, r)
	if !ok {
		return
	}
	owner := u.UserID
	if u.HasAnyRole(auth.RoleAdmin) {
		owner = "" // admins may revoke anyone's key
	}
	if err := h.store.Revoke(r.Context(), r.PathValue("id"), owner); err != nil {
		if errors.Is(err, ErrInvalidKey) {
			httputil.Error(w, http.StatusNotFound, "API key not found")
			return
		}
		h.logger.Error("revoke api key failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to revoke API key")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
