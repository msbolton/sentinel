package demo

import (
	"net"
	"net/http"
	"net/netip"
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

// AddressKey is what per-address demo limits count by: an IPv4 address as
// is, and an IPv6 one by its /64, since one household or phone is handed a
// whole /64 and can pick any address in it. Anything that is not an address
// is its own key.
func AddressKey(ip string) string {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return ip
	}
	addr = addr.Unmap().WithZone("")
	if addr.Is4() {
		return addr.String()
	}
	p, err := addr.Prefix(64)
	if err != nil {
		return ip
	}
	return p.String()
}
