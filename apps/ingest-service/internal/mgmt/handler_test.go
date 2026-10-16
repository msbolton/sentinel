package mgmt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sentinel/go-common/middleware"

	"github.com/sentinel/ingest-service/internal/aoi"
	"github.com/sentinel/ingest-service/internal/drivers"
	"github.com/sentinel/ingest-service/internal/registry"
	_ "github.com/sentinel/ingest-service/internal/source/all"
)

type memStore struct {
	feeds map[string]registry.Feed
	areas map[string]registry.Area
}

func newMemStore() *memStore {
	s := &memStore{feeds: map[string]registry.Feed{}, areas: map[string]registry.Area{}}
	for _, b := range registry.Builtins {
		f := b
		f.CreatedAt, f.UpdatedAt = time.Now(), time.Now()
		f.Version = 1
		s.feeds[f.ID] = f
	}
	return s
}

func (s *memStore) List(context.Context) ([]registry.Feed, error) {
	out := []registry.Feed{}
	for _, f := range s.feeds {
		out = append(out, f)
	}
	return out, nil
}
func (s *memStore) Get(_ context.Context, id string) (registry.Feed, error) {
	f, ok := s.feeds[id]
	if !ok {
		return registry.Feed{}, registry.ErrNotFound
	}
	return f, nil
}
func (s *memStore) Create(_ context.Context, f *registry.Feed) error {
	if _, exists := s.feeds[f.ID]; exists {
		return registry.ErrConflict
	}
	f.Version = 1
	s.feeds[f.ID] = *f
	return nil
}
func (s *memStore) Update(_ context.Context, f *registry.Feed) error {
	if _, ok := s.feeds[f.ID]; !ok {
		return registry.ErrNotFound
	}
	f.Version++
	s.feeds[f.ID] = *f
	return nil
}
func (s *memStore) SetEnabled(_ context.Context, id string, enabled bool) (registry.Feed, error) {
	f, ok := s.feeds[id]
	if !ok {
		return registry.Feed{}, registry.ErrNotFound
	}
	f.Enabled = enabled
	s.feeds[id] = f
	return f, nil
}
func (s *memStore) Delete(_ context.Context, id string) error {
	if _, ok := s.feeds[id]; !ok {
		return registry.ErrNotFound
	}
	delete(s.feeds, id)
	return nil
}
func (s *memStore) GetArea(_ context.Context, id string) (registry.Area, error) {
	a, ok := s.areas[id]
	if !ok {
		return registry.Area{FeedID: id, Boxes: []registry.Box{}}, nil
	}
	return a, nil
}
func (s *memStore) SetArea(_ context.Context, a registry.Area) error {
	if _, ok := s.feeds[a.FeedID]; !ok {
		return registry.ErrNotFound
	}
	s.areas[a.FeedID] = a
	return nil
}

type fakeRuntime struct {
	live map[string]bool
	last registry.Area
}

func (r *fakeRuntime) SetArea(id string, a registry.Area) (aoi.Status, bool) {
	if !r.live[id] {
		return aoi.Status{}, false
	}
	r.last = a
	return aoi.Status{Active: a.Active, Clients: a.Clients, AreaSqDeg: 42, EffectiveInterval: 10 * time.Second, CreditsRemaining: 3999}, true
}
func (r *fakeRuntime) AreaStatus(id string) (aoi.Status, bool) {
	if !r.live[id] {
		return aoi.Status{}, false
	}
	return aoi.Status{Active: r.last.Active, Idle: !r.last.Active, CreditsRemaining: 3999}, true
}

func newServer(t *testing.T, token string) (*httptest.Server, *memStore, *fakeRuntime) {
	t.Helper()
	store := newMemStore()
	rt := &fakeRuntime{live: map[string]bool{"opensky": true}}
	h := NewHandler(store, drivers.Default(), rt, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, Auth(AuthOptions{ServiceToken: token}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, store, rt
}

type reqOpts struct {
	token, roles, user string
}

func do(t *testing.T, srv *httptest.Server, method, path, body string, o reqOpts) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if o.token != "" {
		req.Header.Set(middleware.HeaderServiceToken, o.token)
	}
	if o.roles != "" {
		req.Header.Set(middleware.HeaderUserRoles, o.roles)
	}
	if o.user != "" {
		req.Header.Set(middleware.HeaderUserID, o.user)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf strings.Builder
	b := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(b)
		buf.Write(b[:n])
		if err != nil {
			break
		}
	}
	return resp, []byte(buf.String())
}

var operator = reqOpts{roles: "sentinel-operator", user: "u1"}

func TestListAndFilters(t *testing.T) {
	srv, _, _ := newServer(t, "")
	resp, body := do(t, srv, "GET", "/feeds", "", reqOpts{})
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var feeds []feedDTO
	if err := json.Unmarshal(body, &feeds); err != nil || len(feeds) != 3 {
		t.Fatalf("feeds = %s err %v", body, err)
	}
	for _, f := range feeds {
		if f.Status.State != registry.StateStopped || f.Status.Health != registry.HealthUnknown {
			t.Errorf("default status = %+v", f.Status)
		}
		if f.ID == "opensky" && (f.Source != "OPENSKY" || len(f.Capabilities) != 2) {
			t.Errorf("opensky dto = %+v", f)
		}
	}
	_, body = do(t, srv, "GET", "/feeds?capability=area", "", reqOpts{})
	if err := json.Unmarshal(body, &feeds); err != nil || len(feeds) != 2 {
		t.Fatalf("area filter = %s", body)
	}
	for _, f := range feeds {
		if f.ID != "opensky" && f.ID != "adsb-lol" {
			t.Errorf("unexpected area feed %s", f.ID)
		}
	}
	_, body = do(t, srv, "GET", "/feeds?capability=poll&driver=celestrak", "", reqOpts{})
	if err := json.Unmarshal(body, &feeds); err != nil || len(feeds) != 1 || feeds[0].ID != "celestrak" {
		t.Fatalf("driver filter = %s", body)
	}
	_, body = do(t, srv, "GET", "/feeds?enabled=true", "", reqOpts{})
	if err := json.Unmarshal(body, &feeds); err != nil || len(feeds) != 0 {
		t.Fatalf("enabled filter = %s", body)
	}
}

func TestCreatePatchEnableDelete(t *testing.T) {
	srv, store, _ := newServer(t, "")
	body := `{"name":"Site MQTT","driver":"stream","config":{"transport":{"kind":"mqtt","brokerUrl":"tcp://b:1883","topics":["x/#"]},"decoder":{"name":"json"}}}`
	resp, out := do(t, srv, "POST", "/feeds", body, operator)
	if resp.StatusCode != 201 {
		t.Fatalf("create status %d: %s", resp.StatusCode, out)
	}
	var created feedDTO
	_ = json.Unmarshal(out, &created)
	if created.ID != "site-mqtt" || created.CreatedBy != "u1" || created.Enabled || created.Version != 1 {
		t.Fatalf("created = %+v", created)
	}
	if resp, _ := do(t, srv, "POST", "/feeds", body, operator); resp.StatusCode != 409 {
		t.Fatalf("duplicate status %d", resp.StatusCode)
	}

	resp, out = do(t, srv, "PATCH", "/feeds/site-mqtt", `{"config":{"framing":"line"},"description":"d"}`, operator)
	if resp.StatusCode != 200 {
		t.Fatalf("patch status %d: %s", resp.StatusCode, out)
	}
	var patched feedDTO
	_ = json.Unmarshal(out, &patched)
	var cfg map[string]any
	_ = json.Unmarshal(patched.Config, &cfg)
	if cfg["framing"] != "line" || cfg["transport"] == nil || patched.Description != "d" || patched.Version != 2 {
		t.Fatalf("patched = %+v cfg %v", patched, cfg)
	}
	if resp, _ := do(t, srv, "PATCH", "/feeds/site-mqtt", `{"driver":"opensky"}`, operator); resp.StatusCode != 400 {
		t.Fatalf("immutable field status %d", resp.StatusCode)
	}

	resp, out = do(t, srv, "POST", "/feeds/site-mqtt/enable", "", operator)
	if resp.StatusCode != 200 || !strings.Contains(string(out), `"enabled":true`) {
		t.Fatalf("enable status %d: %s", resp.StatusCode, out)
	}
	if resp, _ := do(t, srv, "DELETE", "/feeds/opensky", "", operator); resp.StatusCode != 403 {
		t.Fatalf("builtin delete status %d", resp.StatusCode)
	}
	if resp, _ := do(t, srv, "DELETE", "/feeds/site-mqtt", "", operator); resp.StatusCode != 204 {
		t.Fatalf("delete status %d", resp.StatusCode)
	}
	if _, ok := store.feeds["site-mqtt"]; ok {
		t.Fatal("feed still stored after delete")
	}
	if resp, _ := do(t, srv, "GET", "/feeds/site-mqtt", "", reqOpts{}); resp.StatusCode != 404 {
		t.Fatalf("get after delete status %d", resp.StatusCode)
	}
}

func TestValidationErrors(t *testing.T) {
	srv, _, _ := newServer(t, "")
	resp, out := do(t, srv, "POST", "/feeds", `{"id":"Bad!","name":"x","driver":"opensky","config":{"intervalSec":0,"clientSecret":"s"},"secretRefs":{"clientSecret":"plain"}}`, operator)
	if resp.StatusCode != 422 {
		t.Fatalf("status %d: %s", resp.StatusCode, out)
	}
	var e errorResponse
	_ = json.Unmarshal(out, &e)
	paths := map[string]bool{}
	for _, p := range e.Details {
		paths[p.Path] = true
	}
	for _, want := range []string{"/id", "/config/intervalSec", "/config/clientSecret", "/secretRefs/clientSecret"} {
		if !paths[want] {
			t.Errorf("missing %s in %+v", want, e.Details)
		}
	}
	if resp, _ := do(t, srv, "POST", "/feeds", `not json`, operator); resp.StatusCode != 400 {
		t.Fatalf("bad json status %d", resp.StatusCode)
	}
	if resp, _ := do(t, srv, "PUT", "/feeds/opensky", `{"name":"x"}`, operator); resp.StatusCode != 400 {
		t.Fatalf("partial PUT status %d", resp.StatusCode)
	}
}

func TestAuthTokenAndRoles(t *testing.T) {
	srv, _, _ := newServer(t, "secret")
	if resp, _ := do(t, srv, "GET", "/feeds", "", reqOpts{}); resp.StatusCode != 401 {
		t.Fatalf("no token status %d", resp.StatusCode)
	}
	if resp, _ := do(t, srv, "GET", "/feeds", "", reqOpts{token: "wrong"}); resp.StatusCode != 401 {
		t.Fatalf("wrong token status %d", resp.StatusCode)
	}
	if resp, _ := do(t, srv, "GET", "/feeds", "", reqOpts{token: "secret"}); resp.StatusCode != 200 {
		t.Fatalf("token-only GET status %d", resp.StatusCode)
	}
	if resp, _ := do(t, srv, "POST", "/feeds/opensky/enable", "", reqOpts{token: "secret", roles: "sentinel-viewer"}); resp.StatusCode != 403 {
		t.Fatalf("viewer mutation status %d", resp.StatusCode)
	}
	if resp, _ := do(t, srv, "POST", "/feeds/opensky/enable", "", reqOpts{token: "secret", roles: "sentinel-admin"}); resp.StatusCode != 200 {
		t.Fatalf("admin mutation status %d", resp.StatusCode)
	}
	// The gateway's area push carries no user.
	if resp, _ := do(t, srv, "PUT", "/feeds/opensky/area", `{"active":false,"clients":0,"boxes":[]}`, reqOpts{token: "secret"}); resp.StatusCode != 200 {
		t.Fatalf("service area push status %d", resp.StatusCode)
	}
}

func TestAreaEndpoints(t *testing.T) {
	srv, store, rt := newServer(t, "")
	body := `{"active":true,"clients":2,"boxes":[{"north":42,"south":41,"east":-87,"west":-88}]}`
	resp, out := do(t, srv, "PUT", "/feeds/opensky/area", body, reqOpts{})
	if resp.StatusCode != 200 {
		t.Fatalf("put area status %d: %s", resp.StatusCode, out)
	}
	var a areaResponse
	_ = json.Unmarshal(out, &a)
	if !a.Live || !a.Active || a.AreaSqDeg != 42 || a.EffectiveIntervalSec != 10 || a.CreditsRemaining == nil || *a.CreditsRemaining != 3999 || a.ExpiresAt == nil {
		t.Fatalf("live area = %+v", a)
	}
	if stored := store.areas["opensky"]; !stored.Active || stored.Clients != 2 || stored.ExpiresAt == nil {
		t.Fatalf("stored area = %+v", stored)
	}
	if rt.last.Clients != 2 {
		t.Fatalf("runtime did not receive the area: %+v", rt.last)
	}
	// The pre-registry viewport alias is gone.
	if resp, _ := do(t, srv, "PUT", "/feeds/opensky/viewport", body, reqOpts{}); resp.StatusCode != 404 && resp.StatusCode != 405 {
		t.Fatalf("viewport alias should be gone, status %d", resp.StatusCode)
	}
	resp, out = do(t, srv, "GET", "/feeds/opensky/area", "", reqOpts{})
	if resp.StatusCode != 200 || !strings.Contains(string(out), `"live":true`) {
		t.Fatalf("get area status %d: %s", resp.StatusCode, out)
	}
	// Not running here: answered from the row.
	rt.live = map[string]bool{}
	resp, out = do(t, srv, "GET", "/feeds/opensky/area", "", reqOpts{})
	_ = json.Unmarshal(out, &a)
	if resp.StatusCode != 200 || a.Live || !a.Active || a.Clients != 2 || !a.Idle {
		t.Fatalf("row area = %+v", a)
	}

	cases := map[string]int{
		`{"active":true,"clients":1,"boxes":[]}`:                                        400,
		`{"active":true,"clients":1,"boxes":[{"north":1,"south":2,"east":1,"west":0}]}`: 400,
		`{"active":true,"clients":1,"boxes":[{},{},{},{},{}]}`:                          400,
		`{"active":false,"clients":-1,"boxes":[]}`:                                      400,
	}
	for body, want := range cases {
		if resp, _ := do(t, srv, "PUT", "/feeds/opensky/area", body, reqOpts{}); resp.StatusCode != want {
			t.Errorf("%s: status %d want %d", body, resp.StatusCode, want)
		}
	}
	if resp, _ := do(t, srv, "PUT", "/feeds/celestrak/area", body, reqOpts{}); resp.StatusCode != 409 {
		t.Fatalf("unsupported driver status %d", resp.StatusCode)
	}
	if resp, _ := do(t, srv, "PUT", "/feeds/nope/area", body, reqOpts{}); resp.StatusCode != 404 {
		t.Fatalf("unknown feed status %d", resp.StatusCode)
	}
}

func TestDrivers(t *testing.T) {
	srv, _, _ := newServer(t, "")
	resp, out := do(t, srv, "GET", "/drivers", "", reqOpts{})
	var list []drivers.Info
	if resp.StatusCode != 200 || json.Unmarshal(out, &list) != nil || len(list) < 4 {
		t.Fatalf("drivers = %d %s", resp.StatusCode, out)
	}
	resp, out = do(t, srv, "GET", "/drivers/opensky", "", reqOpts{})
	var info drivers.Info
	if resp.StatusCode != 200 || json.Unmarshal(out, &info) != nil || len(info.ConfigSchema) == 0 || len(info.SecretFields) != 2 {
		t.Fatalf("driver = %d %s", resp.StatusCode, out)
	}
	if resp, _ := do(t, srv, "GET", "/drivers/nope", "", reqOpts{}); resp.StatusCode != 404 {
		t.Fatalf("unknown driver status %d", resp.StatusCode)
	}
}

func TestMergeConfig(t *testing.T) {
	out, err := mergeConfig(json.RawMessage(`{"a":1,"b":{"x":1}}`), json.RawMessage(`{"b":{"y":2},"c":3,"a":null}`))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	if _, ok := m["a"]; ok {
		t.Error("null must delete the key")
	}
	if b := m["b"].(map[string]any); b["y"] != 2.0 || b["x"] != nil {
		t.Errorf("nested objects replace, not deep-merge: %v", b)
	}
	if m["c"] != 3.0 {
		t.Errorf("new key missing: %v", m)
	}
}
