package emplacement

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sentinel/go-common/middleware"
)

// memStore is a Store in memory with the real store's version rule.
type memStore struct {
	rows    map[string]Emplacement
	next    int
	deleted []string // ids Delete was called with
}

func newMemStore(rows ...Emplacement) *memStore {
	m := &memStore{rows: map[string]Emplacement{}}
	for _, e := range rows {
		m.rows[e.ID] = e
	}
	return m
}

func (m *memStore) List(_ context.Context, f Filter) ([]Emplacement, error) {
	out := []Emplacement{}
	for _, e := range m.rows {
		if f.Mobility != "" && e.Mobility != f.Mobility {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}
func (m *memStore) Get(_ context.Context, id string) (Emplacement, error) {
	e, ok := m.rows[id]
	if !ok {
		return Emplacement{}, ErrNotFound
	}
	return e, nil
}
func (m *memStore) Create(_ context.Context, e Emplacement) (Emplacement, error) {
	m.next++
	e.ID, e.Version = "00000000-0000-0000-0000-00000000000"+string(rune('0'+m.next)), 1
	m.rows[e.ID] = e
	return e, nil
}
func (m *memStore) Update(_ context.Context, id string, ifVersion int, fn func(*Emplacement) error) (Emplacement, error) {
	e, ok := m.rows[id]
	if !ok {
		return Emplacement{}, ErrNotFound
	}
	if ifVersion > 0 && e.Version != ifVersion {
		return Emplacement{}, ErrVersionConflict
	}
	if err := fn(&e); err != nil {
		return Emplacement{}, err
	}
	e.Version++
	m.rows[id] = e
	return e, nil
}
func (m *memStore) Delete(_ context.Context, id string) error {
	m.deleted = append(m.deleted, id)
	if _, ok := m.rows[id]; !ok {
		return ErrNotFound
	}
	delete(m.rows, id)
	return nil
}

const (
	idU = "11111111-1111-1111-1111-111111111111"
	idS = "22222222-2222-2222-2222-222222222222"
	idM = "33333333-3333-3333-3333-333333333333"
)

func seeded() *memStore {
	u := fixed("Coastal")
	u.ID, u.Version = idU, 3
	s := fixed("Secret site")
	s.ID, s.Version, s.Classification = idS, 1, "SECRET"
	m := New()
	m.ID, m.Version, m.Name, m.Mobility, m.SensorType = idM, 1, "Ship radar", MobilityMobile, "RADAR"
	return newMemStore(u, s, m)
}

func feeds(_ context.Context, id string) (bool, bool, error) {
	switch id {
	case "grpc-1":
		return true, true, nil
	case "tak-out":
		return true, false, nil
	}
	return false, false, nil
}

func serve(t *testing.T, store Store, method, path, clearance, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(store, feeds, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, middleware.AuthFromHeaders)
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set(middleware.HeaderUserID, "u-1")
	r.Header.Set(middleware.HeaderClassification, clearance)
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestHandlerListAndGetAreClearanceFiltered(t *testing.T) {
	store := seeded()
	var list []Emplacement
	w := serve(t, store, http.MethodGet, "/emplacements", "CONFIDENTIAL", "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list) != 2 {
		t.Fatalf("list as C: %d %s", w.Code, w.Body)
	}
	if w := serve(t, store, http.MethodGet, "/emplacements/"+idS, "CONFIDENTIAL", ""); w.Code != 404 {
		t.Fatalf("an S emplacement for a C caller must be 404, got %d", w.Code)
	}
	w = serve(t, store, http.MethodGet, "/emplacements/"+idS, "TOP_SECRET", "")
	if w.Code != 200 || w.Header().Get("ETag") != `"1"` {
		t.Fatalf("get as TS: %d etag %q", w.Code, w.Header().Get("ETag"))
	}
	if w := serve(t, store, http.MethodGet, "/emplacements/nope", "TOP_SECRET", ""); w.Code != 400 {
		t.Fatalf("malformed id: %d", w.Code)
	}
}

func TestHandlerCreate(t *testing.T) {
	store := newMemStore()
	body := `{"name":"Mast 4","mobility":"FIXED","sensorType":"EO_IR","position":{"lat":51,"lon":1},"coverage":{"minRangeM":0,"maxRangeM":20000,"azimuthWidthDeg":90},"feedId":"grpc-1"}`
	w := serve(t, store, http.MethodPost, "/emplacements", "UNCLASSIFIED", body)
	var e Emplacement
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &e) != nil || e.CreatedBy != "u-1" || e.Classification != "UNCLASSIFIED" || !e.Active || *e.FeedID != "grpc-1" {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	cases := map[string]string{
		"above clearance": `{"name":"x","mobility":"FIXED","sensorType":"RADAR","position":{"lat":1,"lon":1},"classification":"SECRET"}`,
		"unknown feed":    `{"name":"x","mobility":"FIXED","sensorType":"RADAR","position":{"lat":1,"lon":1},"feedId":"nope"}`,
		"outbound feed":   `{"name":"x","mobility":"FIXED","sensorType":"RADAR","position":{"lat":1,"lon":1},"feedId":"tak-out"}`,
		"fixed, no fix":   `{"name":"x","mobility":"FIXED","sensorType":"RADAR"}`,
	}
	for name, body := range cases {
		if w := serve(t, store, http.MethodPost, "/emplacements", "CONFIDENTIAL", body); w.Code != 422 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if w := serve(t, store, http.MethodPost, "/emplacements", "CONFIDENTIAL", `{"name":`); w.Code != 400 {
		t.Errorf("bad json: %d", w.Code)
	}
	if w := serve(t, store, http.MethodPost, "/emplacements", "CONFIDENTIAL", `{"name":"x","bogus":1}`); w.Code != 400 {
		t.Errorf("unknown field: %d", w.Code)
	}
}

func TestHandlerPatch(t *testing.T) {
	store := seeded()
	w := serve(t, store, http.MethodPatch, "/emplacements/"+idU, "CONFIDENTIAL", `{"boresightDeg":-10}`, "If-Match", `"3"`)
	var e Emplacement
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &e) != nil || e.BoresightDeg != 350 || e.Version != 4 || w.Header().Get("ETag") != `"4"` {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	// Review focus: two editors on version 3; the second is refused.
	if w := serve(t, store, http.MethodPatch, "/emplacements/"+idU, "CONFIDENTIAL", `{"description":"late"}`, "If-Match", `"3"`); w.Code != 412 {
		t.Fatalf("stale If-Match: %d", w.Code)
	}
	if w := serve(t, store, http.MethodPatch, "/emplacements/"+idU, "CONFIDENTIAL", `{}`, "If-Match", `"x"`); w.Code != 400 {
		t.Fatalf("malformed If-Match: %d", w.Code)
	}
	// Review focus: valid field by field, invalid as a whole.
	if w := serve(t, store, http.MethodPatch, "/emplacements/"+idM, "CONFIDENTIAL", `{"mobility":"FIXED"}`); w.Code != 422 {
		t.Fatalf("MOBILE→FIXED with no position: %d %s", w.Code, w.Body)
	}
	// Review focus: clearance on writes.
	if w := serve(t, store, http.MethodPatch, "/emplacements/"+idU, "CONFIDENTIAL", `{"classification":"SECRET"}`); w.Code != 422 {
		t.Fatalf("raising above own clearance: %d", w.Code)
	}
	if w := serve(t, store, http.MethodPatch, "/emplacements/"+idS, "CONFIDENTIAL", `{"description":"x"}`); w.Code != 404 {
		t.Fatalf("patching an invisible emplacement: %d", w.Code)
	}
	if store.rows[idS].Description != "" {
		t.Fatal("the invisible emplacement was changed")
	}
	// A wrong If-Match must not turn the 404 into a 412, and the right one must
	// not tell the caller they guessed the version.
	for _, v := range []string{`"9"`, `"1"`} {
		if w := serve(t, store, http.MethodPatch, "/emplacements/"+idS, "CONFIDENTIAL", `{"description":"x"}`, "If-Match", v); w.Code != 404 {
			t.Fatalf("patching an invisible emplacement with If-Match %s: %d", v, w.Code)
		}
	}
	if got := store.rows[idS]; got.Description != "" || got.Version != 1 {
		t.Fatal("the invisible emplacement was changed")
	}
}

func TestHandlerDelete(t *testing.T) {
	store := seeded()
	if w := serve(t, store, http.MethodDelete, "/emplacements/"+idS, "CONFIDENTIAL", ""); w.Code != 404 {
		t.Fatalf("deleting an invisible emplacement: %d", w.Code)
	}
	if w := serve(t, store, http.MethodDelete, "/emplacements/"+idU, "CONFIDENTIAL", ""); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
	if _, err := store.Get(context.Background(), idU); !errors.Is(err, ErrNotFound) {
		t.Fatal("not deleted")
	}
}

// A uuid in upper case names the same emplacement. The store, and through its
// notification every replica's cache, sees the lower-case id the rows are
// keyed by; otherwise the cache would keep the emplacement and its mirror keep
// reporting it.
func TestHandlerDeleteTakesAnUpperCaseID(t *testing.T) {
	const id = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	e := fixed("Headland")
	e.ID, e.Version = id, 1
	store := newMemStore(e)
	if w := serve(t, store, http.MethodDelete, "/emplacements/"+strings.ToUpper(id), "CONFIDENTIAL", ""); w.Code != 204 {
		t.Fatalf("delete by the upper-case id: %d %s", w.Code, w.Body)
	}
	if len(store.deleted) != 1 || store.deleted[0] != id {
		t.Fatalf("store deleted %v, want [%s]", store.deleted, id)
	}
}
