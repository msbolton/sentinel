package emplacement

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func feeds(_ context.Context, id string) (FeedInfo, error) {
	switch id {
	case "grpc-1", "grpc-2":
		return FeedInfo{Exists: true, Inbound: true, Driver: "grpc"}, nil
	case "tcp-in":
		return FeedInfo{Exists: true, Inbound: true, Driver: "tcp"}, nil
	case "tak-out":
		return FeedInfo{Exists: true, Inbound: false, Driver: "tak"}, nil
	}
	return FeedInfo{}, nil
}

func serve(t *testing.T, store Store, method, path, clearance, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	return serveWith(t, NewHandler(store, feeds, nil), method, path, clearance, body, headers...)
}

func serveWith(t *testing.T, h *Handler, method, path, clearance, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
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

// heartbeat comes from the owning replica's status, falls back to the row
// for a connection with none, and is absent for a row whose connection does
// not heartbeat (or that has none).
func TestHeartbeatComesFromTheConnectionsStatus(t *testing.T) {
	now := time.Now().UTC()
	silent := now.Add(-time.Minute).Truncate(time.Second)
	recent := now.Add(-2 * time.Second)
	sec := 5
	mk := func(id, feed string) Emplacement {
		e := senderEmplacement(MobilityFixed, "u1")
		e.ID, e.Name = id, id
		if feed == "" {
			e.FeedID, e.Active = nil, false
		} else {
			f := feed
			e.FeedID = &f
		}
		return e
	}
	online := mk("aaaaaaaa-0000-4000-8000-000000000001", "grpc-1")
	offline := mk("aaaaaaaa-0000-4000-8000-000000000002", "grpc-1")
	rowOnly := mk("aaaaaaaa-0000-4000-8000-000000000003", "grpc-2")
	rowOnly.LastHeartbeatAt, rowOnly.HeartbeatIntervalSec = &recent, &sec
	legacy := mk("aaaaaaaa-0000-4000-8000-000000000004", "tcp-in")
	unbound := mk("aaaaaaaa-0000-4000-8000-000000000005", "")
	h := NewHandler(newMemStore(online, offline, rowOnly, legacy, unbound), feeds, nil)
	h.Liveness = func(context.Context) (map[string]FeedLiveness, error) {
		return map[string]FeedLiveness{
			"grpc-1": {Driver: "grpc", Emplacements: []Liveness{
				{ID: online.ID, State: HeartbeatOnline},
				{ID: offline.ID, State: HeartbeatOffline, LastHeartbeatAt: &silent}}},
			"grpc-2": {Driver: "grpc"},
			"tcp-in": {Driver: "tcp"},
		}, nil
	}
	w := serveWith(t, h, http.MethodGet, "/emplacements", "SECRET", "")
	var list []map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	byID := map[string]map[string]any{}
	for _, e := range list {
		byID[e["id"].(string)] = e
	}
	if e := byID[online.ID]; e["heartbeat"] != "online" || e["silentSince"] != nil {
		t.Errorf("online = %v / %v", e["heartbeat"], e["silentSince"])
	}
	if e := byID[offline.ID]; e["heartbeat"] != "offline" || e["silentSince"] != silent.Format(time.RFC3339) {
		t.Errorf("offline = %v / %v", e["heartbeat"], e["silentSince"])
	}
	if e := byID[rowOnly.ID]; e["heartbeat"] != "online" {
		t.Errorf("a connection with no status, from the row = %v", e["heartbeat"])
	}
	for _, id := range []string{legacy.ID, unbound.ID} {
		if _, ok := byID[id]["heartbeat"]; ok {
			t.Errorf("%s carries a heartbeat: %v", id, byID[id]["heartbeat"])
		}
	}
	if w := serveWith(t, h, http.MethodGet, "/emplacements/"+offline.ID, "SECRET", ""); !strings.Contains(w.Body.String(), `"heartbeat":"offline"`) {
		t.Errorf("get: %s", w.Body)
	}
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

func TestCheckWriteHoldsToTheCeiling(t *testing.T) {
	e := New()
	e.Name, e.Mobility, e.SensorType = "x", MobilityMobile, "RF"
	e.Classification = "SECRET"
	e.Active = false                                      // an active one would need a connection too
	err := CheckWrite(context.Background(), &e, 1, feeds) // CONFIDENTIAL ceiling
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Problems) != 1 || ve.Problems[0].Path != "/classification" {
		t.Fatalf("CheckWrite = %v, want one /classification problem", err)
	}
	e.Classification = "CONFIDENTIAL"
	if err := CheckWrite(context.Background(), &e, 1, feeds); err != nil {
		t.Fatalf("CheckWrite at the ceiling = %v", err)
	}
}

// A sender reports a mobile emplacement's position; a PATCH that sets one
// is ignored for it, and the rest of the PATCH still applies.
func TestPatchIgnoresPositionOfASendersMobileEmplacement(t *testing.T) {
	e := senderEmplacement(MobilityMobile, SenderPrefix+"t1")
	store := newMemStore(e)
	res := serve(t, store, http.MethodPatch, "/emplacements/"+e.ID, "SECRET",
		`{"position":{"lat":10,"lon":10},"altitude":999,"description":"moved by hand"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("PATCH = %d %s", res.Code, res.Body)
	}
	got, _ := store.Get(context.Background(), e.ID)
	if got.Position.Lat != 50 || got.Altitude == nil || *got.Altitude != 100 || got.Description != "moved by hand" {
		t.Errorf("after PATCH: position %v, altitude %v, description %q", got.Position, got.Altitude, got.Description)
	}
}

// A person's own mobile emplacement, and a sender's fixed one, are moved by
// a PATCH; so is a sender's mobile one that the PATCH makes fixed.
func TestPatchPositionAppliesExceptWhileASendersEmplacementStaysMobile(t *testing.T) {
	cases := []struct {
		name, mobility, createdBy, body, wantMobility string
	}{
		{"person-made mobile", MobilityMobile, "u1", `{"position":{"lat":10,"lon":10}}`, MobilityMobile},
		{"sender-made fixed", MobilityFixed, SenderPrefix + "t1", `{"position":{"lat":10,"lon":10}}`, MobilityFixed},
		{"sender-made mobile made fixed", MobilityMobile, SenderPrefix + "t1", `{"mobility":"FIXED","position":{"lat":10,"lon":10}}`, MobilityFixed},
		{"sender-made mobile made fixed, lower case", MobilityMobile, SenderPrefix + "t1", `{"mobility":" fixed ","position":{"lat":10,"lon":10}}`, MobilityFixed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := senderEmplacement(c.mobility, c.createdBy)
			store := newMemStore(e)
			res := serve(t, store, http.MethodPatch, "/emplacements/"+e.ID, "SECRET", c.body)
			if res.Code != http.StatusOK {
				t.Fatalf("PATCH = %d %s", res.Code, res.Body)
			}
			got, _ := store.Get(context.Background(), e.ID)
			if got.Position.Lat != 10 || got.Position.Lon != 10 || got.Mobility != c.wantMobility {
				t.Errorf("after PATCH: position %v, mobility %s", got.Position, got.Mobility)
			}
		})
	}
}

func senderEmplacement(mobility, createdBy string) Emplacement {
	feed := "grpc-1"
	alt := 100.0
	e := New()
	e.ID, e.Name, e.Mobility, e.SensorType, e.FeedID, e.CreatedBy, e.Version = "11111111-1111-1111-1111-111111111111", "Ship ESM", mobility, "RF", &feed, createdBy, 1
	e.Position = &Point{Lat: 50, Lon: -4}
	e.Altitude = &alt
	return e
}

func TestAnActiveEmplacementNeedsAConnection(t *testing.T) {
	store := newMemStore()
	site := `"name":"Mast 5","mobility":"FIXED","sensorType":"RADAR","position":{"lat":51,"lon":1}`
	if w := serve(t, store, http.MethodPost, "/emplacements", "UNCLASSIFIED", `{`+site+`}`); w.Code != 422 || !strings.Contains(w.Body.String(), "/feedId") {
		t.Fatalf("active with no connection: %d %s", w.Code, w.Body)
	}
	// Inactive it may have none: the Unassigned group's rows.
	if w := serve(t, store, http.MethodPost, "/emplacements", "UNCLASSIFIED", `{`+site+`,"active":false}`); w.Code != 201 {
		t.Fatalf("inactive with no connection: %d %s", w.Code, w.Body)
	}
	// Unbinding an active one needs active:false in the same PATCH.
	bound := senderEmplacement(MobilityFixed, "u1")
	store.rows[bound.ID] = bound
	if w := serve(t, store, http.MethodPatch, "/emplacements/"+bound.ID, "SECRET", `{"feedId":""}`); w.Code != 422 {
		t.Fatalf("unbinding an active emplacement: %d %s", w.Code, w.Body)
	}
	if w := serve(t, store, http.MethodPatch, "/emplacements/"+bound.ID, "SECRET", `{"feedId":"","active":false}`); w.Code != 200 {
		t.Fatalf("unbinding and deactivating: %d %s", w.Code, w.Body)
	}
}

func TestBindingIsForGrpcConnectionsOnly(t *testing.T) {
	store := newMemStore()
	body := `{"name":"Mast 6","mobility":"FIXED","sensorType":"RADAR","position":{"lat":51,"lon":1},"feedId":"tcp-in"}`
	if w := serve(t, store, http.MethodPost, "/emplacements", "UNCLASSIFIED", body); w.Code != 422 || !strings.Contains(w.Body.String(), "grpc") {
		t.Fatalf("binding to a tcp connection: %d %s", w.Code, w.Body)
	}
	// A row bound to a tcp connection before the change can still be edited.
	legacy := senderEmplacement(MobilityFixed, "u1")
	tcp := "tcp-in"
	legacy.FeedID = &tcp
	store.rows[legacy.ID] = legacy
	if w := serve(t, store, http.MethodPatch, "/emplacements/"+legacy.ID, "SECRET", `{"description":"kept"}`); w.Code != 200 {
		t.Fatalf("editing a legacy tcp-bound row: %d %s", w.Code, w.Body)
	}
	// Moving it to a grpc connection is a binding, and allowed.
	if w := serve(t, store, http.MethodPatch, "/emplacements/"+legacy.ID, "SECRET", `{"feedId":"grpc-1"}`); w.Code != 200 {
		t.Fatalf("moving it to grpc: %d %s", w.Code, w.Body)
	}
}

func TestChangingTheConnectionResetsTheHeartbeat(t *testing.T) {
	e := senderEmplacement(MobilityFixed, "u1")
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	sec := 5
	e.LastHeartbeatAt, e.HeartbeatIntervalSec = &at, &sec
	store := newMemStore(e)
	if w := serve(t, store, http.MethodPatch, "/emplacements/"+e.ID, "SECRET", `{"description":"same connection"}`); w.Code != 200 {
		t.Fatalf("edit: %d %s", w.Code, w.Body)
	}
	if got := store.rows[e.ID]; got.LastHeartbeatAt == nil || got.HeartbeatIntervalSec == nil {
		t.Fatal("an edit that keeps the connection cleared the heartbeat")
	}
	if w := serve(t, store, http.MethodPatch, "/emplacements/"+e.ID, "SECRET", `{"feedId":"grpc-2"}`); w.Code != 200 {
		t.Fatalf("rebind: %d %s", w.Code, w.Body)
	}
	if got := store.rows[e.ID]; got.LastHeartbeatAt != nil || got.HeartbeatIntervalSec != nil {
		t.Fatalf("after a rebind: last %v, interval %v; want both cleared", got.LastHeartbeatAt, got.HeartbeatIntervalSec)
	}
}

// A client may send back what it read: the computed and recorded heartbeat
// fields are accepted and ignored.
func TestComputedFieldsAreIgnoredOnInput(t *testing.T) {
	e := senderEmplacement(MobilityFixed, "u1")
	store := newMemStore(e)
	body := `{"description":"from a GET","heartbeat":"online","silentSince":"2026-10-20T12:00:00Z","lastHeartbeatAt":"2026-10-20T12:00:00Z","heartbeatIntervalSec":5}`
	if w := serve(t, store, http.MethodPatch, "/emplacements/"+e.ID, "SECRET", body); w.Code != 200 {
		t.Fatalf("patch with read-only fields: %d %s", w.Code, w.Body)
	}
	if got := store.rows[e.ID]; got.LastHeartbeatAt != nil || got.HeartbeatIntervalSec != nil || got.Description != "from a GET" {
		t.Fatalf("after the patch: %+v", got)
	}
}
