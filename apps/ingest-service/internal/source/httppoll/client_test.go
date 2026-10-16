package httppoll

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestOAuth2TokenCachedAndSent(t *testing.T) {
	var tokenHits atomic.Int32
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenHits.Add(1)
		if r.Method != http.MethodPost || r.FormValue("grant_type") != "client_credentials" || r.FormValue("client_id") != "id" || r.FormValue("client_secret") != "sec" {
			t.Errorf("bad token request: %v %v", r.Method, r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok-1","expires_in":3600}`))
	}))
	defer tokenServer.Close()
	var auth string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("X-Rate-Limit-Remaining", "42")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer api.Close()

	c := NewClient(time.Second, Auth{Kind: AuthOAuth2, TokenURL: tokenServer.URL, ClientID: "id", ClientSecret: "sec"})
	for i := 0; i < 2; i++ {
		resp, err := c.GetOK(context.Background(), api.URL)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Header.Get("X-Rate-Limit-Remaining") != "42" || string(resp.Body) != "{}" {
			t.Errorf("response = %+v", resp)
		}
	}
	if auth != "Bearer tok-1" || tokenHits.Load() != 1 {
		t.Errorf("auth=%q tokenHits=%d", auth, tokenHits.Load())
	}
}

func TestOtherAuthKindsAndErrors(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("slow down"))
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	c := NewClient(time.Second, Auth{Kind: AuthBasic, Username: "u", Password: "p"})
	if _, err := c.GetOK(ctx, srv.URL); err != nil || got.Get("Authorization") == "" {
		t.Errorf("basic: %v %v", err, got)
	}
	c = NewClient(time.Second, Auth{Kind: AuthHeader, HeaderName: "X-Api-Key", Token: "k"})
	if _, err := c.GetOK(ctx, srv.URL); err != nil || got.Get("X-Api-Key") != "k" {
		t.Errorf("header: %v %v", err, got)
	}
	c = NewClient(time.Second, Auth{Kind: AuthBearer, Token: "b"})
	_, err := c.GetOK(ctx, srv.URL+"/fail")
	var se *StatusError
	if !errors.As(err, &se) || se.Status != 429 || se.Body != "slow down" {
		t.Errorf("status error = %v", err)
	}
	for _, a := range []Auth{{Kind: AuthBasic}, {Kind: AuthBearer}, {Kind: AuthHeader, HeaderName: "x"}, {Kind: AuthOAuth2, TokenURL: "x"}, {Kind: "magic"}} {
		if err := a.Validate(); err == nil {
			t.Errorf("%+v should be invalid", a)
		}
	}
	if err := (Auth{}).Validate(); err != nil {
		t.Error(err)
	}
}
