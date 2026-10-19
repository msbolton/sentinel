package mgmt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/drivers"
	"github.com/sentinel/ingest-service/internal/ingesttoken"
	"github.com/sentinel/ingest-service/internal/registry"
)

type memTokens struct {
	mu   sync.Mutex
	rows map[string]ingesttoken.Token
	n    int
}

func (m *memTokens) Create(_ context.Context, t ingesttoken.Token, hash []byte) (ingesttoken.Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(hash) != 32 {
		panic("hash must be a SHA-256")
	}
	m.n++
	t.ID, t.CreatedAt = "00000000-0000-0000-0000-00000000000"+string(rune('0'+m.n)), time.Now()
	m.rows[t.ID] = t
	return t, nil
}
func (m *memTokens) List(_ context.Context, feed string) ([]ingesttoken.Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ingesttoken.Token{}
	for _, t := range m.rows {
		if t.FeedID == feed {
			out = append(out, t)
		}
	}
	return out, nil
}
func (m *memTokens) Revoke(_ context.Context, feed, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.rows[id]
	if !ok || t.FeedID != feed {
		return ingesttoken.ErrNotFound
	}
	if t.RevokedAt == nil {
		t.RevokedAt = &at
	}
	m.rows[id] = t
	return nil
}

func grpcFeed(id, level string) registry.Feed {
	return registry.Feed{ID: id, Name: id, Driver: "grpc", Enabled: true, Version: 1,
		Config: json.RawMessage(`{"port":6100,"classification":"` + level + `"}`), SecretRefs: map[string]string{}}
}

// newTokenServer is newServer with the given feeds in the store and, when
// tokens is non-nil, a token store on the handler. The grpc driver is not
// registered yet, so its feeds go straight into the store.
func newTokenServer(t *testing.T, tokens TokenStore, feeds ...registry.Feed) *httptest.Server {
	t.Helper()
	store := newMemStore()
	for _, f := range feeds {
		store.feeds[f.ID] = f
	}
	h := NewHandler(store, drivers.Default(), &fakeRuntime{live: map[string]bool{}}, nil)
	if tokens != nil { // a typed nil must not reach the interface
		h.Tokens = tokens
	}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, Auth(AuthOptions{}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestTokenLifecycle(t *testing.T) {
	tokens := &memTokens{rows: map[string]ingesttoken.Token{}}
	srv := newTokenServer(t, tokens, grpcFeed("ew-1", "CONFIDENTIAL"))
	cleared := reqOpts{roles: "sentinel-operator", user: "u1", class: "SECRET"}

	resp, out := do(t, srv, "POST", "/feeds/ew-1/tokens", `{"name":"sensor one"}`, cleared)
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %s", resp.StatusCode, out)
	}
	var created struct {
		ingesttoken.Token
		Secret string `json:"token"`
	}
	_ = json.Unmarshal(out, &created)
	if !strings.HasPrefix(created.Secret, "si_") || len(created.Secret) != 46 || created.Prefix != created.Secret[:11] ||
		created.Classification != "CONFIDENTIAL" || created.CreatedBy != "u1" || created.FeedID != "ew-1" {
		t.Fatalf("created = %+v", created)
	}

	// Review focus 5: the list never carries the secret.
	resp, out = do(t, srv, "GET", "/feeds/ew-1/tokens", "", cleared)
	if resp.StatusCode != 200 || strings.Contains(string(out), created.Secret) || strings.Contains(string(out), `"token"`) {
		t.Fatalf("list: %d %s", resp.StatusCode, out)
	}

	resp, _ = do(t, srv, "DELETE", "/feeds/ew-1/tokens/"+created.ID, "", cleared)
	if resp.StatusCode != 204 {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	if resp, _ := do(t, srv, "DELETE", "/feeds/ew-1/tokens/"+created.ID, "", cleared); resp.StatusCode != 204 {
		t.Fatalf("revoking again: %d", resp.StatusCode)
	}
	if resp, _ := do(t, srv, "DELETE", "/feeds/ew-1/tokens/00000000-0000-0000-0000-000000000009", "", cleared); resp.StatusCode != 404 {
		t.Fatalf("unknown token: %d", resp.StatusCode)
	}
}

// Review focus 3.
func TestTokenCreateRules(t *testing.T) {
	tokens := &memTokens{rows: map[string]ingesttoken.Token{}}
	srv := newTokenServer(t, tokens, grpcFeed("ew-1", "CONFIDENTIAL"),
		registry.Feed{ID: "tcp-1", Name: "tcp-1", Driver: "tcp", Enabled: true, Version: 1, Config: json.RawMessage(`{"mode":"listen","port":6200}`), SecretRefs: map[string]string{}})
	secretOp := reqOpts{roles: "sentinel-operator", user: "u1", class: "SECRET"}

	cases := []struct {
		name, path, body string
		as               reqOpts
		want             int
	}{
		{"below the feed", "/feeds/ew-1/tokens", `{"name":"x","classification":"UNCLASSIFIED"}`, secretOp, 422},
		{"above clearance", "/feeds/ew-1/tokens", `{"name":"x","classification":"TOP_SECRET"}`, secretOp, 422},
		{"not a level", "/feeds/ew-1/tokens", `{"name":"x","classification":"RESTRICTED"}`, secretOp, 422},
		{"no name", "/feeds/ew-1/tokens", `{"name":"  "}`, secretOp, 422},
		{"expired", "/feeds/ew-1/tokens", `{"name":"x","expiresAt":"2020-01-01T00:00:00Z"}`, secretOp, 422},
		{"not grpc", "/feeds/tcp-1/tokens", `{"name":"x"}`, secretOp, 422},
		{"unknown feed", "/feeds/nope/tokens", `{"name":"x"}`, secretOp, 404},
		{"not cleared for the feed", "/feeds/ew-1/tokens", `{"name":"x"}`, reqOpts{roles: "sentinel-operator", user: "u1", class: "UNCLASSIFIED"}, 403},
		{"viewer", "/feeds/ew-1/tokens", `{"name":"x"}`, reqOpts{roles: "sentinel-viewer", user: "u1", class: "SECRET"}, 403},
		{"ok at SECRET", "/feeds/ew-1/tokens", `{"name":"x","classification":"SECRET"}`, secretOp, 201},
	}
	for _, c := range cases {
		if resp, out := do(t, srv, "POST", c.path, c.body, c.as); resp.StatusCode != c.want {
			t.Errorf("%s: %d %s", c.name, resp.StatusCode, out)
		}
	}
	if resp, _ := do(t, srv, "GET", "/feeds/ew-1/tokens", "", reqOpts{roles: "sentinel-viewer", user: "u1", class: "SECRET"}); resp.StatusCode != 403 {
		t.Errorf("a viewer listed tokens: %d", resp.StatusCode)
	}
}

func TestTokensDisabledWithoutAStore(t *testing.T) {
	srv := newTokenServer(t, nil, grpcFeed("ew-1", "UNCLASSIFIED"))
	if resp, _ := do(t, srv, "GET", "/feeds/ew-1/tokens", "", reqOpts{roles: "sentinel-operator", user: "u1", class: "SECRET"}); resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("no store: %d", resp.StatusCode)
	}
}
