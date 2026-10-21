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

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/sentinel/ingest-service/internal/drivers"
	"github.com/sentinel/ingest-service/internal/ingesttoken"
	"github.com/sentinel/ingest-service/internal/registry"
)

type memTokens struct {
	mu   sync.Mutex
	rows map[string]ingesttoken.Token
	n    int
	// createErr, when set, is what Create returns.
	createErr error
}

func (m *memTokens) Create(_ context.Context, t ingesttoken.Token, hash []byte) (ingesttoken.Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return ingesttoken.Token{}, m.createErr
	}
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
	return newLoggedTokenServer(t, nil, tokens, feeds...)
}

// newLoggedTokenServer is newTokenServer with the handler logging to logger.
func newLoggedTokenServer(t *testing.T, logger *zap.Logger, tokens TokenStore, feeds ...registry.Feed) *httptest.Server {
	t.Helper()
	store := newMemStore()
	for _, f := range feeds {
		store.feeds[f.ID] = f
	}
	h := NewHandler(store, drivers.Default(), &fakeRuntime{live: map[string]bool{}}, logger)
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
	if err := json.Unmarshal(out, &created); err != nil {
		t.Fatalf("create body: %v: %s", err, out)
	}
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

func TestTokenRoutesHoldTheCallerToTheFeed(t *testing.T) {
	tokens := &memTokens{rows: map[string]ingesttoken.Token{}}
	srv := newTokenServer(t, tokens, grpcFeed("ew-1", "CONFIDENTIAL"), grpcFeed("ew-2", "CONFIDENTIAL"))
	secretOp := reqOpts{roles: "sentinel-operator", user: "u1", class: "SECRET"}

	resp, out := do(t, srv, "POST", "/feeds/ew-1/tokens", `{"name":"x"}`, secretOp)
	var created ingesttoken.Token
	if err := json.Unmarshal(out, &created); err != nil || resp.StatusCode != 201 {
		t.Fatalf("create: %d %s", resp.StatusCode, out)
	}

	// Another feed's token is not found through this feed's path, and stays usable.
	if resp, _ := do(t, srv, "DELETE", "/feeds/ew-2/tokens/"+created.ID, "", secretOp); resp.StatusCode != 404 {
		t.Errorf("revoking through another feed: %d", resp.StatusCode)
	}
	if tokens.rows[created.ID].RevokedAt != nil {
		t.Error("a token was revoked through another feed's path")
	}

	notCleared := reqOpts{roles: "sentinel-operator", user: "u1", class: "UNCLASSIFIED"}
	if resp, _ := do(t, srv, "GET", "/feeds/ew-1/tokens", "", notCleared); resp.StatusCode != 403 {
		t.Errorf("list without clearance: %d", resp.StatusCode)
	}
	if resp, _ := do(t, srv, "DELETE", "/feeds/ew-1/tokens/"+created.ID, "", notCleared); resp.StatusCode != 403 {
		t.Errorf("revoke without clearance: %d", resp.StatusCode)
	}
	viewer := reqOpts{roles: "sentinel-viewer", user: "u1", class: "SECRET"}
	if resp, _ := do(t, srv, "DELETE", "/feeds/ew-1/tokens/"+created.ID, "", viewer); resp.StatusCode != 403 {
		t.Errorf("a viewer revoked a token: %d", resp.StatusCode)
	}
	if tokens.rows[created.ID].RevokedAt != nil {
		t.Error("a refused revoke still revoked the token")
	}
}

func TestTokenDefaultsAndBounds(t *testing.T) {
	tokens := &memTokens{rows: map[string]ingesttoken.Token{}}
	plain := grpcFeed("plain", "")
	plain.Config = json.RawMessage(`{"port":6100}`)
	srv := newTokenServer(t, tokens, plain, grpcFeed("ew-1", "UNCLASSIFIED"))
	op := reqOpts{roles: "sentinel-operator", user: "u1", class: "SECRET"}

	resp, out := do(t, srv, "POST", "/feeds/plain/tokens", `{"name":"x"}`, op)
	var got ingesttoken.Token
	if err := json.Unmarshal(out, &got); err != nil || resp.StatusCode != 201 || got.Classification != "UNCLASSIFIED" {
		t.Errorf("feed without a classification: %d %s", resp.StatusCode, out)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	body := func(name string) string {
		b, _ := json.Marshal(map[string]string{"name": name})
		return string(b)
	}
	cases := []struct {
		name, body string
		want       int
	}{
		{"expiresAt is now", `{"name":"x","expiresAt":"` + now + `"}`, 422},
		{"254 bytes of multibyte", body(strings.Repeat("é", 127)), 201},
		{"256 bytes of multibyte", body(strings.Repeat("é", 128)), 422},
		{"255 bytes", body(strings.Repeat("a", 255)), 201},
		{"a NUL in the name", `{"name":"a\u0000b"}`, 422},
		{"a newline in the name", `{"name":"a\nb"}`, 422},
	}
	for _, c := range cases {
		if resp, out := do(t, srv, "POST", "/feeds/ew-1/tokens", c.body, op); resp.StatusCode != c.want {
			t.Errorf("%s: %d %.200s", c.name, resp.StatusCode, out)
		}
	}
}

// A feed deleted between the lookup and the insert fails the foreign key, which
// the store reports as ErrNotFound: the caller is told about the feed.
func TestTokenCreateOnAFeedDeletedMidRequest(t *testing.T) {
	tokens := &memTokens{rows: map[string]ingesttoken.Token{}, createErr: ingesttoken.ErrNotFound}
	srv := newTokenServer(t, tokens, grpcFeed("ew-1", "UNCLASSIFIED"))
	resp, out := do(t, srv, "POST", "/feeds/ew-1/tokens", `{"name":"x"}`, reqOpts{roles: "sentinel-operator", user: "u1", class: "SECRET"})
	if resp.StatusCode != 404 || !strings.Contains(string(out), "feed not found") {
		t.Fatalf("got %d %s", resp.StatusCode, out)
	}
}

// The secret is shown once, in the create response, and nowhere else: the
// handler's logs name the token by id and prefix only.
func TestTokenSecretNeverReachesTheLogs(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	tokens := &memTokens{rows: map[string]ingesttoken.Token{}}
	srv := newLoggedTokenServer(t, zap.New(core), tokens, grpcFeed("ew-1", "CONFIDENTIAL"))
	cleared := reqOpts{roles: "sentinel-operator", user: "u1", class: "SECRET"}

	resp, out := do(t, srv, "POST", "/feeds/ew-1/tokens", `{"name":"sensor one"}`, cleared)
	var created struct {
		ingesttoken.Token
		Secret string `json:"token"`
	}
	if err := json.Unmarshal(out, &created); err != nil || resp.StatusCode != 201 || created.Secret == "" {
		t.Fatalf("create: %d %s", resp.StatusCode, out)
	}
	if resp, _ := do(t, srv, "DELETE", "/feeds/ew-1/tokens/"+created.ID, "", cleared); resp.StatusCode != 204 {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}

	entries := logs.All()
	if len(entries) < 2 {
		t.Fatalf("expected the create and revoke to be logged, got %d entries", len(entries))
	}
	for _, e := range entries {
		fields, _ := json.Marshal(e.ContextMap())
		if strings.Contains(e.Message, created.Secret) || strings.Contains(string(fields), created.Secret) ||
			strings.Contains(e.Message, created.Secret[3:]) || strings.Contains(string(fields), created.Secret[3:]) {
			t.Errorf("the token secret reached the log: %q %s", e.Message, fields)
		}
	}
}
