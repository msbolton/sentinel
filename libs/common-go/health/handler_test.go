package health

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func probe(t *testing.T, h *Handler, path string) (int, healthResponse) {
	t.Helper()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var resp healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("%s: %v in %q", path, err, rec.Body.String())
	}
	return rec.Code, resp
}

func TestHealthyWhenChecksPass(t *testing.T) {
	h := NewHandler("svc", zap.NewNop())
	h.AddCheck("database", func() error { return nil })
	if code, resp := probe(t, h, "/ready"); code != http.StatusOK || resp.Status != "ready" || resp.Checks != nil {
		t.Fatalf("ready: %d %+v", code, resp)
	}
	if code, resp := probe(t, h, "/health"); code != http.StatusOK || resp.Status != "healthy" || resp.Checks != nil {
		t.Fatalf("health: %d %+v", code, resp)
	}
}

func TestFailingCheckIsNamedOnBothEndpoints(t *testing.T) {
	h := NewHandler("svc", zap.NewNop())
	h.AddCheck("database", func() error { return nil })
	h.AddCheck("kafka", func() error { return errors.New("kafka consumer stopped") })
	code, resp := probe(t, h, "/ready")
	if code != http.StatusServiceUnavailable || resp.Status != "not ready" || resp.Checks["kafka"] != "kafka consumer stopped" || len(resp.Checks) != 1 {
		t.Fatalf("ready: %d %+v", code, resp)
	}
	// Liveness must not restart the process over a dependency outage, but
	// it says what is wrong.
	code, resp = probe(t, h, "/health")
	if code != http.StatusOK || resp.Status != "degraded" || resp.Checks["kafka"] != "kafka consumer stopped" {
		t.Fatalf("health: %d %+v", code, resp)
	}
}

func TestPositionalCheckersAreNumbered(t *testing.T) {
	h := NewHandler("svc", zap.NewNop(), func() error { return errors.New("down") })
	if code, resp := probe(t, h, "/ready"); code != http.StatusServiceUnavailable || resp.Checks["check0"] != "down" {
		t.Fatalf("ready: %d %+v", code, resp)
	}
}
