package middleware

import (
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

func CORS(allowedOrigins string) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Set CORS headers
			if allowedOrigins == "*" {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				// IMPORTANT: Do NOT set Allow-Credentials with wildcard origin
			} else if origin != "" {
				// Check if origin is in the allowed list
				matched := false
				allowedList := strings.Split(allowedOrigins, ",")
				for _, allowed := range allowedList {
					allowed = strings.TrimSpace(allowed)
					if origin == allowed {
						w.Header().Set("Access-Control-Allow-Origin", origin)
						w.Header().Set("Access-Control-Allow-Credentials", "true")
						matched = true
						break
					}
				}
				// If no match, omit Access-Control-Allow-Origin entirely.
				// Setting it to "" is invalid per the CORS spec and
				// results in undefined browser behavior.
				if !matched {
					// Also block the preflight from succeeding
					if r.Method == "OPTIONS" {
						w.WriteHeader(http.StatusForbidden)
						return
					}
				}
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, x-signature, x-timestamp, x-nonce")
			w.Header().Set("Access-Control-Max-Age", "86400")

			// Handle preflight requests
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
