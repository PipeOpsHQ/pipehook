package handler

import (
	"crypto/subtle"
	"net/http"
)

// BasicAuthMiddleware creates a middleware that protects routes with HTTP Basic Authentication
// If username or password is empty, authentication is disabled (for backward compatibility)
func BasicAuthMiddleware(username, password string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// If no credentials are configured, skip authentication
			if username == "" || password == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Get credentials from request
			user, pass, ok := r.BasicAuth()

			// Use constant-time comparison to prevent timing attacks
			validUser := subtle.ConstantTimeCompare([]byte(user), []byte(username)) == 1
			validPass := subtle.ConstantTimeCompare([]byte(pass), []byte(password)) == 1

			if !ok || !validUser || !validPass {
				w.Header().Set("WWW-Authenticate", `Basic realm="Admin Access"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
