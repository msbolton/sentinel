package export

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/sentinel/api-gateway/internal/auth"
)

type fakeSource struct {
	entities []Entity
	calls    []Query
	fail     error
}

func (f *fakeSource) Query(_ context.Context, _ *auth.User, q Query) (Page, error) {
	f.calls = append(f.calls, q)
	if f.fail != nil {
		return Page{}, f.fail
	}
	start := (q.Page - 1) * q.PageSize
	if start > len(f.entities) {
		start = len(f.entities)
	}
	end := start + q.PageSize
	if end > len(f.entities) {
		end = len(f.entities)
	}
	return Page{Entities: f.entities[start:end], Total: len(f.entities)}, nil
}

func f64(v float64) *float64 { return &v }
func str(v string) *string   { return &v }

func sample(n int) []Entity {
	out := make([]Entity, 0, n)
	for i := 0; i < n; i++ {
		e := Entity{ID: "e" + string(rune('a'+i%26)) + string(rune('0'+i/26)), Name: "UAL" + string(rune('0'+i%10)), EntityType: "AIRCRAFT", Classification: "UNCLASSIFIED", Source: "OPENSKY", AgeoutState: "LIVE",
			FeedID: str("opensky"), Position: &Point{Lat: 41.9 + float64(i)*0.01, Lon: -87.9}, Altitude: f64(10668), Heading: f64(270.5), SpeedKnots: f64(450), MilStd2525dSymbol: str("SFAPMF----"), LastObservedAt: str("2026-10-16T12:00:00Z")}
		out = append(out, e)
	}
	return out
}

func serve(t *testing.T, src Source, target string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(src, zap.NewNop())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = "sentinel.example"
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{UserID: "u1", Roles: []string{auth.RoleViewer}, ClassificationLevel: "SECRET"}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestGeoJSON(t *testing.T) {
	src := &fakeSource{entities: sample(3)}
	src.entities[1].Position = nil // no position: not a feature
	rec := serve(t, src, "/api/v1/export/entities.geojson?bbox=-90,40,-85,45&types=AIRCRAFT,VESSEL&feeds=opensky")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/geo+json" || rec.Header().Get("X-Total-Count") != "3" {
		t.Fatalf("status %d headers %v body %s", rec.Code, rec.Header(), rec.Body.String())
	}
	var fc struct {
		Type     string `json:"type"`
		Features []struct {
			ID         string         `json:"id"`
			Geometry   geometry       `json:"geometry"`
			Properties map[string]any `json:"properties"`
		} `json:"features"`
		Total int `json:"totalFeatures"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &fc); err != nil {
		t.Fatal(err)
	}
	if fc.Type != "FeatureCollection" || len(fc.Features) != 2 || fc.Total != 3 {
		t.Fatalf("collection = %+v", fc)
	}
	f := fc.Features[0]
	if f.Geometry.Type != "Point" || len(f.Geometry.Coordinates) != 3 || f.Geometry.Coordinates[0] != -87.9 || f.Geometry.Coordinates[2] != 10668 {
		t.Errorf("geometry = %+v", f.Geometry)
	}
	if f.Properties["name"] != "UAL0" || f.Properties["feedId"] != "opensky" || f.Properties["sidc"] != "SFAPMF----" || f.Properties["classification"] != "UNCLASSIFIED" || f.Properties["heading"] != 270.5 {
		t.Errorf("properties = %+v", f.Properties)
	}
	q := src.calls[0]
	if q.BBox == nil || q.BBox.West != -90 || q.BBox.North != 45 || len(q.Types) != 2 || q.Feeds[0] != "opensky" || q.PageSize != upstreamPage {
		t.Errorf("upstream query = %+v", q)
	}
}

func TestGeoJSONExerciseProperty(t *testing.T) {
	src := &fakeSource{entities: sample(2)}
	src.entities[0].Exercise = true
	rec := serve(t, src, "/api/v1/export/entities.geojson")
	var fc struct {
		Features []struct {
			Properties map[string]any `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &fc); err != nil || len(fc.Features) != 2 {
		t.Fatalf("body %s err %v", rec.Body.String(), err)
	}
	if fc.Features[0].Properties["exercise"] != true {
		t.Errorf("exercise entity properties = %+v", fc.Features[0].Properties)
	}
	if _, present := fc.Features[1].Properties["exercise"]; present {
		t.Errorf("live entity properties = %+v", fc.Features[1].Properties)
	}
}

// An entity-service response with no exercise key reads as not exercise.
func TestEntityDecodesExerciseKey(t *testing.T) {
	var with, without Entity
	if err := json.Unmarshal([]byte(`{"id":"a","exercise":true}`), &with); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"id":"b"}`), &without); err != nil {
		t.Fatal(err)
	}
	if !with.Exercise || without.Exercise {
		t.Errorf("with=%v without=%v", with.Exercise, without.Exercise)
	}
}

func TestGeoJSONPagingAcrossUpstreamPages(t *testing.T) {
	src := &fakeSource{entities: sample(2500)}
	rec := serve(t, src, "/api/v1/export/entities.geojson?limit=2000&page=1")
	var fc struct {
		Features []json.RawMessage `json:"features"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &fc)
	if len(fc.Features) != 2000 || len(src.calls) != 2 || rec.Header().Get("X-Total-Count") != "2500" {
		t.Fatalf("features=%d calls=%d total=%s", len(fc.Features), len(src.calls), rec.Header().Get("X-Total-Count"))
	}
	src.calls = nil
	rec = serve(t, src, "/api/v1/export/entities.geojson?limit=2000&page=2")
	_ = json.Unmarshal(rec.Body.Bytes(), &fc)
	if len(fc.Features) != 500 || src.calls[0].Page != 3 {
		t.Fatalf("page 2: features=%d first upstream page=%d", len(fc.Features), src.calls[0].Page)
	}
	rec = serve(t, src, "/api/v1/export/entities.geojson?limit=99999")
	_ = json.Unmarshal(rec.Body.Bytes(), &fc)
	if len(fc.Features) != 2500 {
		t.Fatalf("limit clamp: %d", len(fc.Features))
	}
}

func TestKMLAndNetworkLink(t *testing.T) {
	src := &fakeSource{entities: sample(2)}
	rec := serve(t, src, "/api/v1/export/entities.kml?feeds=opensky")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "kml") || !strings.HasPrefix(body, "<?xml") {
		t.Fatalf("status %d type %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if strings.Count(body, "<Placemark") != 2 || !strings.Contains(body, "<coordinates>-87.900000,41.900000,10668.0</coordinates>") || !strings.Contains(body, `<Data name="sidc">`) || !strings.Contains(body, "<when>2026-10-16T12:00:00Z</when>") {
		t.Errorf("kml = %s", body)
	}

	rec = serve(t, src, "/api/v1/export/entities.kml?link=1&refresh=3&feeds=opensky&api_key=sk_abc")
	body = rec.Body.String()
	if !strings.Contains(body, "<NetworkLink>") || !strings.Contains(body, "<refreshInterval>5</refreshInterval>") || !strings.Contains(body, "<viewFormat>bbox=[bboxWest],[bboxSouth],[bboxEast],[bboxNorth]</viewFormat>") {
		t.Errorf("network link = %s", body)
	}
	if !strings.Contains(body, "<href>http://sentinel.example/api/v1/export/entities.kml?api_key=sk_abc&amp;feeds=opensky</href>") {
		t.Errorf("href = %s", body)
	}
	if len(src.calls) != 1 {
		t.Errorf("network link document must not query entities: %d calls", len(src.calls))
	}
}

func TestBadQueriesAndUpstreamFailure(t *testing.T) {
	src := &fakeSource{}
	for _, target := range []string{
		"/api/v1/export/entities.geojson?bbox=1,2,3",
		"/api/v1/export/entities.geojson?bbox=a,b,c,d",
		"/api/v1/export/entities.geojson?bbox=0,50,10,40",
		"/api/v1/export/entities.geojson?page=0",
		"/api/v1/export/entities.geojson?limit=-1",
		"/api/v1/export/entities.kml?link=1&refresh=x",
	} {
		if rec := serve(t, src, target); rec.Code != 400 {
			t.Errorf("%s: status %d", target, rec.Code)
		}
	}
	src.fail = errors.New("boom")
	if rec := serve(t, src, "/api/v1/export/entities.geojson"); rec.Code != 502 {
		t.Errorf("upstream failure: %d", rec.Code)
	}
}
