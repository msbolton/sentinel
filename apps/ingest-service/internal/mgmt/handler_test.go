package mgmt

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sentinel/go-common/middleware"

	"github.com/sentinel/ingest-service/internal/aoi"
	"github.com/sentinel/ingest-service/internal/drivers"
	"github.com/sentinel/ingest-service/internal/registry"
	_ "github.com/sentinel/ingest-service/internal/source/all"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

type memStore struct {
	// mu stands in for the real store's advisory lock: a save's guard check
	// and write are one step. It also keeps concurrent requests off the maps.
	mu          sync.Mutex
	feeds       map[string]registry.Feed
	areas       map[string]registry.Area
	replicas    []registry.Replica
	replicasErr error
	listErr     error
	// guards records, for each create and update, whether it came with a port guard.
	guards []bool
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := []registry.Feed{}
	for _, f := range s.feeds {
		out = append(out, f)
	}
	return out, nil
}
func (s *memStore) Get(_ context.Context, id string) (registry.Feed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.feeds[id]
	if !ok {
		return registry.Feed{}, registry.ErrNotFound
	}
	return f, nil
}

// check runs a save's guard against every stored feed, the way the real store
// does under its lock, and refuses the save with the problems it reports. The
// caller holds mu.
func (s *memStore) check(f registry.Feed, g registry.Guard) error {
	s.guards = append(s.guards, g != nil)
	if g == nil {
		return nil
	}
	others := make([]registry.Feed, 0, len(s.feeds))
	for _, o := range s.feeds {
		// Only the fields the real store reads for a guard (see registry.Guard),
		// so that a guard cannot lean on one it will not get.
		others = append(others, registry.Feed{ID: o.ID, Name: o.Name, Driver: o.Driver, Config: o.Config, Enabled: o.Enabled})
	}
	if problems := g(f, others); len(problems) > 0 {
		return &registry.ValidationError{Problems: problems}
	}
	return nil
}

func (s *memStore) CreateGuarded(_ context.Context, f *registry.Feed, g registry.Guard) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(*f, g); err != nil {
		return err
	}
	if _, exists := s.feeds[f.ID]; exists {
		return registry.ErrConflict
	}
	f.Version = 1
	s.feeds[f.ID] = *f
	return nil
}
func (s *memStore) UpdateGuarded(_ context.Context, f *registry.Feed, g registry.Guard) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(*f, g); err != nil {
		return err
	}
	if _, ok := s.feeds[f.ID]; !ok {
		return registry.ErrNotFound
	}
	f.Version++
	s.feeds[f.ID] = *f
	return nil
}
func (s *memStore) SetEnabled(_ context.Context, id string, enabled bool) (registry.Feed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.feeds[id]
	if !ok {
		return registry.Feed{}, registry.ErrNotFound
	}
	f.Enabled = enabled
	s.feeds[id] = f
	return f, nil
}
func (s *memStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.feeds[id]; !ok {
		return registry.ErrNotFound
	}
	delete(s.feeds, id)
	return nil
}
func (s *memStore) GetArea(_ context.Context, id string) (registry.Area, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.areas[id]
	if !ok {
		return registry.Area{FeedID: id, Boxes: []registry.Box{}}, nil
	}
	return a, nil
}
func (s *memStore) LiveReplicas(context.Context) ([]registry.Replica, error) {
	return s.replicas, s.replicasErr
}
func (s *memStore) SetArea(_ context.Context, a registry.Area) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.feeds[a.FeedID]; !ok {
		return registry.ErrNotFound
	}
	s.areas[a.FeedID] = a
	return nil
}

type fakeRuntime struct {
	live  map[string]bool
	last  registry.Area
	snaps map[string]registry.Status
}

func (r *fakeRuntime) Snapshot(id string) (registry.Status, bool) {
	st, ok := r.snaps[id]
	return st, ok
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
	// class is the caller's clearance, as the gateway propagates it.
	class string
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
	if o.class != "" {
		req.Header.Set(middleware.HeaderClassification, o.class)
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
	if err := json.Unmarshal(body, &feeds); err != nil || len(feeds) != len(registry.Builtins) {
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
	// Every built-in takes a viewport except intelsky, which polls the globe.
	areaFeeds := map[string]bool{"opensky": true, "celestrak": true, "aisstream": true}
	_, body = do(t, srv, "GET", "/feeds?capability=area", "", reqOpts{})
	if err := json.Unmarshal(body, &feeds); err != nil || len(feeds) != len(areaFeeds) {
		t.Fatalf("area filter = %s", body)
	}
	for _, f := range feeds {
		if !areaFeeds[f.ID] {
			t.Errorf("unexpected area feed %s", f.ID)
		}
	}
	_, body = do(t, srv, "GET", "/feeds?capability=listen", "", reqOpts{})
	if err := json.Unmarshal(body, &feeds); err != nil || len(feeds) != 0 {
		t.Fatalf("listen filter = %s", body)
	}
	_, body = do(t, srv, "GET", "/feeds?capability=poll&driver=celestrak", "", reqOpts{})
	if err := json.Unmarshal(body, &feeds); err != nil || len(feeds) != 1 || feeds[0].ID != "celestrak" {
		t.Fatalf("driver filter = %s", body)
	}
	_, body = do(t, srv, "GET", "/feeds?enabled=true", "", reqOpts{})
	// No built-in feed is enabled out of the box: each needs credentials or an operator.
	if err := json.Unmarshal(body, &feeds); err != nil || len(feeds) != 0 {
		t.Fatalf("enabled filter = %s", body)
	}
	// Every built-in feed is a source: direction=in lists all, out none.
	_, body = do(t, srv, "GET", "/feeds?direction=in", "", reqOpts{})
	if err := json.Unmarshal(body, &feeds); err != nil || len(feeds) != len(registry.Builtins) || feeds[0].Direction != "in" {
		t.Fatalf("direction=in filter = %s", body)
	}
	_, body = do(t, srv, "GET", "/feeds?direction=out", "", reqOpts{})
	if err := json.Unmarshal(body, &feeds); err != nil || len(feeds) != 0 {
		t.Fatalf("direction=out filter = %s", body)
	}
	if !matchesDirection("both", "in") || !matchesDirection("both", "out") || matchesDirection("in", "both") || matchesDirection("out", "in") {
		t.Error("matchesDirection")
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
	// A stream feed has no area capability.
	resp, out = do(t, srv, "POST", "/feeds", `{"name":"Site MQTT","driver":"stream","config":{"transport":{"kind":"mqtt","brokerUrl":"tcp://b:1883","topics":["x/#"]},"decoder":{"name":"json"}}}`, operator)
	if resp.StatusCode != 201 {
		t.Fatalf("create stream feed status %d: %s", resp.StatusCode, out)
	}
	if resp, _ := do(t, srv, "PUT", "/feeds/site-mqtt/area", body, reqOpts{}); resp.StatusCode != 409 {
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
	if resp.StatusCode != 200 || json.Unmarshal(out, &info) != nil || len(info.ConfigSchema) == 0 || len(info.SecretFields) != 2 || info.Direction != "in" {
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

func TestLiveSnapshotAndStaleRows(t *testing.T) {
	srv, store, rt := newServer(t, "")
	now := time.Now().UTC()
	old := now.Add(-time.Minute)
	f := store.feeds["celestrak"]
	f.Status = &registry.Status{Owner: "other-replica", State: registry.StateRunning, Health: registry.HealthHealthy, UpdatedAt: &old, Detail: json.RawMessage("{}")}
	store.feeds["celestrak"] = f
	rt.snaps = map[string]registry.Status{"opensky": {Owner: "me", State: registry.StateIdle, Health: registry.HealthHealthy, Detail: json.RawMessage(`{"leaseToken":3}`)}}
	_, body := do(t, srv, "GET", "/feeds", "", reqOpts{})
	var feeds []feedDTO
	if err := json.Unmarshal(body, &feeds); err != nil {
		t.Fatal(err)
	}
	for _, d := range feeds {
		switch d.ID {
		case "opensky":
			if d.Status.State != registry.StateIdle || d.Status.Owner != "me" || d.Status.UpdatedAt == nil {
				t.Errorf("live snapshot not used: %+v", d.Status)
			}
		case "celestrak":
			if d.Status.State != registry.StateUnassigned || d.Status.Health != registry.HealthUnknown || d.Status.Owner != "other-replica" {
				t.Errorf("stale row should render unassigned: %+v", d.Status)
			}
		}
	}
	_, body = do(t, srv, "GET", "/feeds/celestrak/status", "", reqOpts{})
	if !strings.Contains(string(body), `"state":"unassigned"`) {
		t.Errorf("status endpoint = %s", body)
	}
}

func TestListeningPortsMustBeUnique(t *testing.T) {
	srv, _, _ := newServer(t, "")
	feed := func(name string) string {
		return `{"name":"` + name + `","driver":"stream","config":{"transport":{"kind":"udp","address":":10110"},"decoder":{"name":"nmea-ais"}}}`
	}
	if resp, out := do(t, srv, "POST", "/feeds", feed("Pier AIS"), operator); resp.StatusCode != 201 {
		t.Fatalf("first: %d %s", resp.StatusCode, out)
	}
	resp, out := do(t, srv, "POST", "/feeds", feed("Dock AIS"), operator)
	if resp.StatusCode != 422 || !strings.Contains(string(out), "/config/transport/address") || !strings.Contains(string(out), "pier-ais") {
		t.Errorf("duplicate port: %d %s", resp.StatusCode, out)
	}
	bad := `{"name":"Bad allow","driver":"stream","config":{"transport":{"kind":"udp","address":":10111","allow":["10.0.0.0/33"]},"decoder":{"name":"nmea-ais"}}}`
	if resp, out := do(t, srv, "POST", "/feeds", bad, operator); resp.StatusCode != 422 || !strings.Contains(string(out), "/config/transport/allow") {
		t.Errorf("bad allowlist: %d %s", resp.StatusCode, out)
	}
	// A listener with no numeric port is refused at create, not stored to fail at bind time.
	for _, addr := range []string{"localhost", ":http"} {
		noPort := `{"name":"No port","driver":"stream","config":{"transport":{"kind":"udp","address":"` + addr + `"},"decoder":{"name":"nmea-ais"}}}`
		if resp, out := do(t, srv, "POST", "/feeds", noPort, operator); resp.StatusCode != 422 {
			t.Errorf("address %q: %d %s", addr, resp.StatusCode, out)
		}
	}
}

func TestListeningPortChecksRunOnUpdate(t *testing.T) {
	store := newMemStore()
	h := NewHandler(store, drivers.Default(), &fakeRuntime{}, nil)
	h.Reserved = registry.PortSet{5432: true}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, Auth(AuthOptions{}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	feed := func(name, addr string) string {
		return `{"name":"` + name + `","driver":"stream","config":{"transport":{"kind":"udp","address":"` + addr + `"},"decoder":{"name":"nmea-ais"}}}`
	}
	for name, addr := range map[string]string{"Pier AIS": ":10110", "Dock AIS": ":10111"} {
		if resp, out := do(t, srv, "POST", "/feeds", feed(name, addr), operator); resp.StatusCode != 201 {
			t.Fatalf("create %s: %d %s", name, resp.StatusCode, out)
		}
	}
	// The merged config is what gets checked: moving onto another feed's port, or a reserved one, is refused.
	for _, addr := range []string{":10110", ":5432", ":514"} {
		patch := `{"config":{"transport":{"kind":"udp","address":"` + addr + `"}}}`
		if resp, out := do(t, srv, "PATCH", "/feeds/dock-ais", patch, operator); resp.StatusCode != 422 || !strings.Contains(string(out), "/config/transport/address") {
			t.Errorf("patch to %s: %d %s", addr, resp.StatusCode, out)
		}
	}
	// Saving a feed on the port it already holds does not clash with itself.
	if resp, out := do(t, srv, "PATCH", "/feeds/dock-ais", `{"description":"quay side"}`, operator); resp.StatusCode != 200 {
		t.Errorf("unchanged port: %d %s", resp.StatusCode, out)
	}
	if resp, out := do(t, srv, "PATCH", "/feeds/dock-ais", `{"config":{"transport":{"kind":"udp","address":":10112"}}}`, operator); resp.StatusCode != 200 {
		t.Errorf("free port: %d %s", resp.StatusCode, out)
	}
}

// The store takes its lock and runs the port check only for a save whose new
// config listens; a connection that binds no port has nothing to check.
func TestOnlyListeningSavesCarryAPortGuard(t *testing.T) {
	srv, store, _ := newServer(t, "")
	const (
		client = `{"kind":"tcpclient","address":"ais.example:4001"}`
		udp    = `{"kind":"udp","address":":47000"}`
	)
	steps := []struct {
		what, method, path, body string
		guarded                  bool
	}{
		{"create a client", "POST", "/feeds", `{"name":"Site","driver":"stream","config":{"transport":` + client + `,"decoder":{"name":"nmea-ais"}}}`, false},
		{"patch it onto a port", "PATCH", "/feeds/site", `{"config":{"transport":` + udp + `}}`, true},
		{"patch a listener's description", "PATCH", "/feeds/site", `{"description":"quay side"}`, true},
		{"replace the listener", "PUT", "/feeds/site", `{"name":"Site","config":{"transport":` + udp + `,"decoder":{"name":"nmea-ais"}},"enabled":false}`, true},
		{"patch it back to a client", "PATCH", "/feeds/site", `{"config":{"transport":` + client + `}}`, false},
		{"create a udp connection", "POST", "/feeds", `{"name":"Pier","driver":"udp","config":{"port":47001}}`, true},
	}
	for _, st := range steps {
		store.guards = nil
		if resp, out := do(t, srv, st.method, st.path, st.body, operator); resp.StatusCode/100 != 2 {
			t.Fatalf("%s: %d %s", st.what, resp.StatusCode, out)
		}
		if len(store.guards) != 1 || store.guards[0] != st.guarded {
			t.Errorf("%s: the store saw guards %v, want [%v]", st.what, store.guards, st.guarded)
		}
	}
}

// sendTogether sends each {path, body} request at the same moment and returns
// their status codes in order (0 for a request that got no response).
func sendTogether(srv *httptest.Server, method string, reqs [][2]string) []int {
	codes := make([]int, len(reqs))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, r := range reqs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequest(method, srv.URL+r[0], strings.NewReader(r[1]))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(middleware.HeaderUserRoles, operator.roles)
			req.Header.Set(middleware.HeaderUserID, operator.user)
			<-start
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			resp.Body.Close()
			codes[i] = resp.StatusCode
		}()
	}
	close(start)
	wg.Wait()
	return codes
}

// Saves that arrive together and choose the same port: the handler leaves the
// check to the store, which makes it and writes as one step, so exactly one is
// stored and the rest are refused.
func TestConcurrentSavesCannotShareAPort(t *testing.T) {
	srv, _, _ := newServer(t, "")
	count := func(codes []int, status int) (n int) {
		for _, c := range codes {
			if c == status {
				n++
			}
		}
		return n
	}

	var creates [][2]string
	for _, name := range []string{"Dock 1", "Dock 2", "Dock 3", "Dock 4", "Dock 5", "Dock 6"} {
		creates = append(creates, [2]string{"/feeds", `{"name":"` + name + `","driver":"udp","config":{"port":47000}}`})
	}
	if codes := sendTogether(srv, "POST", creates); count(codes, 201) != 1 || count(codes, 422) != len(creates)-1 {
		t.Fatalf("creates on one port: statuses %v, want one 201 and the rest 422", codes)
	}

	// Two connections move onto the same free port.
	for _, body := range []string{
		`{"name":"Pier A","driver":"udp","config":{"port":47001}}`,
		`{"name":"Pier B","driver":"udp","config":{"port":47002}}`,
	} {
		if resp, out := do(t, srv, "POST", "/feeds", body, operator); resp.StatusCode != 201 {
			t.Fatalf("create %s: %d %s", body, resp.StatusCode, out)
		}
	}
	moves := [][2]string{{"/feeds/pier-a", `{"config":{"port":47010}}`}, {"/feeds/pier-b", `{"config":{"port":47010}}`}}
	if codes := sendTogether(srv, "PATCH", moves); count(codes, 200) != 1 || count(codes, 422) != 1 {
		t.Fatalf("moves onto one port: statuses %v, want one 200 and one 422", codes)
	}
}

// Listening connections show why they are not running while no listener
// replica is live, and only then.
func TestListeningConnectionWithoutAListenerIsExplained(t *testing.T) {
	srv, store, rt := newServer(t, "")
	create := func(name, addr, extra string) string {
		body := `{"name":"` + name + `","driver":"stream","enabled":true,"config":{"transport":{"kind":"udp","address":"` + addr + `"},"decoder":{"name":"nmea-ais"}` + extra + `}}`
		resp, out := do(t, srv, "POST", "/feeds", body, operator)
		if resp.StatusCode != 201 {
			t.Fatalf("create %s: %d %s", name, resp.StatusCode, out)
		}
		var d feedDTO
		if err := json.Unmarshal(out, &d); err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	read := func(path string) registry.Status {
		t.Helper()
		resp, body := do(t, srv, "GET", path, "", reqOpts{})
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s: %d %s", path, resp.StatusCode, body)
		}
		if strings.HasSuffix(path, "/status") {
			var st registry.Status
			if err := json.Unmarshal(body, &st); err != nil {
				t.Fatal(err)
			}
			return st
		}
		var d feedDTO
		if err := json.Unmarshal(body, &d); err != nil {
			t.Fatal(err)
		}
		return d.Status
	}
	id := create("Pier AIS", ":10110", "")

	// No listener replica: get, status and list all say so.
	for _, path := range []string{"/feeds/" + id, "/feeds/" + id + "/status"} {
		if st := read(path); st.State != registry.StateUnassigned || st.Health != registry.HealthUnknown || st.LastError != registry.NoListenerError {
			t.Errorf("%s with no listener = %+v", path, st)
		}
	}
	_, body := do(t, srv, "GET", "/feeds", "", reqOpts{})
	var feeds []feedDTO
	if err := json.Unmarshal(body, &feeds); err != nil {
		t.Fatal(err)
	}
	for _, d := range feeds {
		listening := d.ID == id
		if got := d.Status.LastError == registry.NoListenerError; got != listening {
			t.Errorf("list: feed %s has the no-listener error = %v, want %v", d.ID, got, listening)
		}
	}

	// The create, update and enable responses are not decorated with it.
	_, out := do(t, srv, "POST", "/feeds/"+id+"/enable", "", operator)
	if strings.Contains(string(out), registry.NoListenerError) {
		t.Errorf("enable response carries the no-listener error: %s", out)
	}

	// A disabled listening connection is not waiting for anything.
	do(t, srv, "POST", "/feeds/"+id+"/disable", "", operator)
	if st := read("/feeds/" + id); st.LastError == registry.NoListenerError {
		t.Errorf("disabled connection: %+v", st)
	}
	do(t, srv, "POST", "/feeds/"+id+"/enable", "", operator)

	// A stored status survives in the other fields.
	f := store.feeds[id]
	f.Status = &registry.Status{FeedID: id, Owner: "r-old", MessagesTotal: 42, State: registry.StateRunning, Health: registry.HealthHealthy, Detail: json.RawMessage("{}")}
	store.feeds[id] = f
	if st := read("/feeds/" + id); st.State != registry.StateUnassigned || st.LastError != registry.NoListenerError || st.MessagesTotal != 42 {
		t.Errorf("stored status overlaid = %+v", st)
	}

	// An ordinary replica is no listener.
	store.replicas = []registry.Replica{{ID: "r-plain"}}
	if st := read("/feeds/" + id); st.State != registry.StateUnassigned || st.LastError != registry.NoListenerError {
		t.Errorf("with only an ordinary replica live = %+v", st)
	}

	// A live listener replica clears it, a dedicated one and a shared one alike.
	for _, listener := range []registry.Replica{
		{ID: "r-listen", Listener: true, Host: "ingest.example"},
		{ID: "r-listen", Listener: true, Shared: true, Host: "ingest.example"},
	} {
		store.replicas = []registry.Replica{listener}
		if st := read("/feeds/" + id); st.LastError == registry.NoListenerError || st.State != registry.StateRunning {
			t.Errorf("with the listener %+v the stored status stands: %+v", listener, st)
		}
	}

	// A failed lookup says nothing either way, with a listener live or not.
	store.replicasErr = errors.New("database down")
	for _, replicas := range [][]registry.Replica{nil, {{ID: "r-listen", Listener: true}}} {
		store.replicas = replicas
		for _, path := range []string{"/feeds/" + id, "/feeds/" + id + "/status"} {
			if st := read(path); st.LastError == registry.NoListenerError || st.State != registry.StateRunning {
				t.Errorf("%s with a failed lookup and replicas %v = %+v", path, replicas, st)
			}
		}
	}
	store.replicas, store.replicasErr = nil, nil

	// A connection this replica runs shows its live status, whatever the
	// replica lookup says: LEASE_ENABLED=false runs every feed in one process.
	rt.snaps = map[string]registry.Status{id: {State: registry.StateRunning, Health: registry.HealthHealthy, MessagesTotal: 7, Detail: json.RawMessage("{}")}}
	for _, lookupErr := range []error{nil, errors.New("database down")} {
		store.replicasErr = lookupErr
		for _, path := range []string{"/feeds/" + id, "/feeds/" + id + "/status"} {
			if st := read(path); st.LastError == registry.NoListenerError || st.State != registry.StateRunning || st.MessagesTotal != 7 {
				t.Errorf("%s running here (lookup error %v) = %+v", path, lookupErr, st)
			}
		}
		_, body := do(t, srv, "GET", "/feeds", "", reqOpts{})
		var feeds []feedDTO
		if err := json.Unmarshal(body, &feeds); err != nil {
			t.Fatal(err)
		}
		for _, d := range feeds {
			if d.ID == id && (d.Status.LastError == registry.NoListenerError || d.Status.State != registry.StateRunning) {
				t.Errorf("list: running here (lookup error %v) = %+v", lookupErr, d.Status)
			}
		}
	}
	store.replicasErr, rt.snaps = nil, nil

	// A connection pinned to a replica runs there, not on a listener, so a
	// missing listener says nothing about it.
	pinned := create("Dock AIS", ":10111", `,"pinnedReplica":"r-host"`)
	if st := read("/feeds/" + pinned); st.LastError == registry.NoListenerError {
		t.Errorf("pinned connection explained by a missing listener: %+v", st)
	}
}

// An enabled ordinary feed says why it is not running when the only live
// replicas are listeners that run listening connections only.
func TestOrdinaryFeedWithOnlyListenersIsExplained(t *testing.T) {
	srv, store, rt := newServer(t, "")
	edit := func(id string, mutate func(*registry.Feed)) {
		f := store.feeds[id]
		mutate(&f)
		store.feeds[id] = f
	}
	// intelsky is an ordinary feed; aisstream one pinned to the listener;
	// celestrak stays disabled.
	edit("intelsky", func(f *registry.Feed) { f.Enabled = true })
	edit("aisstream", func(f *registry.Feed) {
		f.Enabled = true
		f.Config = json.RawMessage(`{"pinnedReplica":"r-listen"}`)
	})
	body := `{"name":"Pier AIS","driver":"stream","enabled":true,"config":{"transport":{"kind":"udp","address":":10110"},"decoder":{"name":"nmea-ais"}}}`
	if resp, out := do(t, srv, "POST", "/feeds", body, operator); resp.StatusCode != 201 {
		t.Fatalf("create the listening connection: %d %s", resp.StatusCode, out)
	}
	const pier = "pier-ais"

	// statuses reads one feed's status the three ways the API gives it.
	statuses := func(id string) map[string]registry.Status {
		t.Helper()
		out := map[string]registry.Status{}
		_, raw := do(t, srv, "GET", "/feeds/"+id, "", reqOpts{})
		var dto feedDTO
		if err := json.Unmarshal(raw, &dto); err != nil {
			t.Fatal(err)
		}
		out["get"] = dto.Status
		_, raw = do(t, srv, "GET", "/feeds/"+id+"/status", "", reqOpts{})
		var st registry.Status
		if err := json.Unmarshal(raw, &st); err != nil {
			t.Fatal(err)
		}
		out["status"] = st
		_, raw = do(t, srv, "GET", "/feeds", "", reqOpts{})
		var list []feedDTO
		if err := json.Unmarshal(raw, &list); err != nil {
			t.Fatal(err)
		}
		for _, d := range list {
			if d.ID == id {
				out["list"] = d.Status
			}
		}
		return out
	}
	// expect checks every view of the feed: unassigned with the given error,
	// or, for an empty one, with neither of the two explanations.
	expect := func(when, id, wantErr string) {
		t.Helper()
		for view, st := range statuses(id) {
			if wantErr != "" {
				if st.State != registry.StateUnassigned || st.Health != registry.HealthUnknown || st.LastError != wantErr {
					t.Errorf("%s: %s of %s = %+v, want unassigned with %q", when, view, id, st, wantErr)
				}
			} else if st.LastError == registry.NoOrdinaryReplicaError || st.LastError == registry.NoListenerError {
				t.Errorf("%s: %s of %s is explained although it should not be: %+v", when, view, id, st)
			}
		}
	}
	dedicated := registry.Replica{ID: "r-listen", Listener: true, Host: "ingest.example"}
	shared := registry.Replica{ID: "r-listen", Listener: true, Shared: true, Host: "ingest.example"}
	plain := registry.Replica{ID: "r-plain"}

	store.replicas = []registry.Replica{dedicated}
	expect("only a dedicated listener is live", "intelsky", registry.NoOrdinaryReplicaError)
	for _, id := range []string{pier, "aisstream", "celestrak"} { // listening, pinned and disabled
		expect("only a dedicated listener is live", id, "")
	}

	for name, replicas := range map[string][]registry.Replica{
		"an ordinary replica is live too": {dedicated, plain},
		"the listener is shared":          {shared},
	} {
		store.replicas = replicas
		expect(name, "intelsky", "")
	}

	// Nothing live at all: that is no reason for an ordinary feed, though a
	// listening connection says it has no listener.
	store.replicas = nil
	expect("no replica is live", "intelsky", "")
	expect("no replica is live", pier, registry.NoListenerError)

	// A failed lookup says nothing either way.
	store.replicas, store.replicasErr = []registry.Replica{dedicated}, errors.New("database down")
	expect("the lookup failed", "intelsky", "")
	expect("the lookup failed", pier, "")
	store.replicas, store.replicasErr = []registry.Replica{dedicated}, nil

	// The stored status survives in the other fields.
	edit("intelsky", func(f *registry.Feed) {
		f.Status = &registry.Status{FeedID: "intelsky", Owner: "r-old", MessagesTotal: 42, State: registry.StateRunning, Health: registry.HealthHealthy, Detail: json.RawMessage("{}")}
	})
	if st := statuses("intelsky")["get"]; st.State != registry.StateUnassigned || st.LastError != registry.NoOrdinaryReplicaError || st.MessagesTotal != 42 {
		t.Errorf("stored status overlaid = %+v", st)
	}

	// A feed this replica runs shows its live status.
	rt.snaps = map[string]registry.Status{"intelsky": {State: registry.StateRunning, Health: registry.HealthHealthy, MessagesTotal: 7, Detail: json.RawMessage("{}")}}
	for view, st := range statuses("intelsky") {
		if st.LastError == registry.NoOrdinaryReplicaError || st.State != registry.StateRunning || st.MessagesTotal != 7 {
			t.Errorf("%s of a feed running here = %+v", view, st)
		}
	}
}

// portsServer is a server whose reserved ports are the given set.
func portsServer(t *testing.T, reserved registry.PortSet) (*httptest.Server, *memStore) {
	t.Helper()
	store := newMemStore()
	h := NewHandler(store, drivers.Default(), &fakeRuntime{}, nil)
	h.Reserved = reserved
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, Auth(AuthOptions{}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, store
}

// readPorts fetches GET /feeds/ports and returns the body as it was sent,
// without its trailing newline, and the suggestions in it.
func readPorts(t *testing.T, srv *httptest.Server) (string, map[string]int) {
	t.Helper()
	resp, body := do(t, srv, "GET", "/feeds/ports", "", reqOpts{})
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("GET /feeds/ports: %d %s: %s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	var out struct {
		Suggested map[string]int `json:"suggested"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	return strings.TrimSpace(string(body)), out.Suggested
}

func mustCreate(t *testing.T, srv *httptest.Server, body string) {
	t.Helper()
	if resp, out := do(t, srv, "POST", "/feeds", body, operator); resp.StatusCode != 201 {
		t.Fatalf("create %s: %d %s", body, resp.StatusCode, out)
	}
}

// GET /feeds/ports lists the ports connections hold and the ports Sentinel
// keeps, and a free port for each protocol. The shape is the contract the
// Connections form reads, so it is compared as text.
func TestPortsEndpoint(t *testing.T) {
	reserved, err := registry.ParsePortSet("6000-6001,7000")
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := portsServer(t, reserved)

	// Created out of port order; the response lists them by port, then protocol.
	mustCreate(t, srv, `{"name":"Pier AIS","driver":"stream","enabled":true,"config":{"transport":{"kind":"udp","address":":10110"},"decoder":{"name":"nmea-ais"}}}`)
	mustCreate(t, srv, `{"name":"Gate","driver":"stream","enabled":true,"config":{"transport":{"kind":"tcpserver","address":":6003"},"framing":"line","decoder":{"name":"nmea-ais"}}}`)
	mustCreate(t, srv, `{"name":"Dock","driver":"udp","config":{"port":6002}}`)
	mustCreate(t, srv, `{"name":"Site TCP","driver":"tcp","config":{"mode":"listen","port":6002}}`)
	// Connections that do not listen hold no port.
	mustCreate(t, srv, `{"name":"Relay","driver":"tcp","enabled":true,"config":{"mode":"connect","host":"relay.example","port":6004}}`)
	mustCreate(t, srv, `{"name":"AIS client","driver":"stream","enabled":true,"config":{"transport":{"kind":"tcpclient","address":"ais.example:4001"},"decoder":{"name":"nmea-ais"}}}`)

	// A disabled connection keeps its port, so it is listed (enabled false)
	// and its port is not suggested. 6002 is free for neither protocol
	// (taken by Site TCP and Dock), 6003 is taken for tcp only.
	const want = `{"min":1024,"max":65535,` +
		`"reserved":[{"from":6000,"to":6001},{"from":7000,"to":7000}],` +
		`"taken":[` +
		`{"protocol":"tcp","port":6002,"feedId":"site-tcp","feedName":"Site TCP","enabled":false},` +
		`{"protocol":"udp","port":6002,"feedId":"dock","feedName":"Dock","enabled":false},` +
		`{"protocol":"tcp","port":6003,"feedId":"gate","feedName":"Gate","enabled":true},` +
		`{"protocol":"udp","port":10110,"feedId":"pier-ais","feedName":"Pier AIS","enabled":true}],` +
		`"suggested":{"tcp":6004,"udp":6003}}`
	for i := 0; i < 5; i++ { // the store lists feeds in map order
		if body, _ := readPorts(t, srv); body != want {
			t.Fatalf("GET /feeds/ports:\n got %s\nwant %s", body, want)
		}
	}

	// What it suggests is what a save accepts, and what it lists is refused.
	_, suggested := readPorts(t, srv)
	if resp, out := do(t, srv, "POST", "/feeds", `{"name":"Suggested","driver":"udp","config":{"port":`+strconv.Itoa(suggested["udp"])+`}}`, operator); resp.StatusCode != 201 {
		t.Fatalf("the suggested udp port %d: %d %s", suggested["udp"], resp.StatusCode, out)
	}
	if resp, out := do(t, srv, "POST", "/feeds", `{"name":"Again","driver":"tcp","config":{"mode":"listen","port":6003}}`, operator); resp.StatusCode != 422 || !strings.Contains(string(out), "used by connection gate") {
		t.Fatalf("a tcp port that is listed as taken: %d %s", resp.StatusCode, out)
	}
	if resp, out := do(t, srv, "POST", "/feeds", `{"name":"Reserved","driver":"udp","config":{"port":7000}}`, operator); resp.StatusCode != 422 || !strings.Contains(string(out), "Sentinel") {
		t.Fatalf("a reserved port: %d %s", resp.StatusCode, out)
	}
	_, suggested = readPorts(t, srv)
	if suggested["udp"] != 6004 || suggested["tcp"] != 6004 {
		t.Errorf("suggestions after taking udp 6003 = %v, want both 6004", suggested)
	}
}

func TestPortsEndpointWithNothingConfigured(t *testing.T) {
	srv, _ := portsServer(t, nil)
	body, _ := readPorts(t, srv)
	// The built-in feeds are all pollers. Lists are [], never null.
	if want := `{"min":1024,"max":65535,"reserved":[],"taken":[],"suggested":{"tcp":6000,"udp":6000}}`; body != want {
		t.Errorf("GET /feeds/ports:\n got %s\nwant %s", body, want)
	}
}

// A protocol with no free port is left out of the suggestions.
func TestPortsEndpointOmitsAProtocolWithNoFreePort(t *testing.T) {
	everythingBut40000 := registry.PortSet{}
	for p := 1024; p <= 65535; p++ {
		if p != 40000 {
			everythingBut40000[p] = true
		}
	}
	srv, _ := portsServer(t, everythingBut40000)
	mustCreate(t, srv, `{"name":"Last tcp","driver":"tcp","config":{"mode":"listen","port":40000}}`)
	const reserved = `"reserved":[{"from":1024,"to":39999},{"from":40001,"to":65535}]`
	const lastTCP = `{"protocol":"tcp","port":40000,"feedId":"last-tcp","feedName":"Last tcp","enabled":false}`
	body, _ := readPorts(t, srv)
	if want := `{"min":1024,"max":65535,` + reserved + `,"taken":[` + lastTCP + `],"suggested":{"udp":40000}}`; body != want {
		t.Errorf("tcp full:\n got %s\nwant %s", body, want)
	}
	mustCreate(t, srv, `{"name":"Last udp","driver":"udp","config":{"port":40000}}`)
	body, _ = readPorts(t, srv)
	if want := `{"min":1024,"max":65535,` + reserved + `,"taken":[` + lastTCP + `,{"protocol":"udp","port":40000,"feedId":"last-udp","feedName":"Last udp","enabled":false}],"suggested":{}}`; body != want {
		t.Errorf("both full:\n got %s\nwant %s", body, want)
	}
}

// The literal route wins over GET /feeds/{id}, and no feed can take its name.
func TestPortsRouteAndTheReservedID(t *testing.T) {
	srv, store, _ := newServer(t, "secret")
	token := reqOpts{token: "secret"}
	admin := reqOpts{token: "secret", roles: "sentinel-admin", user: "u1"}

	resp, out := do(t, srv, "GET", "/feeds/ports", "", token)
	if resp.StatusCode != 200 || !strings.HasPrefix(string(out), `{"min":`) {
		t.Fatalf("GET /feeds/ports: %d %s", resp.StatusCode, out)
	}
	// It sits behind the same guard as the rest.
	if resp, _ := do(t, srv, "GET", "/feeds/ports", "", reqOpts{}); resp.StatusCode != 401 {
		t.Errorf("without the service token: %d", resp.StatusCode)
	}

	// The name "Ports" makes the id ports, and an explicit id is refused the same way.
	for _, body := range []string{
		`{"name":"Ports","driver":"opensky"}`,
		`{"id":"ports","name":"Anything","driver":"opensky"}`,
	} {
		resp, out := do(t, srv, "POST", "/feeds", body, admin)
		var e errorResponse
		_ = json.Unmarshal(out, &e)
		if resp.StatusCode != 422 || len(e.Details) != 1 || e.Details[0].Path != "/id" || e.Details[0].Message != "is reserved" {
			t.Errorf("create %s: %d %s", body, resp.StatusCode, out)
		}
	}
	if _, ok := store.feeds["ports"]; ok {
		t.Error("a feed named ports was stored")
	}
	// Only GET has the literal route; there is no such feed for the others.
	for _, method := range []string{"PUT", "PATCH", "DELETE"} {
		if resp, _ := do(t, srv, method, "/feeds/ports", `{"name":"x","config":{},"enabled":false}`, admin); resp.StatusCode != 404 {
			t.Errorf("%s /feeds/ports: %d", method, resp.StatusCode)
		}
	}
	// Ids that only contain the word are fine.
	if resp, out := do(t, srv, "POST", "/feeds", `{"name":"Ports 2","driver":"opensky"}`, admin); resp.StatusCode != 201 {
		t.Errorf("create ports-2: %d %s", resp.StatusCode, out)
	}
}

func TestPortsEndpointStoreFailure(t *testing.T) {
	srv, store, _ := newServer(t, "")
	store.listErr = errors.New("database down")
	resp, out := do(t, srv, "GET", "/feeds/ports", "", reqOpts{})
	if resp.StatusCode != 500 || strings.Contains(string(out), "database down") {
		t.Errorf("a failed listing: %d %s", resp.StatusCode, out)
	}
}

// UseSecretPolicy holds the secret references of a connection to the policy on
// create and on update: a variable or file it does not allow is a 422 at the
// reference, naming it and the settings that decide.
func TestSecretPolicyIsEnforcedOnSave(t *testing.T) {
	store := newMemStore()
	h := NewHandler(store, drivers.Default(), &fakeRuntime{}, nil)
	h.UseSecretPolicy(registry.SecretPolicy{EnvAllow: []string{"OPENSKY_*", "SENTINEL_SECRET_*"}, FileDirs: []string{"/etc/sentinel"}})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, Auth(AuthOptions{}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	create := func(name, refs string) (*http.Response, []byte) {
		return do(t, srv, "POST", "/feeds", `{"name":"`+name+`","driver":"opensky","config":{"intervalSec":10},"secretRefs":`+refs+`}`, operator)
	}
	if resp, out := create("Allowed", `{"clientId":"env:OPENSKY_CLIENT_ID","clientSecret":"file:/etc/sentinel/secrets/os.key"}`); resp.StatusCode != 201 {
		t.Fatalf("allowed refs: %d %s", resp.StatusCode, out)
	}
	resp, out := create("Denied", `{"clientId":"env:DATABASE_URL","clientSecret":"file:/etc/sentinel/../shadow"}`)
	if resp.StatusCode != 422 {
		t.Fatalf("denied refs: %d %s", resp.StatusCode, out)
	}
	for _, want := range []string{"/secretRefs/clientId", "DATABASE_URL", "/secretRefs/clientSecret", "INGEST_SECRET_ENV_ALLOW", "INGEST_SECRET_FILE_DIRS", "SENTINEL_SECRET_"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("422 body lacks %q: %s", want, out)
		}
	}
	// An update is held to it as well.
	patch := `{"secretRefs":{"clientId":"env:INGEST_SERVICE_TOKEN"}}`
	if resp, out := do(t, srv, "PATCH", "/feeds/allowed", patch, operator); resp.StatusCode != 422 || !strings.Contains(string(out), "/secretRefs/clientId") {
		t.Errorf("patch to a denied ref: %d %s", resp.StatusCode, out)
	}
	if resp, out := do(t, srv, "PATCH", "/feeds/allowed", `{"secretRefs":{"clientId":"env:SENTINEL_SECRET_MINE"}}`, operator); resp.StatusCode != 200 {
		t.Errorf("patch to an allowed ref: %d %s", resp.StatusCode, out)
	}
	// A stream's TLS files are held to the folders.
	tls := `{"name":"TLS","driver":"stream","config":{"transport":{"kind":"tcpserver","address":":47100","tls":{"enabled":true,"certFile":"/etc/passwd","keyFile":"/etc/sentinel/tak/k.pem"}},"framing":"line","decoder":{"name":"cot"}}}`
	resp, out = do(t, srv, "POST", "/feeds", tls, operator)
	if resp.StatusCode != 422 || !strings.Contains(string(out), "/config/transport/tls/certFile") || strings.Contains(string(out), "tls/keyFile") {
		t.Errorf("tls files: %d %s", resp.StatusCode, out)
	}
}

// No internal driver ships any more (the emplacements mirror was the only
// one), so the refusal is checked against a catalog that has one.
func TestCreateRefusesInternalDrivers(t *testing.T) {
	internal := driver.Definition{ID: "test-internal", Name: "Test internal", Internal: true,
		New: func(driver.Spec, driver.Deps) (driver.Driver, error) { return nil, nil }}
	catalog, err := drivers.Build(append(driver.All(), internal))
	if err != nil {
		t.Fatal(err)
	}
	store := newMemStore()
	h := NewHandler(store, catalog, &fakeRuntime{live: map[string]bool{}}, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, Auth(AuthOptions{}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	resp, out := do(t, srv, "POST", "/feeds", `{"name":"Second mirror","driver":"test-internal","config":{}}`, operator)
	if resp.StatusCode != 422 {
		t.Fatalf("status %d: %s", resp.StatusCode, out)
	}
	var e errorResponse
	_ = json.Unmarshal(out, &e)
	// "internal" tells this refusal from the one for an unknown driver, which
	// is also a 422 at /driver.
	if len(e.Details) == 0 || e.Details[0].Path != "/driver" || !strings.Contains(e.Details[0].Message, "internal") {
		t.Fatalf("details = %+v", e.Details)
	}
	if feeds, _ := store.List(context.Background()); len(feeds) != len(registry.Builtins) {
		t.Fatalf("a feed was stored: %d feeds, want the %d builtins", len(feeds), len(registry.Builtins))
	}
}
