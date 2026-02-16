package handler

import (
	"crypto/subtle"
	"log"
	"net/http"
)

// IsAdminAuthenticated checks if the current request has valid admin credentials
// Returns true if admin credentials are configured and the request has valid credentials
// Returns false if admin credentials are not configured or request has invalid/missing credentials
func (h *Handler) IsAdminAuthenticated(r *http.Request) bool {
	// If no credentials are configured, admin is not authenticated
	if h.AdminUsername == "" || h.AdminPassword == "" {
		return false
	}

	// Get credentials from request
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}

	// Use constant-time comparison to prevent timing attacks on credential values
	validUser := subtle.ConstantTimeCompare([]byte(user), []byte(h.AdminUsername)) == 1
	validPass := subtle.ConstantTimeCompare([]byte(pass), []byte(h.AdminPassword)) == 1

	return validUser && validPass
}

// BasicAuthMiddleware creates a middleware that protects routes with HTTP Basic Authentication
// If username or password is empty, authentication is disabled (for backward compatibility)
func BasicAuthMiddleware(username, password string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// If no credentials are configured, skip authentication
			if username == "" || password == "" {
				log.Printf("WARNING: Admin page authentication is disabled. Set ADMIN_USERNAME and ADMIN_PASSWORD to enable protection.")
				next.ServeHTTP(w, r)
				return
			}

			// Get credentials from request
			user, pass, ok := r.BasicAuth()

			// Perform authentication check
			// Note: We check ok separately to maintain clear error handling,
			// but the timing difference between "no auth" and "wrong auth" is acceptable
			// as it doesn't leak credential information
			if !ok {
				w.Header().Set("WWW-Authenticate", `Basic realm="Admin Access"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Use constant-time comparison to prevent timing attacks on credential values
			validUser := subtle.ConstantTimeCompare([]byte(user), []byte(username)) == 1
			validPass := subtle.ConstantTimeCompare([]byte(pass), []byte(password)) == 1

			if !validUser || !validPass {
				w.Header().Set("WWW-Authenticate", `Basic realm="Admin Access"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
