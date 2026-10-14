// Package server wires the gateway's routes, middleware, and upstream proxies.
package server

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/middleware"

	"github.com/sentinel/api-gateway/internal/auth"
)

// Upstreams holds the reverse-proxy handlers for each domain service.
type Upstreams struct {
	Entities http.Handler
	Tracks   http.Handler
	Alerts   http.Handler
	Links    http.Handler
	Search   http.Handler
	Feeds    http.Handler
}

// Deps are the collaborators the router needs.
type Deps struct {
	Logger     *zap.Logger
	Auth       *auth.Middleware
	Policies   []Policy
	Production bool
	CORSOrigin string
	Upstreams  Upstreams
	// Locations registers its routes on the protected API mux.
	Locations interface{ RegisterRoutes(*http.ServeMux) }
	// Public registers unauthenticated routes (e.g. registration) on the root mux.
	Public interface{ RegisterRoutes(*http.ServeMux) }
	// Protected registers additional authenticated routes on the API mux.
	Protected []interface{ RegisterRoutes(*http.ServeMux) }
	// Root registers routes outside /api/v1 that handle their own auth (e.g. websocket).
	Root []interface{ RegisterRoutes(*http.ServeMux) }
	// ReadyChecks run on readiness probes.
	ReadyChecks map[string]func() error
	StartTime   time.Time
}

// New builds the gateway's HTTP handler.
func New(d Deps) http.Handler {
	if d.Policies == nil {
		d.Policies = DefaultPolicies
	}
	if d.StartTime.IsZero() {
		d.StartTime = time.Now()
	}

	// Protected API: everything under /api/v1 except health and public routes.
	api := http.NewServeMux()
	mountProxy(api, "/api/v1/entities", d.Upstreams.Entities)
	mountProxy(api, "/api/v1/tracks", d.Upstreams.Tracks)
	mountProxy(api, "/api/v1/alerts", d.Upstreams.Alerts)
	mountProxy(api, "/api/v1/links", d.Upstreams.Links)
	mountProxy(api, "/api/v1/search", d.Upstreams.Search)
	mountProxy(api, "/api/v1/feeds", d.Upstreams.Feeds)
	if d.Locations != nil {
		d.Locations.RegisterRoutes(api)
	}
	for _, p := range d.Protected {
		p.RegisterRoutes(api)
	}
	protected := d.Auth.RequireAuth(EnforcePolicies(d.Policies, d.Auth)(api))

	root := http.NewServeMux()
	root.HandleFunc("GET /api/v1/health", d.handleLiveness)
	root.HandleFunc("GET /api/v1/health/ready", d.handleReadiness)
	root.HandleFunc("GET /health", d.handleLiveness)
	root.HandleFunc("GET /ready", d.handleReadiness)
	if d.Public != nil {
		d.Public.RegisterRoutes(root)
	}
	for _, r := range d.Root {
		r.RegisterRoutes(root)
	}
	root.Handle("/api/v1/", protected)

	return middleware.CORS(d.CORSOrigin)(auth.SecurityHeaders(d.Production)(root))
}

func mountProxy(mux *http.ServeMux, prefix string, h http.Handler) {
	if h == nil {
		return
	}
	mux.Handle(prefix, h)
	mux.Handle(prefix+"/", h)
}

func (d Deps) handleLiveness(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":    "ok",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func (d Deps) handleReadiness(w http.ResponseWriter, _ *http.Request) {
	status := http.StatusOK
	overall := "ok"
	checks := map[string]map[string]interface{}{}
	for name, check := range d.ReadyChecks {
		start := time.Now()
		err := check()
		entry := map[string]interface{}{"status": "ok", "latencyMs": time.Since(start).Milliseconds()}
		if err != nil {
			entry["status"] = "error"
			entry["error"] = err.Error()
			overall = "unhealthy"
			status = http.StatusServiceUnavailable
		}
		checks[name] = entry
	}
	version := os.Getenv("APP_VERSION")
	if version == "" {
		version = "0.0.1"
	}
	writeJSON(w, status, map[string]interface{}{
		"status":    overall,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"service":   "api-gateway",
		"version":   version,
		"uptime":    int64(time.Since(d.StartTime).Seconds()),
		"checks":    checks,
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
