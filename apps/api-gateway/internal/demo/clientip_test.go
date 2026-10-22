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
