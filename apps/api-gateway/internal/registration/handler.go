package registration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/httputil"
)

// Handler serves the /api/v1/auth routes.
type Handler struct {
	admin   AdminAPI
	mailer  Mailer
	limiter *IPLimiter
	logger  *zap.Logger
}

// NewHandler creates the registration handler. limiter may be nil.
func NewHandler(admin AdminAPI, mailer Mailer, limiter *IPLimiter, logger *zap.Logger) *Handler {
	if mailer == nil {
		mailer = NopMailer{}
	}
	return &Handler{admin: admin, mailer: mailer, limiter: limiter, logger: logger}
}

// RegisterPublicRoutes mounts the unauthenticated registration endpoint.
func (h *Handler) RegisterPublicRoutes(mux *http.ServeMux) {
	var register http.Handler = http.HandlerFunc(h.handleRegister)
	if h.limiter != nil {
		register = h.limiter.Middleware(register)
	}
	mux.Handle("POST /api/v1/auth/register", register)
}

// RegisterAdminRoutes mounts the admin endpoints. The caller applies auth and
// the sentinel-admin role policy.
func (h *Handler) RegisterAdminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/auth/pending-registrations", h.handlePending)
	mux.HandleFunc("POST /api/v1/auth/approve-registration/{userId}", h.handleApprove)
	mux.HandleFunc("POST /api/v1/auth/reject-registration/{userId}", h.handleReject)
	mux.HandleFunc("GET /api/v1/auth/users", h.handleUsers)
	mux.HandleFunc("PUT /api/v1/auth/users/{userId}/classification", h.handleClassification)
}

type registerRequest struct {
	Username        string `json:"username"`
	Email           string `json:"email"`
	Password        string `json:"password"`
	ConfirmPassword string `json:"confirmPassword"`
	FirstName       string `json:"firstName"`
	LastName        string `json:"lastName"`
	Organization    string `json:"organization"`
	Justification   string `json:"justification"`
}

type classificationRequest struct {
	ClassificationLevel string `json:"classificationLevel"`
}

func (h *Handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.Error(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	switch {
	case strings.TrimSpace(req.Username) == "" || strings.TrimSpace(req.Email) == "":
		httputil.Error(w, http.StatusBadRequest, "username and email are required")
		return
	case req.Password != req.ConfirmPassword:
		httputil.Error(w, http.StatusBadRequest, "Passwords do not match")
		return
	case len(req.Password) < 8:
		httputil.Error(w, http.StatusBadRequest, "Password must be at least 8 characters")
		return
	}

	err := h.admin.CreateUser(r.Context(), CreateUserInput{
		Username: req.Username, Email: req.Email, FirstName: req.FirstName, LastName: req.LastName,
		Password: req.Password, Organization: req.Organization, Justification: req.Justification,
	})
	if err != nil {
		h.respondError(w, err)
		return
	}
	h.logger.Info("registration submitted", zap.String("username", req.Username))
	httputil.JSON(w, http.StatusCreated, map[string]string{
		"message": "Registration submitted successfully. Pending admin approval.",
	})
}

func (h *Handler) handlePending(w http.ResponseWriter, r *http.Request) {
	users, err := h.admin.PendingRegistrations(r.Context())
	if err != nil {
		h.respondError(w, err)
		return
	}
	httputil.JSON(w, http.StatusOK, users)
}

func (h *Handler) handleApprove(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("userId")
	level := "classification-u"
	if r.Body != nil && r.ContentLength != 0 {
		var req classificationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.Error(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		if req.ClassificationLevel != "" {
			level = req.ClassificationLevel
		}
	}
	if !IsValidClassification(level) {
		httputil.Error(w, http.StatusBadRequest, "Invalid classification level")
		return
	}
	if err := h.admin.ApproveUser(r.Context(), userID, level); err != nil {
		h.respondError(w, err)
		return
	}
	h.logger.Info("user approved", zap.String("userId", userID), zap.String("classification", level))
	httputil.JSON(w, http.StatusCreated, map[string]string{"message": fmt.Sprintf("User %s has been approved.", userID)})
}

func (h *Handler) handleReject(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("userId")
	rejected, err := h.admin.RejectUser(r.Context(), userID)
	if err != nil {
		h.respondError(w, err)
		return
	}
	body := fmt.Sprintf("Dear %s,\n\nYour registration request for Sentinel has been reviewed and unfortunately rejected.\n\nIf you believe this is an error, please contact your administrator.\n\nSentinel Security Team", rejected.FirstName)
	if err := h.mailer.Send(context.WithoutCancel(r.Context()), rejected.Email, "Sentinel — Registration Request Rejected", body); err != nil {
		h.logger.Warn("rejection notice not sent", zap.String("userId", userID), zap.String("email", rejected.Email), zap.Error(err))
	} else {
		h.logger.Info("user rejected, notice sent", zap.String("userId", userID), zap.String("email", rejected.Email))
	}
	httputil.JSON(w, http.StatusCreated, map[string]string{"message": fmt.Sprintf("User %s has been rejected.", userID)})
}

func (h *Handler) handleUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.admin.ActiveUsers(r.Context())
	if err != nil {
		h.respondError(w, err)
		return
	}
	httputil.JSON(w, http.StatusOK, users)
}

func (h *Handler) handleClassification(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("userId")
	var req classificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !IsValidClassification(req.ClassificationLevel) {
		httputil.Error(w, http.StatusBadRequest, "Invalid classification level")
		return
	}
	if err := h.admin.UpdateClassification(r.Context(), userID, req.ClassificationLevel); err != nil {
		h.respondError(w, err)
		return
	}
	h.logger.Info("classification updated", zap.String("userId", userID), zap.String("classification", req.ClassificationLevel))
	httputil.JSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("Classification updated for user %s.", userID)})
}

func (h *Handler) respondError(w http.ResponseWriter, err error) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.Status >= 500 {
			h.logger.Error("registration admin call failed", zap.Int("status", apiErr.Status), zap.String("message", apiErr.Message))
		}
		httputil.Error(w, apiErr.Status, apiErr.Message)
		return
	}
	h.logger.Error("registration failed", zap.Error(err))
	httputil.Error(w, http.StatusInternalServerError, "Internal server error")
}
