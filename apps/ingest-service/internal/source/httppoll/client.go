// Package httppoll is what polled HTTP sources share: an authenticated
// client and a scheduler that turns an interval, an area of interest and
// quota feedback into poll timing.
package httppoll

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Auth kinds.
const (
	AuthNone   = "none"
	AuthBasic  = "basic"
	AuthBearer = "bearer"
	AuthHeader = "header"
	AuthOAuth2 = "oauth2_client_credentials"
)

// Auth configures request authentication. Secrets come from the feed's
// secret refs (password, token, clientSecret).
type Auth struct {
	Kind         string `json:"kind"`
	Username     string `json:"username"`
	Password     string `json:"-"`
	Token        string `json:"-"`
	HeaderName   string `json:"headerName"`
	TokenURL     string `json:"tokenUrl"`
	ClientID     string `json:"-"`
	ClientSecret string `json:"-"`
	Scopes       string `json:"scopes"`
}

// Response is a completed request.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Client issues authenticated requests with a bounded response body.
type Client struct {
	HTTP      *http.Client
	Auth      Auth
	UserAgent string
	MaxBody   int64 // bytes read from a response (default 32 MiB)

	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time
}

// NewClient builds a client with the given timeout.
func NewClient(timeout time.Duration, auth Auth) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if auth.Kind == "" {
		auth.Kind = AuthNone
	}
	return &Client{HTTP: &http.Client{Timeout: timeout}, Auth: auth, UserAgent: "sentinel-ingest/1"}
}

// Validate checks the auth block is complete.
func (a Auth) Validate() error {
	switch a.Kind {
	case "", AuthNone:
		return nil
	case AuthBasic:
		if a.Username == "" || a.Password == "" {
			return errors.New("basic auth needs username and the password secret")
		}
	case AuthBearer:
		if a.Token == "" {
			return errors.New("bearer auth needs the token secret")
		}
	case AuthHeader:
		if a.HeaderName == "" || a.Token == "" {
			return errors.New("header auth needs headerName and the token secret")
		}
	case AuthOAuth2:
		if a.TokenURL == "" || a.ClientID == "" || a.ClientSecret == "" {
			return errors.New("oauth2 client credentials need tokenUrl and the clientId/clientSecret secrets")
		}
	default:
		return fmt.Errorf("unknown auth kind %q", a.Kind)
	}
	return nil
}

// Get performs one GET request.
func (c *Client) Get(ctx context.Context, rawURL string) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	return c.Do(req)
}

// Do performs a prepared request (any method, headers and body) with the
// user agent and authentication applied, reading a bounded body. The
// request's context drives the token fetch for OAuth2.
func (c *Client) Do(req *http.Request) (*Response, error) {
	if c.UserAgent != "" && req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if err := c.authorize(req.Context(), req); err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http %s: %w", strings.ToLower(req.Method), err)
	}
	defer resp.Body.Close()
	max := c.MaxBody
	if max <= 0 {
		max = 32 << 20
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, max))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: body}, nil
}

// StatusError is returned by GetOK for non-2xx responses.
type StatusError struct {
	Status int
	Body   string
	Header http.Header
}

func (e *StatusError) Error() string { return fmt.Sprintf("remote returned %d: %s", e.Status, e.Body) }

// GetOK performs one GET request and fails on a non-2xx status.
func (c *Client) GetOK(ctx context.Context, rawURL string) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	return c.DoOK(req)
}

// DoOK performs a prepared request and fails on a non-2xx status.
func (c *Client) DoOK(req *http.Request) (*Response, error) {
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.Status < 200 || resp.Status > 299 {
		body := string(resp.Body)
		if len(body) > 512 {
			body = body[:512]
		}
		if resp.Status == http.StatusUnauthorized {
			c.mu.Lock()
			c.accessToken = "" // force a token refresh next time
			c.mu.Unlock()
		}
		return nil, &StatusError{Status: resp.Status, Body: strings.TrimSpace(body), Header: resp.Header}
	}
	return resp, nil
}

func (c *Client) authorize(ctx context.Context, req *http.Request) error {
	switch c.Auth.Kind {
	case "", AuthNone:
	case AuthBasic:
		req.SetBasicAuth(c.Auth.Username, c.Auth.Password)
	case AuthBearer:
		req.Header.Set("Authorization", "Bearer "+c.Auth.Token)
	case AuthHeader:
		req.Header.Set(c.Auth.HeaderName, c.Auth.Token)
	case AuthOAuth2:
		tok, err := c.accessTokenFor(ctx)
		if err != nil {
			return fmt.Errorf("oauth2 token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	default:
		return fmt.Errorf("unknown auth kind %q", c.Auth.Kind)
	}
	return nil
}

// accessTokenFor returns a cached client-credentials token, refreshing it
// 30 s before expiry.
func (c *Client) accessTokenFor(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.accessToken != "" && time.Now().Before(c.tokenExpiry.Add(-30*time.Second)) {
		return c.accessToken, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {c.Auth.ClientID}, "client_secret": {c.Auth.ClientSecret}}
	if c.Auth.Scopes != "" {
		form.Set("scope", c.Auth.Scopes)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Auth.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return "", errors.New("token response has no access_token")
	}
	if tr.ExpiresIn <= 0 {
		tr.ExpiresIn = 300
	}
	c.accessToken = tr.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return c.accessToken, nil
}
