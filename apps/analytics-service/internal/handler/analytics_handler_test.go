package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/middleware"

	"github.com/sentinel/analytics-service/internal/baseline"
)

type fakeStats struct{ snap map[string]any }

func (f fakeStats) Snapshot() map[string]any { return f.snap }

type fakeCoverage struct{ n int64 }

func (f fakeCoverage) Count(context.Context) (int64, error) { return f.n, nil }

type fakeBaselines struct{ byID map[string]*baseline.Baseline }

func (f fakeBaselines) Get(_ context.Context, id string) (*baseline.Baseline, error) {
	return f.byID[id], nil
}
func (f fakeBaselines) Count(context.Context) (int64, error) { return int64(len(f.byID)), nil }

type fakeEntities struct{ levels map[string]string }

func (f fakeEntities) Classification(_ context.Context, id string) (string, error) {
	if l, ok := f.levels[id]; ok {
		return l, nil
	}
	return "UNCLASSIFIED", nil
}

func newMux(t *testing.T) http.Handler {
	t.Helper()
	h := NewAnalyticsHandler(
		fakeStats{snap: map[string]any{"positions": int64(42)}},
		fakeCoverage{n: 7},
		fakeBaselines{byID: map[string]*baseline.Baseline{
			"u1": {EntityID: "u1", EntityType: "VEHICLE", PointCount: 300, Mature: true, Hours: make([]int16, baseline.Slots)},
			"s1": {EntityID: "s1", EntityType: "VESSEL", PointCount: 10},
		}},
		fakeEntities{levels: map[string]string{"s1": "SECRET"}},
		zap.NewNop())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return middleware.AuthFromHeaders(mux)
}

func get(h http.Handler, path, clearance string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if clearance != "" {
		req.Header.Set(middleware.HeaderClassification, clearance)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestStatsEndpoint(t *testing.T) {
	rec := get(newMux(t), "/api/v1/analytics/stats", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["positions"] != float64(42) || body["coverageCells"] != float64(7) || body["baselines"] != float64(2) {
		t.Errorf("body = %v", body)
	}
}

func TestBaselineEndpointAppliesCeiling(t *testing.T) {
	h := newMux(t)
	rec := get(h, "/api/v1/analytics/entities/u1/baseline", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("unclassified entity for unclassified caller: %d", rec.Code)
	}
	var b baseline.Baseline
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	if !b.Mature || b.PointCount != 300 || b.Places == nil {
		t.Errorf("baseline = %+v", b)
	}
	if rec := get(h, "/api/v1/analytics/entities/s1/baseline", ""); rec.Code != http.StatusNotFound {
		t.Errorf("secret entity for unclassified caller: %d, want 404", rec.Code)
	}
	if rec := get(h, "/api/v1/analytics/entities/s1/baseline", "SECRET"); rec.Code != http.StatusOK {
		t.Errorf("secret entity for secret caller: %d, want 200", rec.Code)
	}
	if rec := get(h, "/api/v1/analytics/entities/none/baseline", "TOP_SECRET"); rec.Code != http.StatusNotFound {
		t.Errorf("entity without baseline: %d, want 404", rec.Code)
	}
}

func TestCeilingFailsClosed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := ceilingOf(req); got != "UNCLASSIFIED" {
		t.Errorf("no header: ceiling = %q, want UNCLASSIFIED", got)
	}
}
