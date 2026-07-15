package handler

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/PipeOpsHQ/pipehook/internal/store"
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
			// An unconfigured admin panel must not silently become public.
			if username == "" || password == "" {
				http.Error(w, "admin authentication is not configured", http.StatusServiceUnavailable)
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

func (h *Handler) APIAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.APIKey == "" {
			http.Error(w, "API access is not configured", http.StatusServiceUnavailable)
			return
		}
		provided := strings.TrimSpace(r.Header.Get("X-API-Key"))
		if auth := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			provided = strings.TrimSpace(auth[7:])
		}
		if subtle.ConstantTimeCompare([]byte(provided), []byte(h.APIKey)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !h.allowAPIRequest() {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) allowAPIRequest() bool {
	h.apiRateMu.Lock()
	defer h.apiRateMu.Unlock()
	now := time.Now()
	if h.apiRateWindow.IsZero() || now.Sub(h.apiRateWindow) >= time.Minute {
		h.apiRateWindow = now
		h.apiRateCount = 0
	}
	if h.apiRateCount >= 300 {
		return false
	}
	h.apiRateCount++
	return true
}

func browserIDFromRequest(r *http.Request) string {
	cookie, err := r.Cookie(browserIDCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (h *Handler) canAccessEndpoint(r *http.Request, endpoint *store.Endpoint) bool {
	return h.IsAdminAuthenticated(r) || (endpoint.CreatorID != "" && endpoint.CreatorID == browserIDFromRequest(r))
}

func (h *Handler) requireEndpointAccess(w http.ResponseWriter, r *http.Request, endpointID string) (*store.Endpoint, bool) {
	endpoint, err := h.Store.GetEndpoint(r.Context(), endpointID)
	if err != nil {
		http.Error(w, "endpoint not found", http.StatusNotFound)
		return nil, false
	}
	if !h.canAccessEndpoint(r, endpoint) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return nil, false
	}
	return endpoint, true
}

func (h *Handler) requireRequestAccess(w http.ResponseWriter, r *http.Request, requestID int64) (*store.Request, bool) {
	request, err := h.Store.GetRequest(r.Context(), requestID)
	if err != nil {
		http.Error(w, "request not found", http.StatusNotFound)
		return nil, false
	}
	if _, ok := h.requireEndpointAccess(w, r, request.EndpointID); !ok {
		return nil, false
	}
	return request, true
}
