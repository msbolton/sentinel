package demo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sentinel/api-gateway/internal/auth"
	"github.com/sentinel/api-gateway/internal/registration"
)

// fakeLocator maps IPs to fixed points.
type fakeLocator map[string][2]float64

func (f fakeLocator) Locate(ip string) (float64, float64, bool) {
	p, ok := f[ip]
	return p[0], p[1], ok
}

type handlerRig struct {
	*guestRig
	mux *http.ServeMux
}

func newHandlerRig(t *testing.T, max int, loc Locator, limiter *registration.IPLimiter) *handlerRig {
	t.Helper()
	g := newGuestRig(t, max)
	if loc == nil {
		loc = NopLocator{}
	}
	h := NewHandler(g.guests, g.sites, loc, "demo-visitors", limiter, g.guests.logger)
	mux := http.NewServeMux()
	h.RegisterPublicRoutes(mux)
	h.RegisterProtectedRoutes(mux)
	return &handlerRig{guestRig: g, mux: mux}
}

func (r *handlerRig) postSession(t *testing.T, xff string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/demo/session", nil)
	req.RemoteAddr = "10.0.0.1:5000"
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, req)
	return rec
}

// startGuest makes a guest through the registry and returns the principal the
// gateway's auth middleware would attach for it.
func (r *handlerRig) startGuest(t *testing.T) (*auth.User, Session) {
	t.Helper()
	s, err := r.guests.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	created := r.kc.createdGuests()
	c := created[len(created)-1]
	return &auth.User{UserID: c.ID, Username: c.Username, Roles: []string{auth.RoleViewer, auth.RoleGuest}}, s
}

func (r *handlerRig) postPosition(t *testing.T, u *auth.User, body, xff string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/demo/position", strings.NewReader(body))
	req.RemoteAddr = "10.0.0.1:5000"
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	if u != nil {
		req = req.WithContext(auth.WithUser(req.Context(), u))
	}
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, req)
	return rec
}

func TestSessionReturnsTokensAndFeedID(t *testing.T) {
	rig := newHandlerRig(t, 25, nil, nil)
	rec := rig.postSession(t, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"accessToken", "refreshToken", "expiresIn", "guestId", "expiresAt", "visitorsFeedId"} {
		if _, ok := got[k]; !ok {
			t.Errorf("session JSON lacks %q: %s", k, rec.Body)
		}
	}
	if len(got) != 6 {
		t.Errorf("session JSON has %d keys, want 6: %s", len(got), rec.Body)
	}
	if got["visitorsFeedId"] != "demo-visitors" {
		t.Errorf("visitorsFeedId = %v, want demo-visitors", got["visitorsFeedId"])
	}
	pw := rig.kc.createdGuests()[0].Password
	if strings.Contains(rec.Body.String(), pw) {
		t.Error("session response leaks the guest password")
	}
}

func TestSessionFullAnswers503(t *testing.T) {
	rig := newHandlerRig(t, 2, nil, nil)
	for i := 0; i < 2; i++ {
		if rec := rig.postSession(t, ""); rec.Code != http.StatusOK {
			t.Fatalf("session %d: status %d", i, rec.Code)
		}
	}
	rec := rig.postSession(t, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"error":"full"}` {
		t.Fatalf("body = %s, want {\"error\":\"full\"}", body)
	}
}

func TestSessionKeycloakFailureIs502(t *testing.T) {
	rig := newHandlerRig(t, 2, nil, nil)
	rig.kc.createErr = errors.New("keycloak down")
	if rec := rig.postSession(t, ""); rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestSessionRateLimitedPerIP(t *testing.T) {
	rig := newHandlerRig(t, 25, nil, registration.NewIPLimiter(5, 10*time.Minute))
	// The leftmost entry changes every time; only the rightmost counts.
	for i := 0; i < 5; i++ {
		rec := rig.postSession(t, fmt.Sprintf("192.0.2.%d, 203.0.113.7", i))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status %d, want 200", i+1, rec.Code)
		}
	}
	rec := rig.postSession(t, "192.0.2.99, 203.0.113.7")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th request: status %d, want 429", rec.Code)
	}
	if n := len(rig.kc.createdGuests()); n != 5 {
		t.Errorf("Keycloak saw %d creates, want 5", n)
	}
	// Another visitor is unaffected.
	if rec := rig.postSession(t, "203.0.113.7, 198.51.100.1"); rec.Code != http.StatusOK {
		t.Fatalf("other IP: status %d, want 200", rec.Code)
	}
}

func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return got
}

func TestPositionStoresOnlyBlurred(t *testing.T) {
	rig := newHandlerRig(t, 25, nil, nil)
	u, s := rig.startGuest(t)

	rec := rig.postPosition(t, u, `{"lat":51.5074,"lon":-0.1278}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	placed := rig.sites.placements()
	if len(placed) != 1 {
		t.Fatalf("Place called %d times, want 1", len(placed))
	}
	p := placed[0]
	if p.GuestID != s.GuestID || !near(p.Lat, 51.55) || !near(p.Lon, -0.15) {
		t.Fatalf("Place(%q, %v, %v), want (%q, 51.55, -0.15)", p.GuestID, p.Lat, p.Lon, s.GuestID)
	}
	got := decodeResponse(t, rec)
	if got["lat"] != 51.5074 || got["lon"] != -0.1278 || got["source"] != "browser" || got["emplacementId"] != "emp-"+s.GuestID {
		t.Fatalf("response = %s", rec.Body)
	}
}

func TestPositionDeniedUsesIP(t *testing.T) {
	loc := fakeLocator{"203.0.113.7": {48.8566, 2.3522}}
	rig := newHandlerRig(t, 25, loc, nil)
	u, s := rig.startGuest(t)

	rec := rig.postPosition(t, u, `{"denied":true}`, "198.51.100.1, 203.0.113.7")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	placed := rig.sites.placements()
	if len(placed) != 1 {
		t.Fatalf("Place called %d times, want 1", len(placed))
	}
	wantLat, wantLon := Blur(48.8566, 2.3522)
	if p := placed[0]; p.GuestID != s.GuestID || p.Lat != wantLat || p.Lon != wantLon {
		t.Fatalf("Place(%q, %v, %v), want (%q, %v, %v)", p.GuestID, p.Lat, p.Lon, s.GuestID, wantLat, wantLon)
	}
	got := decodeResponse(t, rec)
	if got["lat"] != 48.8566 || got["lon"] != 2.3522 || got["source"] != "ip" || got["emplacementId"] != "emp-"+s.GuestID {
		t.Fatalf("response = %s", rec.Body)
	}
}

func TestPositionDeniedUnknownIP(t *testing.T) {
	rig := newHandlerRig(t, 25, fakeLocator{}, nil)
	u, _ := rig.startGuest(t)

	rec := rig.postPosition(t, u, `{"denied":true}`, "203.0.113.7")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"source":"none"}` {
		t.Fatalf("body = %s, want {\"source\":\"none\"}", body)
	}
	if n := len(rig.sites.placements()); n != 0 {
		t.Fatalf("Place called %d times, want 0", n)
	}
}

func TestPositionDeniedOutOfRangeIPPointIsNone(t *testing.T) {
	rig := newHandlerRig(t, 25, fakeLocator{"203.0.113.7": {91, 0}}, nil)
	u, _ := rig.startGuest(t)
	rec := rig.postPosition(t, u, `{"denied":true}`, "203.0.113.7")
	if body := strings.TrimSpace(rec.Body.String()); rec.Code != http.StatusOK || body != `{"source":"none"}` {
		t.Fatalf("status %d body %s, want 200 {\"source\":\"none\"}", rec.Code, body)
	}
	if n := len(rig.sites.placements()); n != 0 {
		t.Fatalf("Place called %d times, want 0", n)
	}
}

func TestPositionRefusesNonGuest(t *testing.T) {
	rig := newHandlerRig(t, 25, nil, nil)
	for name, u := range map[string]*auth.User{
		"anonymous": nil,
		"viewer":    {UserID: "u-1", Roles: []string{auth.RoleViewer}},
		"admin":     {UserID: "u-2", Roles: []string{auth.RoleAdmin}},
		// Holds the role but is not in the registry, e.g. already swept.
		"unknown guest": {UserID: "u-3", Roles: []string{auth.RoleGuest}},
	} {
		rec := rig.postPosition(t, u, `{"lat":51.5074,"lon":-0.1278}`, "")
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", name, rec.Code)
		}
	}
	if n := len(rig.sites.placements()); n != 0 {
		t.Fatalf("Place called %d times, want 0", n)
	}
}

func TestPositionRejectsBadBodies(t *testing.T) {
	rig := newHandlerRig(t, 25, fakeLocator{"10.0.0.1": {1, 1}}, nil)
	u, _ := rig.startGuest(t)
	for name, body := range map[string]string{
		"not json":         `lat=1`,
		"empty object":     `{}`,
		"lat only":         `{"lat":51.5}`,
		"lon only":         `{"lon":-0.1}`,
		"lat too high":     `{"lat":90.0001,"lon":0}`,
		"lat too low":      `{"lat":-91,"lon":0}`,
		"lon too high":     `{"lat":0,"lon":180.5}`,
		"lon too low":      `{"lat":0,"lon":-181}`,
		"overflow":         `{"lat":1e400,"lon":0}`,
		"string lat":       `{"lat":"NaN","lon":0}`,
		"denied false":     `{"denied":false}`,
		"denied with lat":  `{"denied":true,"lat":51.5,"lon":-0.1}`,
		"oversized":        `{"lat":51.5,"lon":-0.1,"pad":"` + strings.Repeat("x", 2048) + `"}`,
		"trailing garbage": `{"lat":51.5,"lon":-0.1} {"lat":1,"lon":1}`,
	} {
		rec := rig.postPosition(t, u, body, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (body %s)", name, rec.Code, rec.Body)
		}
	}
	if n := len(rig.sites.placements()); n != 0 {
		t.Fatalf("Place called %d times, want 0", n)
	}
}

func TestPositionBoundaryCoordinatesAccepted(t *testing.T) {
	rig := newHandlerRig(t, 25, nil, nil)
	u, _ := rig.startGuest(t)
	for _, body := range []string{`{"lat":90,"lon":180}`, `{"lat":-90,"lon":-180}`, `{"lat":0,"lon":0}`} {
		if rec := rig.postPosition(t, u, body, ""); rec.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200", body, rec.Code)
		}
	}
}

func TestValidCoordinateRejectsNonFinite(t *testing.T) {
	// JSON cannot carry NaN or Inf, so the handler's guard is tested directly.
	nan, inf := math.NaN(), math.Inf(1)
	for _, c := range [][2]float64{{nan, 0}, {0, nan}, {inf, 0}, {0, -inf}} {
		if validCoordinate(c[0], c[1]) {
			t.Errorf("validCoordinate(%v, %v) = true", c[0], c[1])
		}
	}
}

func TestPositionPlaceFailureIs502(t *testing.T) {
	rig := newHandlerRig(t, 25, nil, nil)
	u, _ := rig.startGuest(t)
	rig.sites.placeErr = errors.New("ingest down")
	if rec := rig.postPosition(t, u, `{"lat":51.5074,"lon":-0.1278}`, ""); rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestPositionNeverLogsPrecise(t *testing.T) {
	loc := fakeLocator{"203.0.113.7": {51.5074, -0.1278}}
	rig := newHandlerRig(t, 25, loc, nil)
	u, _ := rig.startGuest(t)

	// Success, IP fallback, a failed Place and a bad body all get a chance to log.
	rig.postPosition(t, u, `{"lat":51.5074,"lon":-0.1278}`, "")
	rig.postPosition(t, u, `{"denied":true}`, "203.0.113.7")
	rig.sites.placeErr = errors.New("ingest down")
	rig.postPosition(t, u, `{"lat":51.5074,"lon":-0.1278}`, "")
	rig.postPosition(t, u, `{"lat":51.5074,"lon":-0.1278,"x":`, "")

	if rig.logs.Len() == 0 {
		t.Fatal("expected the failed Place to be logged; the check below would be vacuous")
	}
	for _, e := range rig.logs.All() {
		line := fmt.Sprint(e.Message, " ", e.ContextMap())
		if strings.Contains(line, "51.5074") || strings.Contains(line, "0.1278") {
			t.Errorf("log entry leaks the precise position: %s", line)
		}
	}
}
