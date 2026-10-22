package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/api-gateway/internal/auth"
	"github.com/sentinel/api-gateway/internal/config"
	"github.com/sentinel/api-gateway/internal/registration"
	"github.com/sentinel/api-gateway/internal/server"
	"github.com/sentinel/api-gateway/internal/stream"
)

// routeKeycloak is just enough Keycloak for a guest to start.
type routeKeycloak struct{}

func (routeKeycloak) CreateGuestUser(context.Context, string, string, []string) (string, error) {
	return "kc-guest-1", nil
}
func (routeKeycloak) DeleteUser(context.Context, string) error { return nil }
func (routeKeycloak) UsersWithRole(context.Context, string) ([]registration.GuestUser, error) {
	return nil, nil
}
func (routeKeycloak) PasswordGrant(context.Context, string, string, string) (registration.TokenResponse, error) {
	return registration.TokenResponse{AccessToken: "a", RefreshToken: "r", ExpiresIn: 300}, nil
}

// The demo's routes mounted exactly as main mounts them, behind the real
// auth middleware (no dev bypass): session and status need no token, the
// position report does.
func TestDemoRoutesThroughTheGateway(t *testing.T) {
	logger := zap.NewNop()
	cfg := config.DemoConfig{
		Enabled:            true,
		MaxGuests:          25,
		GuestTTL:           time.Hour,
		OfflineRemoveAfter: 5 * time.Minute,
		SessionRate:        "5/10m",
		StatusOrigin:       "https://msbolton.dev",
		IngestAddr:         "127.0.0.1:1", // never reached: the test opens the gate itself
		IngestToken:        "si_test",
		IngestInsecure:     true,
		VisitorsFeedID:     "demo-visitors",
	}
	hub := stream.NewHub(logger)
	d, err := startPublicDemo(cfg, routeKeycloak{}, hub, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Stop)

	rejectAll := auth.VerifierFunc(func(context.Context, string) (*auth.User, error) { return nil, errors.New("bad token") })
	gw := server.New(server.Deps{
		Logger:    logger,
		Auth:      auth.NewMiddleware(rejectAll, false, logger),
		Public:    []func(*http.ServeMux){d.RegisterPublicRoutes},
		Protected: []func(*http.ServeMux){d.RegisterProtectedRoutes},
	})
	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = "203.0.113.7:4000"
		rec := httptest.NewRecorder()
		gw.ServeHTTP(rec, req)
		return rec
	}

	rec := do(http.MethodGet, "/api/v1/demo/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status without a token: %d %s", rec.Code, rec.Body)
	}
	var st map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || len(st) != 3 {
		t.Fatalf("status body %s (%v)", rec.Body, err)
	}

	// Before the startup reconcile the session route says so, not 401.
	if rec := do(http.MethodPost, "/api/v1/demo/session", "{}"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("session before ready: %d %s, want 503", rec.Code, rec.Body)
	}
	d.ready.Store(true)
	rec = do(http.MethodPost, "/api/v1/demo/session", "{}")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"accessToken"`) {
		t.Fatalf("session without a token: %d %s, want 200 with tokens", rec.Code, rec.Body)
	}

	if rec := do(http.MethodPost, "/api/v1/demo/position", `{"lat":1,"lon":2}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("position without a token: %d %s, want 401", rec.Code, rec.Body)
	}
}
