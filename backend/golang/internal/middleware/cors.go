// Pairline - Open Source Video Chat and Matchmaking
// Copyright (C) 2026 Albert Blasczykowski
// Aless Microsystems
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

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
