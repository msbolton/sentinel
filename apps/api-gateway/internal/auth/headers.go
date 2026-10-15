package auth

import "net/http"

// SecurityHeaders applies the OWASP response headers the NestJS gateway set.
// HSTS is only sent in production to avoid breaking plain-HTTP development.
func SecurityHeaders(production bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("X-XSS-Protection", "1; mode=block")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate")
			h.Set("Pragma", "no-cache")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(self), payment=()")
			if production {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}
