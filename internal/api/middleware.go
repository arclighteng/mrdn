package api

import (
	"net/http"
	"strings"
)

// SecurityHeaders sets defensive HTTP response headers on every response.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// allowedOrigin returns true when the origin is permitted to access the API.
// Localhost on any port is allowed for local development; the production
// frontend origin is allowed by exact match.
func allowedOrigin(origin string) bool {
	if origin == "http://localhost" ||
		strings.HasPrefix(origin, "http://localhost:") {
		return true
	}
	return origin == "https://mrdn.arclighteng.com"
}

// CORSMiddleware handles cross-origin resource sharing. It reflects the
// request origin back rather than using a wildcard so that the header
// remains compatible with credentialed requests in the future.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowedOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "X-API-Key, Content-Type, Last-Event-ID")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
