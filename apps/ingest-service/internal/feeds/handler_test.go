package feeds

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/sources"
)

func setupTestHandler() (*Handler, *Manager) {
	logger := zap.NewNop()
	mgr := NewManager(logger, nil, nil, nil)
	handler := NewHandler(mgr, logger)
	return handler, mgr
}

func TestHandleFeeds_GET(t *testing.T) {
	handler, mgr := setupTestHandler()

	mgr.Register("mqtt", "MQTT Broker", "mqtt", "MQTT feed",
		func() (sources.Listener, error) { return &mockListener{}, nil }, true)
	mgr.Register("tcp", "TCP Listener", "tcp", "TCP feed",
		func() (sources.Listener, error) { return &mockListener{}, nil }, false)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/feeds", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var feeds []FeedStatusWithHealth
	if err := json.NewDecoder(w.Body).Decode(&feeds); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(feeds) != 2 {
		t.Fatalf("expected 2 feeds, got %d", len(feeds))
	}
	if feeds[0].ID != "mqtt" || !feeds[0].Enabled {
		t.Errorf("unexpected first feed: %+v", feeds[0])
	}
	if feeds[1].ID != "tcp" || feeds[1].Enabled {
		t.Errorf("unexpected second feed: %+v", feeds[1])
	}
}

func TestHandleFeeds_IncludesHealth(t *testing.T) {
	handler, mgr := setupTestHandler()

	mgr.Register("opensky", "OpenSky", "opensky", "OpenSky feed",
		func() (sources.Listener, error) { return &mockListener{}, nil }, true)
	mgr.SetStaleThresholds("opensky", 120, 300)
	mgr.RecordSuccess("opensky", 100, time.Now())

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/feeds", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var feeds []FeedStatusWithHealth
	if err := json.NewDecoder(w.Body).Decode(&feeds); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(feeds) != 1 {
		t.Fatalf("expected 1 feed, got %d", len(feeds))
	}
	if feeds[0].Health == nil {
		t.Fatal("expected Health to be non-nil")
	}
	if feeds[0].Health.Status != "healthy" {
		t.Errorf("expected health status 'healthy', got %q", feeds[0].Health.Status)
	}
	if feeds[0].Health.EntitiesCount != 100 {
		t.Errorf("expected EntitiesCount 100, got %d", feeds[0].Health.EntitiesCount)
	}
}

func TestHandleFeeds_MethodNotAllowed(t *testing.T) {
	handler, _ := setupTestHandler()

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/feeds", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandleFeedByID_Toggle(t *testing.T) {
	handler, mgr := setupTestHandler()

	mgr.Register("opensky", "OpenSky", "opensky", "OpenSky feed",
		func() (sources.Listener, error) { return &mockListener{}, nil }, false)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	// Enable the feed.
	body := strings.NewReader(`{"enabled": true}`)
	req := httptest.NewRequest(http.MethodPut, "/feeds/opensky", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var status FeedStatus
	if err := json.NewDecoder(w.Body).Decode(&status); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !status.Enabled {
		t.Error("expected feed to be enabled")
	}

	// Disable the feed.
	body = strings.NewReader(`{"enabled": false}`)
	req = httptest.NewRequest(http.MethodPut, "/feeds/opensky", body)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	if err := json.NewDecoder(w.Body).Decode(&status); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if status.Enabled {
		t.Error("expected feed to be disabled")
	}
}

func TestHandleFeedByID_NotFound(t *testing.T) {
	handler, _ := setupTestHandler()

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	body := strings.NewReader(`{"enabled": true}`)
	req := httptest.NewRequest(http.MethodPut, "/feeds/nonexistent", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleFeedByID_BadBody(t *testing.T) {
	handler, mgr := setupTestHandler()

	mgr.Register("tcp", "TCP", "tcp", "d",
		func() (sources.Listener, error) { return &mockListener{}, nil }, false)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	body := strings.NewReader(`not json`)
	req := httptest.NewRequest(http.MethodPut, "/feeds/tcp", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleFeedByID_MethodNotAllowed(t *testing.T) {
	handler, _ := setupTestHandler()

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/feeds/something", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}
