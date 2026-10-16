// Package proxy reverse-proxies gateway routes to the Go domain services,
// propagating the authenticated identity as x-sentinel-* headers.
package proxy

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"go.uber.org/zap"

	commonhttp "github.com/sentinel/go-common/httputil"
	"github.com/sentinel/go-common/middleware"

	"github.com/sentinel/api-gateway/internal/auth"
)

// Options configures one upstream proxy.
type Options struct {
	// Name appears in logs and 503 messages, e.g. "entity-service".
	Name string
	// Target is the upstream base URL, e.g. http://entity-service:3001.
	Target string
	// RewritePath optionally transforms the outbound path.
	RewritePath func(path string) string
	// ServiceToken, when set, is sent as X-Sentinel-Service-Token so the
	// upstream can tell the gateway from a stray caller.
	ServiceToken string
	Logger       *zap.Logger
}

var propagatedHeaders = []string{
	middleware.HeaderUserID,
	middleware.HeaderUserRoles,
	middleware.HeaderClassification,
}

// New builds a reverse proxy handler for one upstream.
func New(o Options) (http.Handler, error) {
	target, err := url.Parse(o.Target)
	if err != nil || target.Host == "" {
		return nil, fmt.Errorf("proxy %s: invalid target %q", o.Name, o.Target)
	}
	logger := o.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.SetXForwarded()
			if o.RewritePath != nil {
				pr.Out.URL.Path = o.RewritePath(pr.Out.URL.Path)
				pr.Out.URL.RawPath = ""
			}
			// Never trust identity or service headers supplied by the caller.
			for _, h := range propagatedHeaders {
				pr.Out.Header.Del(h)
			}
			pr.Out.Header.Del(middleware.HeaderServiceToken)
			if u, ok := auth.UserFrom(pr.In.Context()); ok {
				pr.Out.Header.Set(middleware.HeaderUserID, u.UserID)
				pr.Out.Header.Set(middleware.HeaderUserRoles, strings.Join(u.Roles, ","))
				pr.Out.Header.Set(middleware.HeaderClassification, u.ClassificationLevel)
			}
			if o.ServiceToken != "" {
				pr.Out.Header.Set(middleware.HeaderServiceToken, o.ServiceToken)
			}
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Warn("upstream unavailable",
				zap.String("upstream", o.Name),
				zap.String("path", r.URL.Path),
				zap.Error(err),
			)
			commonhttp.Error(w, http.StatusServiceUnavailable, o.Name+" unavailable")
		},
	}, nil
}

// StripPrefix returns a RewritePath that removes prefix from the outbound path.
func StripPrefix(prefix string) func(string) string {
	return func(p string) string {
		if strings.HasPrefix(p, prefix) {
			p = p[len(prefix):]
		}
		if p == "" {
			return "/"
		}
		return p
	}
}
