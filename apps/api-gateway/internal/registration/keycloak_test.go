package registration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// fakeKeycloak simulates the subset of the Admin REST API the client uses.
type fakeKeycloak struct {
	mu          sync.Mutex
	tokenCalls  int
	users       map[string]*keycloakUser
	userRoles   map[string][]keycloakRole
	calls       []string
	putBodies   []map[string]interface{} // every PUT /users/{id} body, in order
	failRoleMap bool
	conflict    bool

	guestBodies   []map[string]interface{} // every guest POST /users body
	guestSeq      int
	passwordForms []url.Values // every password-grant form
}

var realmRoles = []keycloakRole{
	{ID: "r-viewer", Name: "sentinel-viewer"}, {ID: "r-admin", Name: "sentinel-admin"},
	{ID: "r-guest", Name: "sentinel-guest"},
	{ID: "r-u", Name: "classification-u"}, {ID: "r-s", Name: "classification-s"}, {ID: "r-ts", Name: "classification-ts"},
}

func newFakeKeycloak() *fakeKeycloak {
	return &fakeKeycloak{
		users: map[string]*keycloakUser{
			"u-pending": {ID: "u-pending", Username: "newbie", Email: "n@x.io", FirstName: "New", LastName: "Bie", Enabled: false,
				Attributes: map[string][]string{"organization": {"ACME"}, "justification": {"need it"}, "registrationDate": {"2026-10-01T00:00:00Z"}}},
			"u-disabled-old": {ID: "u-disabled-old", Username: "old", Email: "o@x.io", Enabled: false},
			"u-active":       {ID: "u-active", Username: "alice", Email: "a@x.io", FirstName: "Alice", Enabled: true},
			"u-svc":          {ID: "u-svc", Username: "service-account-sentinel-service", Enabled: true},
		},
		userRoles: map[string][]keycloakRole{
			"u-active": {{ID: "r-admin", Name: "sentinel-admin"}, {ID: "r-s", Name: "classification-s"}},
		},
	}
}

func (f *fakeKeycloak) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, v interface{}) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }
	record := func(r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
	}
	mux.HandleFunc("POST /realms/sentinel/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") == "password" {
			f.mu.Lock()
			f.passwordForms = append(f.passwordForms, r.Form)
			f.mu.Unlock()
			if r.Form.Get("password") != "guest-pass" {
				w.WriteHeader(401)
				writeJSON(w, map[string]string{"error": "invalid_grant"})
				return
			}
			writeJSON(w, map[string]interface{}{"access_token": "guest-at", "refresh_token": "guest-rt", "expires_in": 300})
			return
		}
		if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_id") != "sentinel-service" || r.Form.Get("client_secret") != "secret" {
			http.Error(w, "bad grant", 401)
			return
		}
		f.mu.Lock()
		f.tokenCalls++
		f.mu.Unlock()
		writeJSON(w, map[string]interface{}{"access_token": "tok", "expires_in": 300})
	})
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tok" {
				http.Error(w, "unauthorized", 401)
				return
			}
			record(r)
			next(w, r)
		}
	}
	mux.HandleFunc("POST /admin/realms/sentinel/users", auth(func(w http.ResponseWriter, r *http.Request) {
		var u keycloakUser
		_ = json.NewDecoder(r.Body).Decode(&u)
		if f.conflict {
			w.WriteHeader(409)
			writeJSON(w, map[string]string{"errorMessage": "User exists with same username"})
			return
		}
		if len(u.Attributes["sentinelGuestCreatedAt"]) == 1 {
			var raw map[string]interface{}
			b, _ := json.Marshal(u)
			_ = json.Unmarshal(b, &raw)
			f.mu.Lock()
			f.guestSeq++
			u.ID = "guest-" + strconv.Itoa(f.guestSeq)
			f.guestBodies = append(f.guestBodies, raw)
			f.users[u.ID] = &u
			f.mu.Unlock()
			w.Header().Set("Location", "http://"+r.Host+"/admin/realms/sentinel/users/"+u.ID)
			w.WriteHeader(201)
			return
		}
		if u.Enabled || len(u.Attributes["registrationDate"]) != 1 || u.Attributes["organization"][0] != "ACME" {
			http.Error(w, "bad representation", 400)
			return
		}
		w.WriteHeader(201)
	}))
	mux.HandleFunc("GET /admin/realms/sentinel/users", auth(func(w http.ResponseWriter, r *http.Request) {
		enabled := r.URL.Query().Get("enabled") == "true"
		var out []keycloakUser
		for _, u := range f.users {
			if u.Enabled == enabled {
				out = append(out, *u)
			}
		}
		writeJSON(w, out)
	}))
	mux.HandleFunc("GET /admin/realms/sentinel/users/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		u, ok := f.users[r.PathValue("id")]
		if !ok {
			http.Error(w, "not found", 404)
			return
		}
		writeJSON(w, u)
	}))
	mux.HandleFunc("PUT /admin/realms/sentinel/users/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		u, ok := f.users[r.PathValue("id")]
		if !ok {
			http.Error(w, "not found", 404)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]interface{}
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.putBodies = append(f.putBodies, body)
		f.mu.Unlock()
		var patch map[string]json.RawMessage
		_ = json.Unmarshal(raw, &patch)
		if v, ok := patch["enabled"]; ok {
			_ = json.Unmarshal(v, &u.Enabled)
		}
		if v, ok := patch["attributes"]; ok {
			u.Attributes = nil // Keycloak replaces the attribute map; json.Unmarshal would merge
			_ = json.Unmarshal(v, &u.Attributes)
		}
		// Keycloak 26 (declarative user profile) takes the representation as the
		// whole profile: a missing email, firstName or lastName is cleared.
		var email, first, last string
		_ = json.Unmarshal(patch["email"], &email)
		_ = json.Unmarshal(patch["firstName"], &first)
		_ = json.Unmarshal(patch["lastName"], &last)
		u.Email, u.FirstName, u.LastName = email, first, last
		w.WriteHeader(204)
	}))
	mux.HandleFunc("DELETE /admin/realms/sentinel/users/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := f.users[r.PathValue("id")]; !ok {
			http.Error(w, "not found", 404)
			return
		}
		delete(f.users, r.PathValue("id"))
		w.WriteHeader(204)
	}))
	mux.HandleFunc("PUT /admin/realms/sentinel/users/{id}/execute-actions-email", auth(func(w http.ResponseWriter, r *http.Request) {
		var actions []string
		_ = json.NewDecoder(r.Body).Decode(&actions)
		if len(actions) != 1 || actions[0] != "VERIFY_EMAIL" {
			http.Error(w, "bad actions", 400)
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("GET /admin/realms/sentinel/roles", auth(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, realmRoles) }))
	mux.HandleFunc("GET /admin/realms/sentinel/roles/{role}/users", auth(func(w http.ResponseWriter, r *http.Request) {
		first, _ := strconv.Atoi(r.URL.Query().Get("first"))
		max, err := strconv.Atoi(r.URL.Query().Get("max"))
		if err != nil || max <= 0 {
			max = 100
		}
		var members []keycloakUser
		for id, roles := range f.userRoles {
			for _, role := range roles {
				if role.Name == r.PathValue("role") {
					if u, ok := f.users[id]; ok {
						members = append(members, *u)
					}
				}
			}
		}
		sort.Slice(members, func(i, j int) bool { return members[i].Username < members[j].Username })
		if first > len(members) {
			first = len(members)
		}
		members = members[first:]
		if len(members) > max {
			members = members[:max]
		}
		if members == nil {
			members = []keycloakUser{}
		}
		writeJSON(w, members)
	}))
	mux.HandleFunc("GET /admin/realms/sentinel/users/{id}/role-mappings/realm", auth(func(w http.ResponseWriter, r *http.Request) {
		roles := f.userRoles[r.PathValue("id")]
		if roles == nil {
			roles = []keycloakRole{}
		}
		writeJSON(w, roles)
	}))
	mux.HandleFunc("POST /admin/realms/sentinel/users/{id}/role-mappings/realm", auth(func(w http.ResponseWriter, r *http.Request) {
		if f.failRoleMap {
			http.Error(w, "boom", 500)
			return
		}
		var roles []keycloakRole
		_ = json.NewDecoder(r.Body).Decode(&roles)
		id := r.PathValue("id")
		f.userRoles[id] = append(f.userRoles[id], roles...)
		w.WriteHeader(204)
	}))
	mux.HandleFunc("DELETE /admin/realms/sentinel/users/{id}/role-mappings/realm", auth(func(w http.ResponseWriter, r *http.Request) {
		var roles []keycloakRole
		_ = json.NewDecoder(r.Body).Decode(&roles)
		id := r.PathValue("id")
		var kept []keycloakRole
		for _, have := range f.userRoles[id] {
			drop := false
			for _, d := range roles {
				if d.ID == have.ID {
					drop = true
				}
			}
			if !drop {
				kept = append(kept, have)
			}
		}
		f.userRoles[id] = kept
		w.WriteHeader(204)
	}))
	return httptest.NewServer(mux)
}

func (f *fakeKeycloak) roleNames(id string) []string {
	var out []string
	for _, r := range f.userRoles[id] {
		out = append(out, r.Name)
	}
	return out
}

func newClient(srv *httptest.Server) *KeycloakClient {
	return NewKeycloakClient(srv.URL, "sentinel", "sentinel-service", "secret", zap.NewNop())
}

func TestTokenIsCached(t *testing.T) {
	f := newFakeKeycloak()
	srv := f.server(t)
	defer srv.Close()
	c := newClient(srv)
	for i := 0; i < 3; i++ {
		if _, err := c.PendingRegistrations(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if f.tokenCalls != 1 {
		t.Errorf("token calls = %d, want 1", f.tokenCalls)
	}
}

func TestCreateUser(t *testing.T) {
	f := newFakeKeycloak()
	srv := f.server(t)
	defer srv.Close()
	c := newClient(srv)
	in := CreateUserInput{Username: "bob", Email: "b@x.io", Password: "longenough", Organization: "ACME", Justification: "work"}
	if err := c.CreateUser(context.Background(), in); err != nil {
		t.Fatalf("create: %v", err)
	}
	f.conflict = true
	err := c.CreateUser(context.Background(), in)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 409 || apiErr.Message != "User exists with same username" {
		t.Errorf("conflict mapping wrong: %v", err)
	}
}

func TestPendingRegistrationsFiltersByAttribute(t *testing.T) {
	f := newFakeKeycloak()
	srv := f.server(t)
	defer srv.Close()
	pending, err := newClient(srv).PendingRegistrations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "u-pending" || pending[0].Organization != "ACME" || pending[0].RegistrationDate != "2026-10-01T00:00:00Z" {
		t.Errorf("pending = %+v", pending)
	}
}

func TestApproveUser_HappyPath(t *testing.T) {
	f := newFakeKeycloak()
	srv := f.server(t)
	defer srv.Close()
	if err := newClient(srv).ApproveUser(context.Background(), "u-pending", "classification-s"); err != nil {
		t.Fatal(err)
	}
	u := f.users["u-pending"]
	if !u.Enabled {
		t.Error("user should be enabled")
	}
	if _, still := u.Attributes["registrationDate"]; still {
		t.Error("registrationDate should be cleared")
	}
	if u.Attributes["organization"][0] != "ACME" {
		t.Error("other attributes must be preserved")
	}
	roles := f.roleNames("u-pending")
	if !contains(roles, "sentinel-viewer") || !contains(roles, "classification-s") {
		t.Errorf("roles = %v", roles)
	}
	joined := strings.Join(f.calls, "\n")
	if !strings.Contains(joined, "PUT /admin/realms/sentinel/users/u-pending/execute-actions-email") {
		t.Error("verification email not triggered")
	}
}

// Every PUT must carry the whole representation: under Keycloak 26 a partial
// one clears the e-mail, and the verification e-mail then fails with "User
// email missing".
func TestApproveUser_KeepsProfile(t *testing.T) {
	f := newFakeKeycloak()
	srv := f.server(t)
	defer srv.Close()
	if err := newClient(srv).ApproveUser(context.Background(), "u-pending", "classification-u"); err != nil {
		t.Fatal(err)
	}
	if len(f.putBodies) == 0 {
		t.Fatal("no PUT /users/{id}")
	}
	for i, body := range f.putBodies {
		if body["username"] != "newbie" || body["email"] != "n@x.io" || body["firstName"] != "New" || body["lastName"] != "Bie" {
			t.Errorf("PUT %d dropped the profile: %v", i, body)
		}
	}
	u := f.users["u-pending"]
	if u.Email != "n@x.io" || u.FirstName != "New" || u.LastName != "Bie" {
		t.Errorf("profile after approval = %q %q %q", u.Email, u.FirstName, u.LastName)
	}
}

func TestApproveUser_RollsBackKeepsProfile(t *testing.T) {
	f := newFakeKeycloak()
	f.failRoleMap = true
	srv := f.server(t)
	defer srv.Close()
	_ = newClient(srv).ApproveUser(context.Background(), "u-pending", "classification-u")
	u := f.users["u-pending"]
	if u.Enabled || u.Email != "n@x.io" || u.FirstName != "New" || u.LastName != "Bie" {
		t.Errorf("after rollback: enabled=%v profile=%q %q %q", u.Enabled, u.Email, u.FirstName, u.LastName)
	}
}

func TestApproveUser_RollsBackWhenRolesFail(t *testing.T) {
	f := newFakeKeycloak()
	f.failRoleMap = true
	srv := f.server(t)
	defer srv.Close()
	err := newClient(srv).ApproveUser(context.Background(), "u-pending", "classification-u")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 500 {
		t.Fatalf("expected 500 APIError, got %v", err)
	}
	u := f.users["u-pending"]
	if u.Enabled {
		t.Error("user should be re-disabled after role failure")
	}
	if _, kept := u.Attributes["registrationDate"]; !kept {
		t.Error("registrationDate must remain so the user stays pending")
	}
}

func TestApproveUser_NotFound(t *testing.T) {
	f := newFakeKeycloak()
	srv := f.server(t)
	defer srv.Close()
	err := newClient(srv).ApproveUser(context.Background(), "nope", "classification-u")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 404 {
		t.Errorf("expected 404, got %v", err)
	}
}

func TestRejectUser(t *testing.T) {
	f := newFakeKeycloak()
	srv := f.server(t)
	defer srv.Close()
	rej, err := newClient(srv).RejectUser(context.Background(), "u-pending")
	if err != nil {
		t.Fatal(err)
	}
	if rej.Email != "n@x.io" || rej.FirstName != "New" {
		t.Errorf("rejected = %+v", rej)
	}
	if _, exists := f.users["u-pending"]; exists {
		t.Error("user should be deleted")
	}
}

func TestUpdateClassification(t *testing.T) {
	f := newFakeKeycloak()
	srv := f.server(t)
	defer srv.Close()
	c := newClient(srv)
	if err := c.UpdateClassification(context.Background(), "u-active", "classification-ts"); err != nil {
		t.Fatal(err)
	}
	roles := f.roleNames("u-active")
	if contains(roles, "classification-s") || !contains(roles, "classification-ts") || !contains(roles, "sentinel-admin") {
		t.Errorf("roles = %v", roles)
	}
	err := c.UpdateClassification(context.Background(), "u-active", "classification-x")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 400 {
		t.Errorf("invalid level should be 400, got %v", err)
	}
}

func TestActiveUsers(t *testing.T) {
	f := newFakeKeycloak()
	srv := f.server(t)
	defer srv.Close()
	users, err := newClient(srv).ActiveUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Username != "alice" {
		t.Fatalf("users = %+v (service accounts must be excluded)", users)
	}
	if users[0].ClassificationLevel == nil || *users[0].ClassificationLevel != "classification-s" || !contains(users[0].Roles, "sentinel-admin") {
		t.Errorf("user = %+v", users[0])
	}
}

func TestTokenFailureIs503(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", 500) }))
	defer srv.Close()
	_, err := newClient(srv).ActiveUsers(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 503 {
		t.Errorf("expected 503, got %v", err)
	}
}

func TestCreateGuestUserCreatesEnabledUserWithRoles(t *testing.T) {
	f := newFakeKeycloak()
	srv := f.server(t)
	defer srv.Close()
	c := newClient(srv)
	fixed := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return fixed }

	id, err := c.CreateGuestUser(context.Background(), "guest-abc", "guest-pass", []string{"sentinel-viewer", "classification-u", "sentinel-guest"})
	if err != nil {
		t.Fatalf("create guest: %v", err)
	}
	if id != "guest-1" {
		t.Errorf("id = %q, want the last segment of Location", id)
	}
	if len(f.guestBodies) != 1 {
		t.Fatalf("guest POSTs = %d, want 1", len(f.guestBodies))
	}
	body := f.guestBodies[0]
	if body["enabled"] != true || body["username"] != "guest-abc" {
		t.Errorf("body = %v", body)
	}
	if ra, present := body["requiredActions"]; present && len(ra.([]interface{})) != 0 {
		t.Errorf("requiredActions = %v, want none", ra)
	}
	attrs := body["attributes"].(map[string]interface{})
	if got := attrs["sentinelGuestCreatedAt"].([]interface{})[0]; got != "2026-10-22T12:00:00Z" {
		t.Errorf("sentinelGuestCreatedAt = %v", got)
	}
	// The realm profile requires email and names for login; the guest has them.
	if body["email"] == "" || body["firstName"] == "" || body["lastName"] == "" {
		t.Errorf("profile incomplete: %v", body)
	}
	got := f.roleNames("guest-1")
	sort.Strings(got)
	if strings.Join(got, ",") != "classification-u,sentinel-guest,sentinel-viewer" {
		t.Errorf("roles = %v", got)
	}
}

func TestCreateGuestUserDeletesUserWhenRolesFail(t *testing.T) {
	f := newFakeKeycloak()
	f.failRoleMap = true
	srv := f.server(t)
	defer srv.Close()
	if _, err := newClient(srv).CreateGuestUser(context.Background(), "guest-x", "guest-pass", []string{"sentinel-guest"}); err == nil {
		t.Fatal("want an error")
	}
	if _, left := f.users["guest-1"]; left {
		t.Error("a guest without its roles must not be left behind")
	}
}

func TestCreateGuestUserFailsWhenARoleIsMissing(t *testing.T) {
	f := newFakeKeycloak()
	srv := f.server(t)
	defer srv.Close()
	_, err := newClient(srv).CreateGuestUser(context.Background(), "guest-x", "guest-pass", []string{"sentinel-viewer", "no-such-role"})
	if err == nil {
		t.Fatal("want an error when a role does not exist")
	}
	if _, left := f.users["guest-1"]; left {
		t.Error("user left behind")
	}
}

func TestDeleteUserTreats404AsGone(t *testing.T) {
	f := newFakeKeycloak()
	srv := f.server(t)
	defer srv.Close()
	c := newClient(srv)
	if err := c.DeleteUser(context.Background(), "u-active"); err != nil {
		t.Fatalf("delete existing: %v", err)
	}
	if _, still := f.users["u-active"]; still {
		t.Error("user not deleted")
	}
	if err := c.DeleteUser(context.Background(), "u-active"); err != nil {
		t.Errorf("delete missing should succeed, got %v", err)
	}
}

func TestUsersWithRoleParsesCreatedAt(t *testing.T) {
	f := newFakeKeycloak()
	created := time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC)
	attr := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	f.users["g-ts"] = &keycloakUser{ID: "g-ts", Username: "guest-ts", Enabled: true, CreatedTimestamp: created.UnixMilli()}
	f.users["g-attr"] = &keycloakUser{ID: "g-attr", Username: "guest-attr", Enabled: true, CreatedTimestamp: created.UnixMilli(),
		Attributes: map[string][]string{"sentinelGuestCreatedAt": {attr.Format(time.RFC3339)}}}
	f.userRoles["g-ts"] = []keycloakRole{{ID: "r-guest", Name: "sentinel-guest"}}
	f.userRoles["g-attr"] = []keycloakRole{{ID: "r-guest", Name: "sentinel-guest"}}
	srv := f.server(t)
	defer srv.Close()

	got, err := newClient(srv).UsersWithRole(context.Background(), "sentinel-guest")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	// ordered by username in the fake: guest-attr, guest-ts
	if got[0].ID != "g-attr" || got[0].Username != "guest-attr" || !got[0].CreatedAt.Equal(attr) {
		t.Errorf("attribute should win: %+v", got[0])
	}
	if got[1].ID != "g-ts" || !got[1].CreatedAt.Equal(created) {
		t.Errorf("createdTimestamp fallback: %+v", got[1])
	}
}

func TestUsersWithRolePagesUntilShortPage(t *testing.T) {
	f := newFakeKeycloak()
	const n = 250
	for i := 0; i < n; i++ {
		id := "g-" + strconv.Itoa(i)
		f.users[id] = &keycloakUser{ID: id, Username: "guest-" + strconv.Itoa(1000+i), Enabled: true, CreatedTimestamp: 1}
		f.userRoles[id] = []keycloakRole{{ID: "r-guest", Name: "sentinel-guest"}}
	}
	srv := f.server(t)
	defer srv.Close()
	got, err := newClient(srv).UsersWithRole(context.Background(), "sentinel-guest")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != n {
		t.Errorf("got %d users, want %d", len(got), n)
	}
}

func TestPasswordGrantPostsToTokenEndpoint(t *testing.T) {
	f := newFakeKeycloak()
	srv := f.server(t)
	defer srv.Close()
	c := newClient(srv)
	tok, err := c.PasswordGrant(context.Background(), "sentinel-web", "guest-abc", "guest-pass")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "guest-at" || tok.RefreshToken != "guest-rt" || tok.ExpiresIn != 300 {
		t.Errorf("token = %+v", tok)
	}
	form := f.passwordForms[0]
	if form.Get("grant_type") != "password" || form.Get("client_id") != "sentinel-web" || form.Get("username") != "guest-abc" {
		t.Errorf("form = %v", form)
	}
	if f.tokenCalls != 0 {
		t.Error("password grant must not fetch an admin token")
	}

	if _, err := c.PasswordGrant(context.Background(), "sentinel-web", "guest-abc", "wrong"); err == nil {
		t.Error("want an error for a rejected grant")
	}
}
