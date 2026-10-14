package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
