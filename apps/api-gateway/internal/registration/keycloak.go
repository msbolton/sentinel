// Package registration ports the self-service registration and user
// administration flow: users register as disabled Keycloak accounts, admins
// approve (enable + assign roles) or reject (delete + notify) them.
package registration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ValidClassifications are the realm roles that encode clearance.
var ValidClassifications = []string{"classification-u", "classification-c", "classification-s", "classification-ts"}

// classificationPriority lists clearance roles highest first.
var classificationPriority = []string{"classification-ts", "classification-s", "classification-c", "classification-u"}

// IsValidClassification reports whether s is an assignable clearance role.
func IsValidClassification(s string) bool {
	for _, c := range ValidClassifications {
		if c == s {
			return true
		}
	}
	return false
}

// APIError carries an HTTP status to surface to the caller.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return e.Message }

// CreateUserInput is a registration request.
type CreateUserInput struct {
	Username      string
	Email         string
	FirstName     string
	LastName      string
	Password      string
	Organization  string
	Justification string
}

// PendingUser is a registered but not yet approved account.
type PendingUser struct {
	ID               string `json:"id"`
	Username         string `json:"username"`
	Email            string `json:"email"`
	FirstName        string `json:"firstName"`
	LastName         string `json:"lastName"`
	Organization     string `json:"organization"`
	Justification    string `json:"justification"`
	RegistrationDate string `json:"registrationDate"`
}

// ActiveUser is an enabled account with its clearance and realm roles.
type ActiveUser struct {
	ID                  string   `json:"id"`
	Username            string   `json:"username"`
	Email               string   `json:"email"`
	FirstName           string   `json:"firstName"`
	LastName            string   `json:"lastName"`
	ClassificationLevel *string  `json:"classificationLevel"`
	Roles               []string `json:"roles"`
}

// RejectedUser identifies who to notify after a rejection.
type RejectedUser struct {
	Email     string
	FirstName string
}

// AdminAPI is the subset of Keycloak administration the handler depends on.
type AdminAPI interface {
	CreateUser(ctx context.Context, in CreateUserInput) error
	PendingRegistrations(ctx context.Context) ([]PendingUser, error)
	ApproveUser(ctx context.Context, userID, classification string) error
	RejectUser(ctx context.Context, userID string) (RejectedUser, error)
	UpdateClassification(ctx context.Context, userID, classification string) error
	ActiveUsers(ctx context.Context) ([]ActiveUser, error)
}

// KeycloakClient talks to the Keycloak Admin REST API with a cached
// client-credentials token.
type KeycloakClient struct {
	baseURL      string
	realm        string
	clientID     string
	clientSecret string
	http         *http.Client
	logger       *zap.Logger
	now          func() time.Time

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

// NewKeycloakClient creates an admin client for the given realm.
func NewKeycloakClient(baseURL, realm, clientID, clientSecret string, logger *zap.Logger) *KeycloakClient {
	return &KeycloakClient{
		baseURL:      strings.TrimRight(baseURL, "/"),
		realm:        realm,
		clientID:     clientID,
		clientSecret: clientSecret,
		http:         &http.Client{Timeout: 15 * time.Second},
		logger:       logger,
		now:          time.Now,
	}
}

type keycloakUser struct {
	ID         string              `json:"id"`
	Username   string              `json:"username"`
	Email      string              `json:"email"`
	FirstName  string              `json:"firstName"`
	LastName   string              `json:"lastName"`
	Enabled    bool                `json:"enabled"`
	Attributes map[string][]string `json:"attributes,omitempty"`
}

type keycloakRole struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// getToken returns a cached service-account token, refreshing 30 s before expiry.
func (c *KeycloakClient) getToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && c.now().Before(c.expiresAt.Add(-30*time.Second)) {
		return c.token, nil
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/realms/"+c.realm+"/protocol/openid-connect/token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		c.logger.Error("keycloak token request failed", zap.Error(err))
		return "", &APIError{Status: http.StatusServiceUnavailable, Message: "Failed to obtain admin token"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		c.logger.Error("keycloak token request rejected", zap.Int("status", resp.StatusCode), zap.ByteString("body", body))
		return "", &APIError{Status: http.StatusServiceUnavailable, Message: "Failed to obtain admin token"}
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil || tok.AccessToken == "" {
		return "", &APIError{Status: http.StatusServiceUnavailable, Message: "Failed to obtain admin token"}
	}
	c.token = tok.AccessToken
	c.expiresAt = c.now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	return c.token, nil
}

// adminRequest performs an authenticated call against /admin/realms/{realm}{path}.
func (c *KeycloakClient) adminRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, err
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/admin/realms/"+c.realm+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.logger.Error("keycloak admin request failed", zap.String("method", method), zap.String("path", path), zap.Error(err))
		return nil, &APIError{Status: http.StatusServiceUnavailable, Message: "Keycloak unavailable"}
	}
	return resp, nil
}

func (c *KeycloakClient) logFailure(what string, resp *http.Response) {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	c.logger.Error(what, zap.Int("status", resp.StatusCode), zap.ByteString("body", body))
}

func decode(resp *http.Response, dst interface{}) error {
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(dst)
}

func ok(resp *http.Response) bool { return resp.StatusCode >= 200 && resp.StatusCode < 300 }

// CreateUser registers a disabled user carrying the registration attributes.
func (c *KeycloakClient) CreateUser(ctx context.Context, in CreateUserInput) error {
	rep := map[string]interface{}{
		"username":  in.Username,
		"email":     in.Email,
		"firstName": in.FirstName,
		"lastName":  in.LastName,
		"enabled":   false,
		"credentials": []map[string]interface{}{
			{"type": "password", "value": in.Password, "temporary": false},
		},
		"attributes": map[string][]string{
			"organization":     {in.Organization},
			"justification":    {in.Justification},
			"registrationDate": {c.now().UTC().Format(time.RFC3339)},
		},
	}
	resp, err := c.adminRequest(ctx, http.MethodPost, "/users", rep)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		detail := "Username or email already exists"
		var body struct {
			ErrorMessage string `json:"errorMessage"`
		}
		if json.NewDecoder(resp.Body).Decode(&body) == nil && body.ErrorMessage != "" {
			detail = body.ErrorMessage
		}
		return &APIError{Status: http.StatusConflict, Message: detail}
	}
	if !ok(resp) {
		c.logFailure("failed to create user", resp)
		return &APIError{Status: http.StatusInternalServerError, Message: "Failed to create user"}
	}
	return nil
}

// PendingRegistrations lists disabled users that carry a registrationDate attribute.
func (c *KeycloakClient) PendingRegistrations(ctx context.Context) ([]PendingUser, error) {
	resp, err := c.adminRequest(ctx, http.MethodGet, "/users?enabled=false&max=100", nil)
	if err != nil {
		return nil, err
	}
	if !ok(resp) {
		c.logFailure("failed to fetch pending registrations", resp)
		resp.Body.Close()
		return nil, &APIError{Status: http.StatusInternalServerError, Message: "Failed to fetch pending registrations"}
	}
	var users []keycloakUser
	if err := decode(resp, &users); err != nil {
		return nil, &APIError{Status: http.StatusInternalServerError, Message: "Failed to fetch pending registrations"}
	}
	out := []PendingUser{}
	for _, u := range users {
		reg := u.Attributes["registrationDate"]
		if len(reg) == 0 {
			continue
		}
		out = append(out, PendingUser{
			ID: u.ID, Username: u.Username, Email: u.Email, FirstName: u.FirstName, LastName: u.LastName,
			Organization:     first(u.Attributes["organization"]),
			Justification:    first(u.Attributes["justification"]),
			RegistrationDate: reg[0],
		})
	}
	return out, nil
}

// ApproveUser enables the account, assigns sentinel-viewer plus the clearance
// role, clears registrationDate, and triggers the verification email. If role
// assignment fails the account is re-disabled so it stays visible as pending.
// Every update sends the user's whole representation, as read: Keycloak 26
// takes it as the complete profile and would clear a missing email or name,
// and the verification email then fails with "User email missing".
func (c *KeycloakClient) ApproveUser(ctx context.Context, userID, classification string) error {
	rep, err := c.getUserRep(ctx, userID)
	if err != nil {
		return err
	}

	rep["enabled"] = true
	if err := c.putUser(ctx, userID, rep); err != nil {
		c.logger.Error("failed to enable user", zap.String("userId", userID), zap.Error(err))
		return &APIError{Status: http.StatusInternalServerError, Message: "Failed to approve user"}
	}

	if err := c.assignRealmRoles(ctx, userID, []string{"sentinel-viewer", classification}); err != nil {
		c.logger.Error("role assignment failed, re-disabling user", zap.String("userId", userID), zap.Error(err))
		rep["enabled"] = false
		_ = c.putUser(ctx, userID, rep)
		return &APIError{Status: http.StatusInternalServerError, Message: "Failed to assign roles; user has been re-disabled"}
	}

	if attrs, isMap := rep["attributes"].(map[string]interface{}); isMap {
		delete(attrs, "registrationDate")
	}
	if err := c.putUser(ctx, userID, rep); err != nil {
		c.logger.Error("failed to clear registrationDate (non-fatal)", zap.String("userId", userID), zap.Error(err))
	}

	resp, err := c.adminRequest(ctx, http.MethodPut, "/users/"+userID+"/execute-actions-email", []string{"VERIFY_EMAIL"})
	if err == nil {
		if !ok(resp) {
			c.logFailure("failed to send verification email (non-fatal)", resp)
		}
		resp.Body.Close()
	}
	return nil
}

// RejectUser deletes the account and returns contact details for the notice.
func (c *KeycloakClient) RejectUser(ctx context.Context, userID string) (RejectedUser, error) {
	user, err := c.getUser(ctx, userID)
	if err != nil {
		return RejectedUser{}, err
	}
	resp, err := c.adminRequest(ctx, http.MethodDelete, "/users/"+userID, nil)
	if err != nil {
		return RejectedUser{}, err
	}
	defer resp.Body.Close()
	if !ok(resp) {
		c.logFailure("failed to delete user", resp)
		return RejectedUser{}, &APIError{Status: http.StatusInternalServerError, Message: "Failed to reject user"}
	}
	return RejectedUser{Email: user.Email, FirstName: user.FirstName}, nil
}

// UpdateClassification swaps the user's clearance role.
func (c *KeycloakClient) UpdateClassification(ctx context.Context, userID, classification string) error {
	if !IsValidClassification(classification) {
		return &APIError{Status: http.StatusBadRequest, Message: "Invalid classification level"}
	}
	current, err := c.userRealmRoles(ctx, userID)
	if err != nil {
		return &APIError{Status: http.StatusInternalServerError, Message: "Failed to fetch user roles"}
	}
	var existing []keycloakRole
	for _, r := range current {
		if IsValidClassification(r.Name) {
			existing = append(existing, r)
		}
	}
	if len(existing) > 0 {
		resp, err := c.adminRequest(ctx, http.MethodDelete, "/users/"+userID+"/role-mappings/realm", existing)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if !ok(resp) {
			return &APIError{Status: http.StatusInternalServerError, Message: "Failed to remove existing classification"}
		}
	}
	if err := c.assignRealmRoles(ctx, userID, []string{classification}); err != nil {
		return &APIError{Status: http.StatusInternalServerError, Message: "Failed to assign classification"}
	}
	return nil
}

// ActiveUsers lists enabled accounts (excluding service accounts) with roles.
func (c *KeycloakClient) ActiveUsers(ctx context.Context) ([]ActiveUser, error) {
	resp, err := c.adminRequest(ctx, http.MethodGet, "/users?enabled=true&max=100", nil)
	if err != nil {
		return nil, err
	}
	if !ok(resp) {
		resp.Body.Close()
		return nil, &APIError{Status: http.StatusInternalServerError, Message: "Failed to fetch users"}
	}
	var users []keycloakUser
	if err := decode(resp, &users); err != nil {
		return nil, &APIError{Status: http.StatusInternalServerError, Message: "Failed to fetch users"}
	}

	out := []ActiveUser{}
	for _, u := range users {
		if strings.HasPrefix(u.Username, "service-account-") {
			continue
		}
		names := []string{}
		if roles, err := c.userRealmRoles(ctx, u.ID); err == nil {
			for _, r := range roles {
				names = append(names, r.Name)
			}
		}
		var level *string
		for _, p := range classificationPriority {
			if contains(names, p) {
				l := p
				level = &l
				break
			}
		}
		out = append(out, ActiveUser{
			ID: u.ID, Username: u.Username, Email: u.Email, FirstName: u.FirstName, LastName: u.LastName,
			ClassificationLevel: level, Roles: names,
		})
	}
	return out, nil
}

func (c *KeycloakClient) getUser(ctx context.Context, userID string) (*keycloakUser, error) {
	resp, err := c.adminRequest(ctx, http.MethodGet, "/users/"+userID, nil)
	if err != nil {
		return nil, err
	}
	if !ok(resp) {
		resp.Body.Close()
		return nil, &APIError{Status: http.StatusNotFound, Message: "User not found"}
	}
	var u keycloakUser
	if err := decode(resp, &u); err != nil {
		return nil, &APIError{Status: http.StatusInternalServerError, Message: "Failed to read user"}
	}
	return &u, nil
}

// getUserRep reads a user's representation as Keycloak returns it, every field
// kept, for an update that must send it back whole.
func (c *KeycloakClient) getUserRep(ctx context.Context, userID string) (map[string]interface{}, error) {
	resp, err := c.adminRequest(ctx, http.MethodGet, "/users/"+userID, nil)
	if err != nil {
		return nil, err
	}
	if !ok(resp) {
		resp.Body.Close()
		return nil, &APIError{Status: http.StatusNotFound, Message: "User not found"}
	}
	var rep map[string]interface{}
	if err := decode(resp, &rep); err != nil || rep == nil {
		return nil, &APIError{Status: http.StatusInternalServerError, Message: "Failed to read user"}
	}
	return rep, nil
}

func (c *KeycloakClient) putUser(ctx context.Context, userID string, rep map[string]interface{}) error {
	resp, err := c.adminRequest(ctx, http.MethodPut, "/users/"+userID, rep)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if !ok(resp) {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("PUT /users/%s: %d %s", userID, resp.StatusCode, body)
	}
	return nil
}

func (c *KeycloakClient) userRealmRoles(ctx context.Context, userID string) ([]keycloakRole, error) {
	resp, err := c.adminRequest(ctx, http.MethodGet, "/users/"+userID+"/role-mappings/realm", nil)
	if err != nil {
		return nil, err
	}
	if !ok(resp) {
		resp.Body.Close()
		return nil, fmt.Errorf("role mappings: %d", resp.StatusCode)
	}
	var roles []keycloakRole
	if err := decode(resp, &roles); err != nil {
		return nil, err
	}
	return roles, nil
}

// assignRealmRoles resolves role names to representations and maps them to the user.
func (c *KeycloakClient) assignRealmRoles(ctx context.Context, userID string, names []string) error {
	resp, err := c.adminRequest(ctx, http.MethodGet, "/roles", nil)
	if err != nil {
		return err
	}
	if !ok(resp) {
		resp.Body.Close()
		return fmt.Errorf("fetch roles: %d", resp.StatusCode)
	}
	var all []keycloakRole
	if err := decode(resp, &all); err != nil {
		return err
	}
	var toAssign []keycloakRole
	for _, r := range all {
		if contains(names, r.Name) {
			toAssign = append(toAssign, r)
		}
	}
	if len(toAssign) == 0 {
		return errors.New("required roles " + strings.Join(names, ", ") + " not found")
	}
	resp, err = c.adminRequest(ctx, http.MethodPost, "/users/"+userID+"/role-mappings/realm", toAssign)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if !ok(resp) {
		return fmt.Errorf("assign roles: %d", resp.StatusCode)
	}
	return nil
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
