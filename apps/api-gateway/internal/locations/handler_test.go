package locations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

const validID = "6f1d2c3e-4b5a-4c6d-8e7f-9a0b1c2d3e4f"

type fakeStore struct {
	items   map[string]Location
	lastIn  Input
	listCat string
	listQ   string
}

func newFake() *fakeStore {
	desc := "Capital"
	return &fakeStore{items: map[string]Location{
		validID: {ID: validID, Name: "Paris", Description: &desc, Latitude: 48.8, Longitude: 2.3, Altitude: 1200, Heading: 0, Pitch: -45, Range: 2500, Category: "CITY", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}}
}

func (f *fakeStore) List(_ context.Context, category, search string) ([]Location, error) {
	f.listCat, f.listQ = category, search
	out := []Location{}
	for _, l := range f.items {
		out = append(out, l)
	}
	return out, nil
}
func (f *fakeStore) Get(_ context.Context, id string) (*Location, error) {
	l, ok := f.items[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &l, nil
}
func (f *fakeStore) Create(_ context.Context, in Input) (*Location, error) {
	f.lastIn = in
	l := Location{ID: "new-id", Name: *in.Name, Latitude: *in.Latitude, Longitude: *in.Longitude, Altitude: *in.Altitude,
		Heading: *in.Heading, Pitch: *in.Pitch, Range: *in.Range, Has3DTiles: *in.Has3DTiles, Category: *in.Category}
	return &l, nil
}
func (f *fakeStore) Update(_ context.Context, id string, in Input) (*Location, error) {
	l, ok := f.items[id]
	if !ok {
		return nil, ErrNotFound
	}
	f.lastIn = in
	if in.Name != nil {
		l.Name = *in.Name
	}
	return &l, nil
}
func (f *fakeStore) Delete(_ context.Context, id string) error {
	if _, ok := f.items[id]; !ok {
		return ErrNotFound
	}
	delete(f.items, id)
	return nil
}

func newServer(f *fakeStore) http.Handler {
	mux := http.NewServeMux()
	NewHandler(f, zap.NewNop()).RegisterRoutes(mux)
	return mux
}

func call(h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, rdr)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func message(rr *httptest.ResponseRecorder) string {
	var m map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &m)
	s, _ := m["message"].(string)
	return s
}

func TestList_PassesFilters(t *testing.T) {
	f := newFake()
	rr := call(newServer(f), http.MethodGet, "/api/v1/locations?category=CITY&search=par", "")
	if rr.Code != 200 || f.listCat != "CITY" || f.listQ != "par" {
		t.Errorf("status %d, cat %q, q %q", rr.Code, f.listCat, f.listQ)
	}
	if rr := call(newServer(f), http.MethodGet, "/api/v1/locations?category=MOON", ""); rr.Code != 400 {
		t.Errorf("invalid category should be 400, got %d", rr.Code)
	}
}

func TestGet(t *testing.T) {
	h := newServer(newFake())
	if rr := call(h, http.MethodGet, "/api/v1/locations/"+validID, ""); rr.Code != 200 {
		t.Errorf("get: %d", rr.Code)
	}
	if rr := call(h, http.MethodGet, "/api/v1/locations/not-a-uuid", ""); rr.Code != 400 {
		t.Errorf("bad uuid should be 400, got %d", rr.Code)
	}
	rr := call(h, http.MethodGet, "/api/v1/locations/00000000-0000-4000-8000-000000000000", "")
	if rr.Code != 404 || !strings.Contains(message(rr), "not found") {
		t.Errorf("missing should be 404, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestCreate_ValidationAndDefaults(t *testing.T) {
	f := newFake()
	h := newServer(f)

	cases := map[string]string{
		`{"latitude":1,"longitude":2}`:                              "name is required",
		`{"name":"X","latitude":91,"longitude":2}`:                  "latitude must be between -90 and 90",
		`{"name":"X","latitude":1,"longitude":-181}`:                "longitude must be between -180 and 180",
		`{"name":"X","latitude":1,"longitude":2,"altitude":-5}`:     "altitude must not be less than 0",
		`{"name":"X","latitude":1,"longitude":2,"category":"MOON"}`: "category must be one of CITY, MILITARY_BASE, PORT, AIRPORT, CUSTOM",
		`{"name":"X","latitude":1,"longitude":2,"bogus":true}`:      "Invalid request body",
	}
	for body, want := range cases {
		rr := call(h, http.MethodPost, "/api/v1/locations", body)
		if rr.Code != 400 || message(rr) != want {
			t.Errorf("%s -> %d %q, want 400 %q", body, rr.Code, message(rr), want)
		}
	}

	rr := call(h, http.MethodPost, "/api/v1/locations", `{"name":"Oslo","latitude":59.9,"longitude":10.7}`)
	if rr.Code != 201 {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	in := f.lastIn
	if *in.Altitude != 1000 || *in.Heading != 0 || *in.Pitch != -45 || *in.Range != 2000 || *in.Has3DTiles || *in.Category != "CUSTOM" {
		t.Errorf("defaults not applied: %+v", in)
	}
}

func TestUpdate(t *testing.T) {
	f := newFake()
	h := newServer(f)
	rr := call(h, http.MethodPatch, "/api/v1/locations/"+validID, `{"name":"Paris 2"}`)
	if rr.Code != 200 || f.lastIn.Latitude != nil || *f.lastIn.Name != "Paris 2" {
		t.Errorf("patch: %d %+v", rr.Code, f.lastIn)
	}
	if rr := call(h, http.MethodPatch, "/api/v1/locations/"+validID, `{"latitude":100}`); rr.Code != 400 {
		t.Errorf("invalid patch should be 400, got %d", rr.Code)
	}
	if rr := call(h, http.MethodPatch, "/api/v1/locations/00000000-0000-4000-8000-000000000000", `{"name":"x"}`); rr.Code != 404 {
		t.Errorf("missing patch should be 404, got %d", rr.Code)
	}
}

func TestDelete(t *testing.T) {
	f := newFake()
	h := newServer(f)
	if rr := call(h, http.MethodDelete, "/api/v1/locations/"+validID, ""); rr.Code != 204 {
		t.Errorf("delete: %d", rr.Code)
	}
	if rr := call(h, http.MethodDelete, "/api/v1/locations/"+validID, ""); rr.Code != 404 {
		t.Errorf("second delete should be 404, got %d", rr.Code)
	}
}
