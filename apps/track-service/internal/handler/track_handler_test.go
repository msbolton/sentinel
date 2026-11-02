package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/track-service/internal/models"
)

func TestParseTimelineParams_Defaults(t *testing.T) {
	p, err := parseTimelineParams(httptest.NewRequest(http.MethodGet, "/api/v1/tracks/timeline", nil))
	if err != nil {
		t.Fatal(err)
	}
	if p.Buckets != defaultTimelineBuckets {
		t.Errorf("buckets = %d", p.Buckets)
	}
	if got := p.End.Sub(p.Start); got != defaultTimelineWindow {
		t.Errorf("window = %v", got)
	}
}

func TestParseTimelineParams_Explicit(t *testing.T) {
	p, err := parseTimelineParams(httptest.NewRequest(http.MethodGet,
		"/api/v1/tracks/timeline?startTime=2026-01-01T00:00:00Z&endTime=2026-01-02T00:00:00Z&buckets=5000", nil))
	if err != nil {
		t.Fatal(err)
	}
	if p.Start.Format(time.RFC3339) != "2026-01-01T00:00:00Z" || p.End.Format(time.RFC3339) != "2026-01-02T00:00:00Z" {
		t.Errorf("times = %v %v", p.Start, p.End)
	}
	if p.Buckets != maxTimelineBuckets {
		t.Errorf("buckets should clamp to %d, got %d", maxTimelineBuckets, p.Buckets)
	}
}

func TestParseTimelineParams_Rejections(t *testing.T) {
	for _, q := range []string{
		"startTime=nope",
		"endTime=nope",
		"startTime=2026-01-02T00:00:00Z&endTime=2026-01-01T00:00:00Z",
		"buckets=0",
		"buckets=abc",
	} {
		if _, err := parseTimelineParams(httptest.NewRequest(http.MethodGet, "/api/v1/tracks/timeline?"+q, nil)); err == nil {
			t.Errorf("%q should be rejected", q)
		}
	}
}

func TestTimelineRouteTakesPrecedenceOverEntityID(t *testing.T) {
	mux := http.NewServeMux()
	hit := ""
	mux.HandleFunc("GET /api/v1/tracks/timeline", func(w http.ResponseWriter, r *http.Request) { hit = "timeline" })
	mux.HandleFunc("GET /api/v1/tracks/{entityId}", func(w http.ResponseWriter, r *http.Request) { hit = "entity:" + r.PathValue("entityId") })
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/tracks/timeline", nil))
	if hit != "timeline" {
		t.Errorf("routed to %q", hit)
	}
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/tracks/abc", nil))
	if hit != "entity:abc" {
		t.Errorf("routed to %q", hit)
	}
}

func TestParseBBox(t *testing.T) {
	b, err := parseBBox("-118.5,33.2,-117.1,34.0")
	if err != nil || b.West != -118.5 || b.South != 33.2 || b.East != -117.1 || b.North != 34.0 {
		t.Fatalf("%v %v", b, err)
	}
	if b, err := parseBBox("170,-10,-170,10"); err != nil || b.West != 170 || b.East != -170 {
		t.Errorf("antimeridian box rejected: %v %v", b, err)
	}
	for _, bad := range []string{"", "1,2,3", "1,2,3,4,5", "a,b,c,d", "-200,0,10,10", "0,95,10,10", "0,10,10,0", "0,0,NaN,10", "0,0,Inf,10", "0,0,10,10,"} {
		if _, err := parseBBox(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestParseReplayParams(t *testing.T) {
	const base = "/api/v1/tracks/replay?startTime=2026-01-01T00:00:00Z&endTime=2026-01-02T00:00:00Z&bbox=-118,33,-117,34"
	p, err := parseReplayParams(httptest.NewRequest(http.MethodGet, base, nil))
	if err != nil || p.MaxEntities != defaultReplayEntities || p.MaxPoints != defaultReplayPoints {
		t.Fatalf("%+v %v", p, err)
	}
	p, err = parseReplayParams(httptest.NewRequest(http.MethodGet, base+"&maxEntities=5000&maxPoints=5000", nil))
	if err != nil || p.MaxEntities != maxReplayEntities || p.MaxPoints != maxReplayPoints {
		t.Errorf("ceilings not applied: %+v %v", p, err)
	}
	p, err = parseReplayParams(httptest.NewRequest(http.MethodGet, base+"&maxEntities=7&maxPoints=9", nil))
	if err != nil || p.MaxEntities != 7 || p.MaxPoints != 9 {
		t.Errorf("explicit limits lost: %+v %v", p, err)
	}
	// Exactly seven days is allowed.
	if _, err := parseReplayParams(httptest.NewRequest(http.MethodGet, "/api/v1/tracks/replay?startTime=2026-01-01T00:00:00Z&endTime=2026-01-08T00:00:00Z&bbox=-118,33,-117,34", nil)); err != nil {
		t.Errorf("7-day window rejected: %v", err)
	}
	for _, q := range []string{
		"bbox=-118,33,-117,34", // no times
		"startTime=2026-01-01T00:00:00Z&endTime=2026-01-02T00:00:00Z", // no bbox
		"startTime=2026-01-02T00:00:00Z&endTime=2026-01-01T00:00:00Z&bbox=-118,33,-117,34",
		"startTime=2026-01-01T00:00:00Z&endTime=2026-01-09T00:00:00Z&bbox=-118,33,-117,34", // 8 days
		"startTime=2026-01-01T00:00:00Z&endTime=2026-01-02T00:00:00Z&bbox=-118,33,-117,34&maxPoints=0",
		"startTime=2026-01-01T00:00:00Z&endTime=2026-01-02T00:00:00Z&bbox=-118,33,-117,34&maxEntities=0",
		"startTime=2026-01-01T00:00:00Z&endTime=2026-01-02T00:00:00Z&bbox=-118,33,-117,34&maxEntities=x",
		"startTime=yesterday&endTime=2026-01-02T00:00:00Z&bbox=-118,33,-117,34",
		"startTime=2026-01-01T00:00:00Z&endTime=2026-01-02T00:00:00Z&bbox=x",
	} {
		if _, err := parseReplayParams(httptest.NewRequest(http.MethodGet, "/api/v1/tracks/replay?"+q, nil)); err == nil {
			t.Errorf("%q accepted", q)
		}
	}
}

func TestParseTimelineParams_BBox(t *testing.T) {
	p, err := parseTimelineParams(httptest.NewRequest(http.MethodGet, "/api/v1/tracks/timeline?bbox=-118,33,-117,34", nil))
	if err != nil || p.Box == nil || p.Box.East != -117 {
		t.Fatalf("%+v %v", p, err)
	}
	p, err = parseTimelineParams(httptest.NewRequest(http.MethodGet, "/api/v1/tracks/timeline", nil))
	if err != nil || p.Box != nil {
		t.Errorf("no bbox should leave Box nil: %+v %v", p, err)
	}
	if _, err := parseTimelineParams(httptest.NewRequest(http.MethodGet, "/api/v1/tracks/timeline?bbox=x", nil)); err == nil {
		t.Error("bad bbox accepted")
	}
}

func TestReplayRouteTakesPrecedenceOverEntityID(t *testing.T) {
	// Register through the real RegisterRoutes so the test fails if the
	// registration order or pattern ever lets {entityId} capture "replay".
	mux := http.NewServeMux()
	(&TrackHandler{}).RegisterRoutes(mux)
	_, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, "/api/v1/tracks/replay", nil))
	if pattern != "GET /api/v1/tracks/replay" {
		t.Errorf("GET replay matched %q", pattern)
	}
	_, pattern = mux.Handler(httptest.NewRequest(http.MethodGet, "/api/v1/tracks/abc", nil))
	if pattern != "GET /api/v1/tracks/{entityId}" {
		t.Errorf("GET abc matched %q", pattern)
	}
	_, pattern = mux.Handler(httptest.NewRequest(http.MethodPost, "/api/v1/tracks/abc/replay", nil))
	if pattern != "POST /api/v1/tracks/{entityId}/replay" {
		t.Errorf("POST abc/replay matched %q", pattern)
	}
}

func TestBuildSegments_SplitsOnGaps(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pt := func(offset time.Duration) models.TrackPointResult {
		return models.TrackPointResult{EntityID: "e", Timestamp: base.Add(offset)}
	}
	segs := buildSegments([]models.TrackPointResult{pt(0), pt(10 * time.Minute), pt(2 * time.Hour), pt(2*time.Hour + time.Minute)})
	if len(segs) != 2 || len(segs[0].Points) != 2 || len(segs[1].Points) != 2 {
		t.Fatalf("segments = %+v", segs)
	}
	if !segs[0].EndTime.Equal(base.Add(10 * time.Minute)) {
		t.Errorf("first segment end = %v", segs[0].EndTime)
	}
	if len(buildSegments(nil)) != 0 {
		t.Error("nil input should yield no segments")
	}
}

func TestWriteQueryError(t *testing.T) {
	h := &TrackHandler{logger: zap.NewNop()}
	for _, c := range []struct {
		name    string
		err     error
		status  int
		message string
	}{
		{"a deadline, wrapped as the store wraps it", fmt.Errorf("querying replay: %w", context.DeadlineExceeded), http.StatusServiceUnavailable, "replay timed out"},
		{"any other failure", errors.New("connection refused"), http.StatusInternalServerError, "failed to query replay"},
		{"a cancelled caller is not a timeout", fmt.Errorf("querying replay: %w", context.Canceled), http.StatusInternalServerError, "failed to query replay"},
	} {
		rec := httptest.NewRecorder()
		h.writeQueryError(rec, c.err, "replay timed out", "failed to query replay")
		var body struct {
			Message    string `json:"message"`
			StatusCode int    `json:"statusCode"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: body %q: %v", c.name, rec.Body.String(), err)
		}
		if rec.Code != c.status || body.Message != c.message {
			t.Errorf("%s: %d %q, want %d %q", c.name, rec.Code, body.Message, c.status, c.message)
		}
	}
}

func TestHistoryQueryTimeoutIsUnderTheWriteTimeout(t *testing.T) {
	// cmd/server/main.go sets WriteTimeout to 10 s.
	if historyQueryTimeout >= 10*time.Second {
		t.Errorf("historyQueryTimeout = %v, must be under the 10 s write timeout", historyQueryTimeout)
	}
}
