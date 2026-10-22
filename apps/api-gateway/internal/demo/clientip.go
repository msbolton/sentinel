package demo

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP returns the visitor's address. The reverse proxy appends the
// address it saw to X-Forwarded-For, so the rightmost entry is the only one a
// client cannot forge; earlier entries are whatever the client sent. A client
// can also send its own X-Forwarded-For line, so the proxy's entry is the last
// element of the last non-empty line. With no usable header the connection's
// remote address is used, minus its port.
func ClientIP(r *http.Request) string {
	if xff := lastNonEmpty(r.Header.Values("X-Forwarded-For")); xff != "" {
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

func lastNonEmpty(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}
