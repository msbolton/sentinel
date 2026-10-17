package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/middleware"
)

type fakeStats struct{ snap map[string]any }

func (f fakeStats) Snapshot() map[string]any { return f.snap }

type fakeCoverage struct{ n int64 }

func (f fakeCoverage) Count(context.Context) (int64, error) { return f.n, nil }

func TestStatsEndpoint(t *testing.T) {
	h := NewAnalyticsHandler(fakeStats{snap: map[string]any{"positions": int64(42)}}, fakeCoverage{n: 7}, zap.NewNop())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/stats", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["positions"] != float64(42) || body["coverageCells"] != float64(7) {
		t.Errorf("body = %v", body)
	}
}

func TestCeilingFailsClosed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := ceilingOf(req); got != "UNCLASSIFIED" {
		t.Errorf("no header: ceiling = %q, want UNCLASSIFIED", got)
	}
	req.Header.Set(middleware.HeaderClassification, "secret")
	var seen string
	middleware.AuthFromHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = ceilingOf(r)
	})).ServeHTTP(httptest.NewRecorder(), req)
	if seen != "SECRET" {
		t.Errorf("header secret: ceiling = %q, want SECRET", seen)
	}
}
