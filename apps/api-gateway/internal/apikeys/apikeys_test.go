package apikeys

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/sentinel/api-gateway/internal/auth"
)

type memStore struct {
	keys    map[string]Key
	hashes  map[string]string // hex(hash) -> id
	revoked map[string]time.Time
	seq     int
}

func newMemStore() *memStore {
	return &memStore{keys: map[string]Key{}, hashes: map[string]string{}, revoked: map[string]time.Time{}}
}

func (m *memStore) Create(_ context.Context, k Key, hash []byte) (Key, error) {
	m.seq++
	k.ID = "k" + string(rune('0'+m.seq))
	k.CreatedAt = time.Now()
	m.keys[k.ID] = k
	m.hashes[string(hash)] = k.ID
	return k, nil
}

func (m *memStore) ListByUser(_ context.Context, userID string) ([]Key, error) {
	var out []Key
	for _, k := range m.keys {
		if _, gone := m.revoked[k.ID]; k.UserID == userID && !gone {
			out = append(out, k)
		}
	}
	return out, nil
}

func (m *memStore) Revoke(_ context.Context, id, owner string) error {
	k, ok := m.keys[id]
	if _, gone := m.revoked[id]; !ok || gone || (owner != "" && k.UserID != owner) {
		return ErrInvalidKey
	}
	m.revoked[id] = time.Now()
	return nil
}

func (m *memStore) Lookup(_ context.Context, secret string, now time.Time) (Key, error) {
	id, ok := m.hashes[string(Hash(secret))]
	if !ok || !Valid(secret) {
		return Key{}, ErrInvalidKey
	}
	k := m.keys[id]
	var revoked *time.Time
	if t, gone := m.revoked[id]; gone {
		revoked = &t
	}
	if !k.Usable(now, revoked) {
		return Key{}, ErrInvalidKey
	}
	return k, nil
}

func TestGenerateAndShape(t *testing.T) {
	secret, prefix, hash, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !Valid(secret) || !strings.HasPrefix(secret, "sk_") || len(hash) != 32 || prefix != secret[:11] {
		t.Fatalf("secret=%q prefix=%q hash=%d", secret, prefix, len(hash))
	}
	if Valid("sk_short") || Valid(strings.Repeat("x", 46)) {
		t.Error("bad shapes accepted")
	}
	k := Key{UserID: "u1", Username: "ann", Classification: "SECRET"}
	p := k.Principal()
	if !p.ViaAPIKey || p.UserID != "u1" || len(p.Roles) != 1 || p.Roles[0] != auth.RoleViewer || p.ClassificationLevel != "SECRET" {
		t.Errorf("principal = %+v", p)
	}
}

func do(t *testing.T, h *Handler, u *auth.User, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if u != nil {
		req = req.WithContext(auth.WithUser(req.Context(), u))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHandlerLifecycle(t *testing.T) {
	store := newMemStore()
	h := NewHandler(store, zap.NewNop())
	ann := &auth.User{UserID: "u1", Username: "ann", Roles: []string{auth.RoleAnalyst}, ClassificationLevel: "SECRET"}

	rec := do(t, h, ann, http.MethodPost, "/api/v1/auth/api-keys", `{"name":"QGIS","classification":"TOP_SECRET","expiresInDays":30}`)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created Created
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Secret == "" || created.Classification != "SECRET" || created.ExpiresAt == nil || created.Roles[0] != auth.RoleViewer || created.Prefix != created.Secret[:11] {
		t.Fatalf("created = %+v", created)
	}

	// The secret resolves to a viewer principal capped at the issuer's clearance.
	res := NewResolver(store)
	p, err := res.ResolveAPIKey(context.Background(), created.Secret)
	if err != nil || p.UserID != "u1" || p.ClassificationLevel != "SECRET" || !p.ViaAPIKey {
		t.Fatalf("resolve = %+v %v", p, err)
	}
	if _, err := res.ResolveAPIKey(context.Background(), "sk_"+strings.Repeat("A", 43)); err == nil {
		t.Error("unknown key resolved")
	}
	// Expired keys stop working.
	res.now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	if _, err := res.ResolveAPIKey(context.Background(), created.Secret); err == nil {
		t.Error("expired key resolved")
	}
	res.now = time.Now

	// A principal that is itself a key may not list or make keys.
	if rec := do(t, h, p, http.MethodGet, "/api/v1/auth/api-keys", ""); rec.Code != 403 {
		t.Errorf("key listing keys: %d", rec.Code)
	}
	if rec := do(t, h, p, http.MethodPost, "/api/v1/auth/api-keys", `{"name":"x"}`); rec.Code != 403 {
		t.Errorf("key making keys: %d", rec.Code)
	}

	rec = do(t, h, ann, http.MethodGet, "/api/v1/auth/api-keys", "")
	var list []Key
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != 200 || len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}

	bob := &auth.User{UserID: "u2", Username: "bob", Roles: []string{auth.RoleViewer}, ClassificationLevel: "UNCLASSIFIED"}
	if rec := do(t, h, bob, http.MethodDelete, "/api/v1/auth/api-keys/"+created.ID, ""); rec.Code != 404 {
		t.Errorf("revoking someone else's key: %d", rec.Code)
	}
	if rec := do(t, h, ann, http.MethodDelete, "/api/v1/auth/api-keys/"+created.ID, ""); rec.Code != 204 {
		t.Errorf("revoke: %d", rec.Code)
	}
	if _, err := res.ResolveAPIKey(context.Background(), created.Secret); err == nil {
		t.Error("revoked key resolved")
	}
	if rec := do(t, h, ann, http.MethodDelete, "/api/v1/auth/api-keys/"+created.ID, ""); rec.Code != 404 {
		t.Errorf("revoke twice: %d", rec.Code)
	}

	for _, body := range []string{`{}`, `{"name":"x","classification":"ULTRA"}`, `{"name":"x","expiresInDays":-1}`, `not json`} {
		if rec := do(t, h, ann, http.MethodPost, "/api/v1/auth/api-keys", body); rec.Code != 400 {
			t.Errorf("%s: %d", body, rec.Code)
		}
	}
	if rec := do(t, h, nil, http.MethodGet, "/api/v1/auth/api-keys", ""); rec.Code != 401 {
		t.Errorf("no user: %d", rec.Code)
	}
}

// Needs a database with the schema applied: TEST_DATABASE_URL.
func TestPGStore(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPGStore(pool)
	secret, prefix, hash, _ := Generate()
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Millisecond)
	k, err := store.Create(ctx, Key{UserID: "test-user", Username: "test", Name: "t", Prefix: prefix, Roles: []string{auth.RoleViewer}, Classification: "CONFIDENTIAL", ExpiresAt: &exp}, hash)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM sentinel.api_keys WHERE id::text = $1`, k.ID) })
	got, err := store.Lookup(ctx, secret, time.Now())
	if err != nil || got.ID != k.ID || got.Classification != "CONFIDENTIAL" {
		t.Fatalf("lookup = %+v %v", got, err)
	}
	if _, err := store.Lookup(ctx, secret, exp.Add(time.Second)); err == nil {
		t.Error("expired key found")
	}
	list, err := store.ListByUser(ctx, "test-user")
	if err != nil || len(list) == 0 || list[0].LastUsedAt == nil {
		t.Fatalf("list = %+v %v (lastUsedAt must be stamped)", list, err)
	}
	if err := store.Revoke(ctx, k.ID, "someone-else"); err == nil {
		t.Error("revoked another user's key")
	}
	if err := store.Revoke(ctx, k.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(ctx, secret, time.Now()); err == nil {
		t.Error("revoked key found")
	}
}
