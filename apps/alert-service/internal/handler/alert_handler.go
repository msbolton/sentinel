package handler

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sentinel/alert-service/internal/store"
	"github.com/sentinel/go-common/classification"
	"github.com/sentinel/go-common/httputil"
	"github.com/sentinel/go-common/middleware"
	"go.uber.org/zap"
)

// AlertHandler handles alert HTTP endpoints.
type AlertHandler struct {
	store  *store.AlertStore
	logger *zap.Logger
}

// NewAlertHandler creates a new alert handler.
func NewAlertHandler(store *store.AlertStore, logger *zap.Logger) *AlertHandler {
	return &AlertHandler{store: store, logger: logger}
}

// RegisterRoutes registers alert endpoints on the mux.
func (h *AlertHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/alerts", h.handleGetAlerts)
	mux.HandleFunc("GET /api/v1/alerts/{id}", h.handleGetAlert)
	mux.HandleFunc("PATCH /api/v1/alerts/{id}/acknowledge", h.handleAcknowledge)
	mux.HandleFunc("PATCH /api/v1/alerts/{id}/resolve", h.handleResolve)
	mux.HandleFunc("POST /api/v1/alerts/rules", h.handleCreateRule)
	mux.HandleFunc("GET /api/v1/alerts/rules", h.handleGetRules)
	mux.HandleFunc("PUT /api/v1/alerts/rules/{id}", h.handleUpdateRule)
	mux.HandleFunc("DELETE /api/v1/alerts/rules/{id}", h.handleDeleteRule)
}

// ceilingOf is the caller's clearance as propagated by the gateway; a
// missing header fails closed to UNCLASSIFIED.
func ceilingOf(r *http.Request) string {
	return classification.Normalize(middleware.Classification(r.Context()))
}

// findVisible fetches an alert the caller is cleared to see; alerts above
// the caller's clearance do not exist for them.
func (h *AlertHandler) findVisible(w http.ResponseWriter, r *http.Request, id string) (*store.AlertRecord, bool) {
	alert, err := h.store.GetAlert(r.Context(), id)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			httputil.Error(w, http.StatusNotFound, "Alert not found")
			return nil, false
		}
		h.logger.Error("get alert failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to get alert")
		return nil, false
	}
	if classification.Rank(alert.Classification) > classification.Rank(ceilingOf(r)) {
		httputil.Error(w, http.StatusNotFound, "Alert not found")
		return nil, false
	}
	return alert, true
}

func (h *AlertHandler) handleGetAlerts(w http.ResponseWriter, r *http.Request) {
	params := parseAlertQuery(r.URL.Query())
	params.Ceiling = ceilingOf(r)

	result, err := h.store.GetAlerts(r.Context(), params)
	if err != nil {
		h.logger.Error("get alerts failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to get alerts")
		return
	}

	httputil.JSON(w, http.StatusOK, result)
}

func (h *AlertHandler) handleGetAlert(w http.ResponseWriter, r *http.Request) {
	alert, ok := h.findVisible(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	httputil.JSON(w, http.StatusOK, alert)
}

func (h *AlertHandler) handleAcknowledge(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := h.findVisible(w, r, id); !ok {
		return
	}

	var body struct {
		UserID string `json:"userId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.UserID == "" {
		httputil.Error(w, http.StatusBadRequest, "userId is required")
		return
	}

	if err := h.store.AcknowledgeAlert(r.Context(), id, body.UserID); err != nil {
		if strings.Contains(err.Error(), "not found") {
			httputil.Error(w, http.StatusNotFound, "Alert not found")
			return
		}
		h.logger.Error("acknowledge alert failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to acknowledge alert")
		return
	}

	alert, _ := h.store.GetAlert(r.Context(), id)
	httputil.JSON(w, http.StatusOK, alert)
}

func (h *AlertHandler) handleResolve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := h.findVisible(w, r, id); !ok {
		return
	}

	if err := h.store.ResolveAlert(r.Context(), id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			httputil.Error(w, http.StatusNotFound, "Alert not found")
			return
		}
		h.logger.Error("resolve alert failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to resolve alert")
		return
	}

	alert, _ := h.store.GetAlert(r.Context(), id)
	httputil.JSON(w, http.StatusOK, alert)
}

type createRuleRequest struct {
	Name                 string                 `json:"name"`
	RuleType             string                 `json:"ruleType"`
	Config               map[string]interface{} `json:"config"`
	MonitoredEntityTypes []string               `json:"monitoredEntityTypes"`
	Severity             *string                `json:"severity"`
	Enabled              *bool                  `json:"enabled"`
}

func (h *AlertHandler) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	var req createRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.Error(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Name == "" || req.RuleType == "" || req.Config == nil {
		httputil.Error(w, http.StatusBadRequest, "name, ruleType, and config are required")
		return
	}

	severity := "MEDIUM"
	if req.Severity != nil {
		severity = *req.Severity
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	rule, err := h.store.CreateRule(r.Context(), store.CreateRuleParams{
		Name:                 req.Name,
		RuleType:             req.RuleType,
		Config:               req.Config,
		MonitoredEntityTypes: req.MonitoredEntityTypes,
		Severity:             severity,
		Enabled:              enabled,
	})
	if err != nil {
		h.logger.Error("create rule failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to create rule")
		return
	}

	httputil.JSON(w, http.StatusCreated, rule)
}

func (h *AlertHandler) handleGetRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.store.GetRules(r.Context())
	if err != nil {
		h.logger.Error("get rules failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to get rules")
		return
	}

	httputil.JSON(w, http.StatusOK, rules)
}

type updateRuleRequest struct {
	Name                 *string                `json:"name"`
	RuleType             *string                `json:"ruleType"`
	Config               map[string]interface{} `json:"config"`
	MonitoredEntityTypes []string               `json:"monitoredEntityTypes"`
	Severity             *string                `json:"severity"`
	Enabled              *bool                  `json:"enabled"`
}

func (h *AlertHandler) handleUpdateRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req updateRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.Error(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	rule, err := h.store.UpdateRule(r.Context(), id, store.UpdateRuleParams{
		Name:                 req.Name,
		RuleType:             req.RuleType,
		Config:               req.Config,
		MonitoredEntityTypes: req.MonitoredEntityTypes,
		Severity:             req.Severity,
		Enabled:              req.Enabled,
	})
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			httputil.Error(w, http.StatusNotFound, "Rule not found")
			return
		}
		h.logger.Error("update rule failed", zap.Error(err))
		httputil.Error(w, http.StatusInternalServerError, "Failed to update rule")
		return
	}

	httputil.JSON(w, http.StatusOK, rule)
}

func (h *AlertHandler) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := h.store.DeleteRule(r.Context(), id)
	code, message := ruleDeleteStatus(err)
	if code == http.StatusInternalServerError {
		h.logger.Error("delete rule failed", zap.String("ruleId", id), zap.Error(err))
	}
	if code != http.StatusNoContent {
		httputil.Error(w, code, message)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ruleDeleteStatus maps a delete's outcome to the response: 204 on success,
// 404 when the store reports no such rule, 500 otherwise.
func ruleDeleteStatus(err error) (int, string) {
	switch {
	case err == nil:
		return http.StatusNoContent, ""
	case strings.Contains(err.Error(), "not found"):
		return http.StatusNotFound, "Rule not found"
	default:
		return http.StatusInternalServerError, "Failed to delete rule"
	}
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

// parseAlertQuery reads list filters: severity, entityId, types (repeated or
// comma-separated), acknowledged, and paging.
func parseAlertQuery(q url.Values) store.AlertQueryParams {
	params := store.AlertQueryParams{
		Page:     parseIntDefault(q.Get("page"), 1),
		PageSize: parseIntDefault(q.Get("pageSize"), 20),
	}
	if v := q.Get("severity"); v != "" {
		params.Severity = &v
	}
	if v := q.Get("entityId"); v != "" {
		params.EntityID = &v
	}
	for _, t := range q["types"] {
		for _, part := range strings.Split(t, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				params.Types = append(params.Types, trimmed)
			}
		}
	}
	if v := q.Get("acknowledged"); v != "" {
		ack := v == "true"
		params.Acknowledged = &ack
	}
	return params
}
