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

	req := httptest.NewRequest(http.MethodDelete, "/feeds", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandleFeeds_POST_MissingName(t *testing.T) {
	handler, _ := setupTestHandler()

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	body := strings.NewReader(`{"connector_type":"mqtt","format":"json","config":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/feeds", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleFeeds_POST_InvalidConnectorType(t *testing.T) {
	handler, _ := setupTestHandler()

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	body := strings.NewReader(`{"name":"Test","connector_type":"websocket","format":"json","config":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/feeds", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleFeeds_POST_InvalidFormat(t *testing.T) {
	handler, _ := setupTestHandler()

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	body := strings.NewReader(`{"name":"Test","connector_type":"mqtt","format":"xml","config":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/feeds", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
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

func TestHandleFeedByID_DELETE_BuiltIn_403(t *testing.T) {
	handler, mgr := setupTestHandler()

	mgr.Register("opensky", "OpenSky", "opensky", "built-in",
		func() (sources.Listener, error) { return &mockListener{}, nil }, false)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/feeds/opensky", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleFeedByID_DELETE_CustomFeed_204(t *testing.T) {
	handler, mgr := setupTestHandler()

	mgr.Register("custom-1", "Custom", "mqtt", "custom feed",
		func() (sources.Listener, error) { return &mockListener{}, nil }, true)
	mgr.mu.Lock()
	mgr.byID["custom-1"].status.Custom = true
	mgr.mu.Unlock()

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/feeds/custom-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleFeedByID_DELETE_NotFound_404(t *testing.T) {
	handler, _ := setupTestHandler()

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/feeds/nonexistent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleFeedByID_PATCH_BuiltIn_403(t *testing.T) {
	handler, mgr := setupTestHandler()

	mgr.Register("opensky", "OpenSky", "opensky", "built-in",
		func() (sources.Listener, error) { return &mockListener{}, nil }, false)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	body := strings.NewReader(`{"name":"Updated"}`)
	req := httptest.NewRequest(http.MethodPatch, "/feeds/opensky", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
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

func setupViewportHandler(t *testing.T) (*http.ServeMux, *mockViewportListener) {
	t.Helper()
	handler, mgr := setupTestHandler()
	ml := &mockViewportListener{}
	mgr.Register("opensky", "OpenSky", "opensky", "OpenSky feed",
		func() (sources.Listener, error) { return ml, nil }, true)
	mgr.Register("tcp", "TCP", "tcp", "TCP feed",
		func() (sources.Listener, error) { return &mockListener{}, nil }, true)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	return mux, ml
}

func doViewport(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rd)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

const viewportBody = `{"active":true,"clients":1,"boxes":[{"north":45,"south":40,"east":-80,"west":-90}]}`

func TestHandleViewport_PUT_OK(t *testing.T) {
	mux, ml := setupViewportHandler(t)

	w := doViewport(mux, http.MethodPut, "/feeds/opensky/viewport", viewportBody)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp viewportResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Feed != "opensky" || !resp.Enabled || !resp.Active || resp.Clients != 1 || resp.AreaSqDeg != 50 || resp.EffectiveIntervalSec != 10 {
		t.Errorf("unexpected response %+v", resp)
	}
	if resp.CreditsRemaining != nil || resp.LowCredits {
		t.Errorf("mock listener reports no credits; got %+v", resp)
	}
	if ml.sets.Load() != 1 {
		t.Errorf("listener not updated, sets=%d", ml.sets.Load())
	}

	// GET returns the same shape.
	w = doViewport(mux, http.MethodGet, "/feeds/opensky/viewport", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d", w.Code)
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Boxes) != 1 || resp.Boxes[0].North != 45 {
		t.Errorf("GET returned %+v", resp)
	}

	// The feed list carries the viewport.
	w = doViewport(mux, http.MethodGet, "/feeds", "")
	var feeds []FeedStatusWithHealth
	if err := json.NewDecoder(w.Body).Decode(&feeds); err != nil {
		t.Fatal(err)
	}
	if feeds[0].Viewport == nil || !feeds[0].Viewport.Active || len(feeds[0].Viewport.Boxes) != 1 {
		t.Errorf("GET /feeds should include the viewport, got %+v", feeds[0].Viewport)
	}
	if feeds[1].Viewport != nil {
		t.Errorf("tcp feed should have no viewport, got %+v", feeds[1].Viewport)
	}
}

func TestHandleViewport_PUT_Inactive(t *testing.T) {
	mux, _ := setupViewportHandler(t)
	w := doViewport(mux, http.MethodPut, "/feeds/opensky/viewport", `{"active":false,"clients":0,"boxes":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp viewportResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Active || resp.Boxes == nil {
		t.Errorf("unexpected response %+v", resp)
	}
}

func TestHandleViewport_PUT_BadRequests(t *testing.T) {
	mux, _ := setupViewportHandler(t)
	cases := map[string]string{
		"malformed json":  `{"active":`,
		"inverted box":    `{"active":true,"boxes":[{"north":40,"south":45,"east":-80,"west":-90}]}`,
		"antimeridian":    `{"active":true,"boxes":[{"north":45,"south":40,"east":-170,"west":170}]}`,
		"out of range":    `{"active":true,"boxes":[{"north":95,"south":40,"east":-80,"west":-90}]}`,
		"active no boxes": `{"active":true,"boxes":[]}`,
		"too many boxes":  `{"active":true,"boxes":[{"north":1,"south":0,"east":1,"west":0},{"north":1,"south":0,"east":1,"west":0},{"north":1,"south":0,"east":1,"west":0},{"north":1,"south":0,"east":1,"west":0},{"north":1,"south":0,"east":1,"west":0}]}`,
	}
	for name, body := range cases {
		if w := doViewport(mux, http.MethodPut, "/feeds/opensky/viewport", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d: %s", name, w.Code, w.Body.String())
		}
	}
}

func TestHandleViewport_Errors(t *testing.T) {
	mux, _ := setupViewportHandler(t)
	if w := doViewport(mux, http.MethodPut, "/feeds/nope/viewport", viewportBody); w.Code != http.StatusNotFound {
		t.Errorf("unknown feed: expected 404, got %d", w.Code)
	}
	if w := doViewport(mux, http.MethodPut, "/feeds/tcp/viewport", viewportBody); w.Code != http.StatusConflict {
		t.Errorf("unsupported feed: expected 409, got %d", w.Code)
	}
	if w := doViewport(mux, http.MethodDelete, "/feeds/opensky/viewport", ""); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE: expected 405, got %d", w.Code)
	}
	if w := doViewport(mux, http.MethodGet, "/feeds/opensky/other", ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown sub-resource: expected 404, got %d", w.Code)
	}
}
