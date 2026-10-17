package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/middleware"

	"github.com/sentinel/analytics-service/internal/baseline"
	"github.com/sentinel/analytics-service/internal/store"
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

type fakeFindings struct {
	byID      map[string]*store.Finding
	lastQuery store.Query
	reviewed  []string
}

func (f *fakeFindings) List(_ context.Context, q store.Query) ([]store.Finding, int64, error) {
	f.lastQuery = q
	var out []store.Finding
	for _, x := range f.byID {
		if q.EntityID != "" && x.EntityID != q.EntityID {
			continue
		}
		out = append(out, *x)
	}
	return out, int64(len(out)), nil
}
func (f *fakeFindings) Get(_ context.Context, id string) (*store.Finding, error) {
	if x, ok := f.byID[id]; ok {
		return x, nil
	}
	return nil, store.ErrNotFound
}
func (f *fakeFindings) Review(_ context.Context, id, status, user string) (*store.Finding, error) {
	x, ok := f.byID[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	if x.Status != store.StatusOpen {
		return nil, store.ErrBadTransition
	}
	x.Status, x.ReviewedBy = status, &user
	f.reviewed = append(f.reviewed, id+":"+status)
	return x, nil
}
func (f *fakeFindings) Stats(context.Context, string) (map[string]any, error) {
	return map[string]any{"open": int64(len(f.byID))}, nil
}

type fakeEscalator struct{ published []string }

func (f *fakeEscalator) Publish(_ context.Context, x *store.Finding) {
	f.published = append(f.published, x.ID)
}

type fixture struct {
	mux       http.Handler
	findings  *fakeFindings
	escalator *fakeEscalator
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	findings := &fakeFindings{byID: map[string]*store.Finding{
		"f-open":   {ID: "f-open", EntityID: "u1", Kind: "UNUSUAL_PLACE", Severity: "MEDIUM", Status: store.StatusOpen, Classification: "UNCLASSIFIED"},
		"f-secret": {ID: "f-secret", EntityID: "s1", Kind: "WENT_DARK", Severity: "HIGH", Status: store.StatusOpen, Classification: "SECRET"},
		"f-closed": {ID: "f-closed", EntityID: "u1", Kind: "UNUSUAL_TIME", Severity: "LOW", Status: store.StatusClosed, Classification: "UNCLASSIFIED"},
	}}
	esc := &fakeEscalator{}
	h := NewAnalyticsHandler(
		fakeStats{snap: map[string]any{"positions": int64(42)}},
		fakeCoverage{n: 7},
		fakeBaselines{byID: map[string]*baseline.Baseline{
			"u1": {EntityID: "u1", EntityType: "VEHICLE", PointCount: 300, Mature: true, Hours: make([]int16, baseline.Slots)},
			"s1": {EntityID: "s1", EntityType: "VESSEL", PointCount: 10},
		}},
		fakeEntities{levels: map[string]string{"s1": "SECRET"}},
		findings, esc, zap.NewNop())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return fixture{mux: middleware.AuthFromHeaders(mux), findings: findings, escalator: esc}
}

func do(h http.Handler, method, path, clearance, body string) *httptest.ResponseRecorder {
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rdr)
	if clearance != "" {
		req.Header.Set(middleware.HeaderClassification, clearance)
	}
	req.Header.Set(middleware.HeaderUserID, "analyst-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestStatsEndpoint(t *testing.T) {
	rec := do(newFixture(t).mux, http.MethodGet, "/api/v1/analytics/stats", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["positions"] != float64(42) || body["coverageCells"] != float64(7) || body["baselines"] != float64(2) || body["findings"] == nil {
		t.Errorf("body = %v", body)
	}
}

func TestBaselineEndpointAppliesCeiling(t *testing.T) {
	h := newFixture(t).mux
	rec := do(h, http.MethodGet, "/api/v1/analytics/entities/u1/baseline", "", "")
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
	if rec := do(h, http.MethodGet, "/api/v1/analytics/entities/s1/baseline", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("secret entity for unclassified caller: %d, want 404", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/v1/analytics/entities/s1/baseline", "SECRET", ""); rec.Code != http.StatusOK {
		t.Errorf("secret entity for secret caller: %d, want 200", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/v1/analytics/entities/none/baseline", "TOP_SECRET", ""); rec.Code != http.StatusNotFound {
		t.Errorf("entity without baseline: %d, want 404", rec.Code)
	}
}

func TestListFindingsParsesFiltersAndCeiling(t *testing.T) {
	fx := newFixture(t)
	since := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	rec := do(fx.mux, http.MethodGet, "/api/v1/analytics/findings?status=open&kinds=unusual_place,went_dark&severity=high&since="+since.Format(time.RFC3339)+"&page=2&pageSize=10", "SECRET", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	q := fx.findings.lastQuery
	if q.Status != "open" || len(q.Kinds) != 2 || q.Kinds[0] != "UNUSUAL_PLACE" || q.Severity != "high" || q.Since == nil || !q.Since.Equal(since) || q.Page != 2 || q.PageSize != 10 || q.Ceiling != "SECRET" {
		t.Errorf("query = %+v", q)
	}
	var body struct {
		Total int64 `json:"total"`
		Page  int   `json:"page"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Total != 3 || body.Page != 2 {
		t.Errorf("body = %+v", body)
	}
	// Per-entity list scopes the query and hides entities above the ceiling.
	do(fx.mux, http.MethodGet, "/api/v1/analytics/entities/u1/findings", "", "")
	if fx.findings.lastQuery.EntityID != "u1" {
		t.Errorf("entity findings query = %+v", fx.findings.lastQuery)
	}
	if rec := do(fx.mux, http.MethodGet, "/api/v1/analytics/entities/s1/findings", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("secret entity's findings for unclassified caller: %d", rec.Code)
	}
}

func TestGetAndReviewFinding(t *testing.T) {
	fx := newFixture(t)
	if rec := do(fx.mux, http.MethodGet, "/api/v1/analytics/findings/f-secret", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("secret finding for unclassified caller: %d", rec.Code)
	}
	if rec := do(fx.mux, http.MethodGet, "/api/v1/analytics/findings/f-secret", "TOP_SECRET", ""); rec.Code != http.StatusOK {
		t.Errorf("secret finding for cleared caller: %d", rec.Code)
	}
	if rec := do(fx.mux, http.MethodPatch, "/api/v1/analytics/findings/f-open", "", `{"status":"maybe"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad status: %d", rec.Code)
	}
	if rec := do(fx.mux, http.MethodPatch, "/api/v1/analytics/findings/f-closed", "", `{"status":"DISMISSED"}`); rec.Code != http.StatusConflict {
		t.Errorf("review of a closed finding: %d, want 409", rec.Code)
	}
	rec := do(fx.mux, http.MethodPatch, "/api/v1/analytics/findings/f-open", "", `{"status":"confirmed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body.String())
	}
	var f store.Finding
	json.Unmarshal(rec.Body.Bytes(), &f)
	if f.Status != store.StatusConfirmed || f.ReviewedBy == nil || *f.ReviewedBy != "analyst-1" {
		t.Errorf("finding = %+v", f)
	}
	if len(fx.escalator.published) != 1 || fx.escalator.published[0] != "f-open" {
		t.Errorf("confirm did not publish: %v", fx.escalator.published)
	}
	if rec := do(fx.mux, http.MethodPatch, "/api/v1/analytics/findings/nope", "", `{"status":"DISMISSED"}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown finding: %d", rec.Code)
	}
}

func TestCeilingFailsClosed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := ceilingOf(req); got != "UNCLASSIFIED" {
		t.Errorf("no header: ceiling = %q, want UNCLASSIFIED", got)
	}
}
