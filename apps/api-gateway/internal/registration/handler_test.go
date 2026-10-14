package registration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

type fakeAdmin struct {
	created  []CreateUserInput
	approved map[string]string
	err      error
}

func (f *fakeAdmin) CreateUser(_ context.Context, in CreateUserInput) error {
	if f.err != nil {
		return f.err
	}
	f.created = append(f.created, in)
	return nil
}
func (f *fakeAdmin) PendingRegistrations(context.Context) ([]PendingUser, error) {
	return []PendingUser{{ID: "p1", Username: "pending"}}, f.err
}
func (f *fakeAdmin) ApproveUser(_ context.Context, id, level string) error {
	if f.err != nil {
		return f.err
	}
	if f.approved == nil {
		f.approved = map[string]string{}
	}
	f.approved[id] = level
	return nil
}
func (f *fakeAdmin) RejectUser(_ context.Context, id string) (RejectedUser, error) {
	if f.err != nil {
		return RejectedUser{}, f.err
	}
	return RejectedUser{Email: "r@x.io", FirstName: "Rae"}, nil
}
func (f *fakeAdmin) UpdateClassification(_ context.Context, id, level string) error { return f.err }
func (f *fakeAdmin) ActiveUsers(context.Context) ([]ActiveUser, error) {
	return []ActiveUser{{ID: "a1", Username: "alice", Roles: []string{}}}, f.err
}

type fakeMailer struct {
	sent []string
	err  error
}

func (m *fakeMailer) Send(_ context.Context, to, subject, body string) error {
	m.sent = append(m.sent, to+"|"+subject+"|"+body)
	return m.err
}

func setup(admin *fakeAdmin, mailer *fakeMailer, limiter *IPLimiter) http.Handler {
	h := NewHandler(admin, mailer, limiter, zap.NewNop())
	mux := http.NewServeMux()
	h.RegisterPublicRoutes(mux)
	h.RegisterAdminRoutes(mux)
	return mux
}

func post(h http.Handler, path, body, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if ip != "" {
		req.RemoteAddr = ip + ":1234"
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func msg(rr *httptest.ResponseRecorder) string {
	var m map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &m)
	s, _ := m["message"].(string)
	return s
}

const goodRegistration = `{"username":"bob","email":"b@x.io","password":"longenough","confirmPassword":"longenough","firstName":"Bob","lastName":"B","organization":"ACME","justification":"work"}`

func TestRegister_Validation(t *testing.T) {
	admin := &fakeAdmin{}
	h := setup(admin, &fakeMailer{}, nil)
	cases := map[string]string{
		`{"username":"bob","email":"b@x.io","password":"abc","confirmPassword":"abd"}`:          "Passwords do not match",
		`{"username":"bob","email":"b@x.io","password":"short","confirmPassword":"short"}`:      "Password must be at least 8 characters",
		`{"email":"b@x.io","password":"longenough","confirmPassword":"longenough"}`:              "username and email are required",
		`not json`: "Invalid request body",
	}
	for body, want := range cases {
		rr := post(h, "/api/v1/auth/register", body, "")
		if rr.Code != 400 || msg(rr) != want {
			t.Errorf("%s -> %d %q, want 400 %q", body, rr.Code, msg(rr), want)
		}
	}
	if len(admin.created) != 0 {
		t.Error("invalid requests must not reach Keycloak")
	}
}

func TestRegister_SuccessAndConflict(t *testing.T) {
	admin := &fakeAdmin{}
	h := setup(admin, &fakeMailer{}, nil)
	rr := post(h, "/api/v1/auth/register", goodRegistration, "")
	if rr.Code != 201 || !strings.Contains(msg(rr), "Pending admin approval") {
		t.Fatalf("register: %d %s", rr.Code, rr.Body.String())
	}
	if len(admin.created) != 1 || admin.created[0].Organization != "ACME" || admin.created[0].Password != "longenough" {
		t.Errorf("created = %+v", admin.created)
	}
	admin.err = &APIError{Status: 409, Message: "User exists with same email"}
	rr = post(h, "/api/v1/auth/register", goodRegistration, "")
	if rr.Code != 409 || msg(rr) != "User exists with same email" {
		t.Errorf("conflict: %d %s", rr.Code, rr.Body.String())
	}
	admin.err = errors.New("unexpected")
	if rr = post(h, "/api/v1/auth/register", goodRegistration, ""); rr.Code != 500 {
		t.Errorf("unknown error should be 500, got %d", rr.Code)
	}
}

func TestRegister_RateLimited(t *testing.T) {
	h := setup(&fakeAdmin{}, &fakeMailer{}, NewIPLimiter(5, 15*time.Minute))
	for i := 0; i < 5; i++ {
		if rr := post(h, "/api/v1/auth/register", goodRegistration, "10.0.0.1"); rr.Code != 201 {
			t.Fatalf("request %d: %d", i+1, rr.Code)
		}
	}
	if rr := post(h, "/api/v1/auth/register", goodRegistration, "10.0.0.1"); rr.Code != 429 {
		t.Errorf("6th request should be 429, got %d", rr.Code)
	}
	if rr := post(h, "/api/v1/auth/register", goodRegistration, "10.0.0.2"); rr.Code != 201 {
		t.Errorf("other IP should pass, got %d", rr.Code)
	}
}

func TestApprove(t *testing.T) {
	admin := &fakeAdmin{}
	h := setup(admin, &fakeMailer{}, nil)
	if rr := post(h, "/api/v1/auth/approve-registration/u1", "", ""); rr.Code != 201 || admin.approved["u1"] != "classification-u" {
		t.Errorf("default approve: %d %v", rr.Code, admin.approved)
	}
	if rr := post(h, "/api/v1/auth/approve-registration/u2", `{"classificationLevel":"classification-ts"}`, ""); rr.Code != 201 || admin.approved["u2"] != "classification-ts" {
		t.Errorf("explicit approve: %d %v", rr.Code, admin.approved)
	}
	if rr := post(h, "/api/v1/auth/approve-registration/u3", `{"classificationLevel":"classification-x"}`, ""); rr.Code != 400 {
		t.Errorf("invalid level: %d", rr.Code)
	}
	admin.err = &APIError{Status: 404, Message: "User not found"}
	if rr := post(h, "/api/v1/auth/approve-registration/u4", "", ""); rr.Code != 404 {
		t.Errorf("not found: %d", rr.Code)
	}
}

func TestReject_SendsMailAndToleratesMailFailure(t *testing.T) {
	mailer := &fakeMailer{}
	h := setup(&fakeAdmin{}, mailer, nil)
	rr := post(h, "/api/v1/auth/reject-registration/u1", "{}", "")
	if rr.Code != 201 || len(mailer.sent) != 1 || !strings.HasPrefix(mailer.sent[0], "r@x.io|Sentinel") || !strings.Contains(mailer.sent[0], "Dear Rae") {
		t.Errorf("reject: %d sent=%v", rr.Code, mailer.sent)
	}
	mailer.err = errors.New("smtp down")
	if rr := post(h, "/api/v1/auth/reject-registration/u2", "{}", ""); rr.Code != 201 {
		t.Errorf("mail failure must not fail the rejection, got %d", rr.Code)
	}
}

func TestUsersAndClassification(t *testing.T) {
	h := setup(&fakeAdmin{}, &fakeMailer{}, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/auth/users", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"username":"alice"`) {
		t.Errorf("users: %d %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/auth/pending-registrations", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"username":"pending"`) {
		t.Errorf("pending: %d %s", rr.Code, rr.Body.String())
	}
	put := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/users/u1/classification", strings.NewReader(body))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}
	if rr := put(`{"classificationLevel":"classification-s"}`); rr.Code != 200 {
		t.Errorf("classification: %d", rr.Code)
	}
	if rr := put(`{"classificationLevel":"bogus"}`); rr.Code != 400 {
		t.Errorf("bad classification: %d", rr.Code)
	}
	if rr := put(`{}`); rr.Code != 400 {
		t.Errorf("missing classification: %d", rr.Code)
	}
}

func TestIPLimiter_Evicts(t *testing.T) {
	l := NewIPLimiter(2, time.Minute)
	now := time.Now()
	l.now = func() time.Time { return now }
	if !l.Allow("a") || !l.Allow("a") || l.Allow("a") {
		t.Fatal("burst of 2 then deny")
	}
	now = now.Add(2 * time.Minute)
	if !l.Allow("a") {
		t.Error("should refill after the window")
	}
	if len(l.clients) != 1 {
		t.Errorf("stale entries should be evicted, have %d", len(l.clients))
	}
}
