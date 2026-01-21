package handler

import (
	"bytes"
	"net/http"
	"sync"
	"text/template"

	"github.com/PipeOpsHQ/pipehook/internal/store"
	"github.com/PipeOpsHQ/pipehook/ui"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const browserIDCookieName = "pipehook_browser_id"

// Template functions
var funcMap = template.FuncMap{
	"sub": func(a, b int) int { return a - b },
	"add": func(a, b int) int { return a + b },
}

var (
	homeTemplate      = template.Must(template.New("").Funcs(funcMap).ParseFS(ui.FS, "templates/layout.html", "templates/home.html"))
	dashboardTemplate = template.Must(template.New("").Funcs(funcMap).ParseFS(ui.FS, "templates/layout.html", "templates/dashboard.html", "templates/request-detail.html"))
	detailTemplate    = template.Must(template.New("").Funcs(funcMap).ParseFS(ui.FS, "templates/request-detail.html"))

	upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true // Allow all origins in development
		},
	}
)

type Handler struct {
	Store     store.Store
	clients   map[string][]*websocket.Conn // endpointID -> WebSocket connections
	clientsMu sync.RWMutex
}

func NewHandler(s store.Store) *Handler {
	return &Handler{
		Store:   s,
		clients: make(map[string][]*websocket.Conn),
	}
}

// GetBrowserID retrieves or creates a browser fingerprint ID from cookies
func (h *Handler) GetBrowserID(w http.ResponseWriter, r *http.Request) string {
	// Try to get existing browser ID from cookie
	cookie, err := r.Cookie(browserIDCookieName)
	if err == nil && cookie.Value != "" {
		return cookie.Value
	}

	// Generate new browser ID
	browserID := uuid.New().String()

	// Set cookie (expires in 1 year)
	http.SetCookie(w, &http.Cookie{
		Name:     browserIDCookieName,
		Value:    browserID,
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60, // 1 year
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})

	return browserID
}

func (h *Handler) Broadcast(endpointID string, req *store.Request) {
	h.clientsMu.RLock()
	defer h.clientsMu.RUnlock()

	var buf bytes.Buffer
	err := dashboardTemplate.ExecuteTemplate(&buf, "request-item", req)
	if err != nil {
		return
	}

	clients := h.clients[endpointID]
	for i := len(clients) - 1; i >= 0; i-- {
		conn := clients[i]
		if err := conn.WriteJSON(map[string]interface{}{
			"type":    "new-request",
			"payload": buf.String(),
		}); err != nil {
			// Remove disconnected client
			clients = append(clients[:i], clients[i+1:]...)
			conn.Close()
		}
	}
	h.clients[endpointID] = clients
}
