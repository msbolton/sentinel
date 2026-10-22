package demo

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIPTakesRightmostForwarded(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:4000"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	if got := ClientIP(r); got != "5.6.7.8" {
		t.Fatalf("ClientIP = %q, want 5.6.7.8", got)
	}
}

func TestClientIPFallsBackToRemoteAddr(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "9.9.9.9:1234"
	if got := ClientIP(r); got != "9.9.9.9" {
		t.Fatalf("ClientIP = %q, want 9.9.9.9", got)
	}
}

func TestClientIPEmptyRightmostFallsBack(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "9.9.9.9:1234"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, ")
	if got := ClientIP(r); got != "9.9.9.9" {
		t.Fatalf("ClientIP = %q, want 9.9.9.9", got)
	}
}

func TestClientIPRemoteAddrWithoutPort(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "9.9.9.9"
	if got := ClientIP(r); got != "9.9.9.9" {
		t.Fatalf("ClientIP = %q, want 9.9.9.9", got)
	}
}

func TestClientIPUsesLastForwardedHeaderLine(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:4000"
	// A client-sent line first, then the line the proxy appended.
	r.Header.Add("X-Forwarded-For", "1.1.1.1, 2.2.2.2")
	r.Header.Add("X-Forwarded-For", "3.3.3.3, 4.4.4.4")
	if got := ClientIP(r); got != "4.4.4.4" {
		t.Fatalf("ClientIP = %q, want 4.4.4.4", got)
	}
}

func TestClientIPSkipsEmptyTrailingHeaderLine(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:4000"
	r.Header.Add("X-Forwarded-For", "1.1.1.1, 4.4.4.4")
	r.Header.Add("X-Forwarded-For", "  ")
	if got := ClientIP(r); got != "4.4.4.4" {
		t.Fatalf("ClientIP = %q, want 4.4.4.4", got)
	}
}

func TestAddressKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"203.0.113.7", "203.0.113.7"},
		{"::ffff:203.0.113.7", "203.0.113.7"},
		{"2001:db8:1:2::1", "2001:db8:1:2::/64"},
		{"2001:db8:1:2:ffff:ffff:ffff:ffff", "2001:db8:1:2::/64"},
		{"2001:db8:1:3::1", "2001:db8:1:3::/64"},
		{"fe80::1%eth0", "fe80::/64"},
		{"not an ip", "not an ip"},
		{"", ""},
	}
	for _, c := range cases {
		if got := AddressKey(c.in); got != c.want {
			t.Errorf("AddressKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
