package demo

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP returns the visitor's address. The reverse proxy appends the
// address it saw to X-Forwarded-For, so the rightmost entry is the only one a
// client cannot forge; earlier entries are whatever the client sent. With no
// usable header the connection's remote address is used, minus its port.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
			return last
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}
