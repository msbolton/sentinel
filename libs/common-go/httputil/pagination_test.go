package httputil

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParsePagination(t *testing.T) {
	cases := map[string]Pagination{
		"":                      {Page: 1, PageSize: DefaultPageSize, Offset: 0},
		"page=3&pageSize=20":    {Page: 3, PageSize: 20, Offset: 40},
		"page=0&pageSize=0":     {Page: 1, PageSize: DefaultPageSize, Offset: 0},
		"page=-5":               {Page: 1, PageSize: DefaultPageSize, Offset: 0},
		"pageSize=99999":        {Page: 1, PageSize: MaxPageSize, Offset: 0},
		"page=abc&pageSize=xyz": {Page: 1, PageSize: DefaultPageSize, Offset: 0},
	}
	for query, want := range cases {
		got := ParsePagination(httptest.NewRequest(http.MethodGet, "/?"+query, nil))
		if got != want {
			t.Errorf("%q: got %+v, want %+v", query, got, want)
		}
	}
}

func TestErrorShape(t *testing.T) {
	rr := httptest.NewRecorder()
	Error(rr, http.StatusNotFound, "Entity not found")
	if rr.Code != 404 || rr.Header().Get("Content-Type") != "application/json" {
		t.Errorf("status %d, content-type %q", rr.Code, rr.Header().Get("Content-Type"))
	}
	if rr.Body.String() != "{\"message\":\"Entity not found\",\"statusCode\":404}\n" {
		t.Errorf("body = %s", rr.Body.String())
	}
	rr = httptest.NewRecorder()
	NoContent(rr)
	if rr.Code != 204 || rr.Body.Len() != 0 {
		t.Errorf("no content: %d %q", rr.Code, rr.Body.String())
	}
}
