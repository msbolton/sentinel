package mgmt

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sentinel/go-common/middleware"
)

func TestAuthRolesForEmplacements(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	h := Auth(AuthOptions{})(ok)
	cases := []struct {
		method, path, roles string
		want                int
	}{
		{http.MethodPost, "/emplacements", "sentinel-analyst", 403},
		{http.MethodPost, "/emplacements", "sentinel-operator", 204},
		{http.MethodPatch, "/emplacements/x", "sentinel-operator", 204},
		{http.MethodPost, "/emplacements", "sentinel-viewer", 403},
		{http.MethodDelete, "/emplacements/x", "sentinel-operator", 403},
		{http.MethodDelete, "/emplacements/x", "sentinel-admin", 204},
		{http.MethodPost, "/feeds", "sentinel-analyst", 403}, // feeds are unchanged
		{http.MethodPost, "/feeds", "sentinel-operator", 204},
		{http.MethodGet, "/emplacements", "", 204},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		r.Header.Set(middleware.HeaderUserRoles, c.roles)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s %s as %q: %d, want %d", c.method, c.path, c.roles, w.Code, c.want)
		}
	}
}
