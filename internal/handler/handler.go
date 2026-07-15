package handler

import (
	"bytes"
	"html/template"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/PipeOpsHQ/pipehook/internal/store"
	"github.com/PipeOpsHQ/pipehook/ui"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const browserIDCookieName = "pipehook_browser_id"

// BaseTemplateData contains common data for all templates
type BaseTemplateData struct {
	IsAdmin bool
}

// Template functions
var funcMap = template.FuncMap{
	"sub": func(a, b int) int { return a - b },
	"add": func(a, b int) int { return a + b },
}

var (
	homeTemplate      = template.Must(template.New("").Funcs(funcMap).ParseFS(ui.FS, "templates/layout.html", "templates/home.html"))
	dashboardTemplate = template.Must(template.New("").Funcs(funcMap).ParseFS(ui.FS, "templates/layout.html", "templates/dashboard.html", "templates/request-detail.html"))
	detailTemplate    = template.Must(template.New("").Funcs(funcMap).ParseFS(ui.FS, "templates/request-detail.html"))
	adminTemplate     = template.Must(template.New("").Funcs(funcMap).ParseFS(ui.FS, "templates/layout.html", "templates/admin.html"))

	upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true
			}
			return origin == requestScheme(r)+"://"+r.Host
		},
	}
)

type Handler struct {
	Store               store.Store
	clients             map[string][]*websocket.Conn // endpointID -> WebSocket connections
	clientsMu           sync.RWMutex
	AdminUsername       string
	AdminPassword       string
	APIKey              string
	MaxWebhookBodyBytes int64
	AllowPrivateForward bool
	ForwardClient       *http.Client
	apiRateMu           sync.Mutex
	apiRateWindow       time.Time
	apiRateCount        int
}

func NewHandler(s store.Store) *Handler {
	return &Handler{
		Store:               s,
		clients:             make(map[string][]*websocket.Conn),
		MaxWebhookBodyBytes: 2 * 1024 * 1024, // 2MB default
		ForwardClient:       newForwardClient(false),
	}
}

func (h *Handler) SetAllowPrivateForwarding(allow bool) {
	h.AllowPrivateForward = allow
	h.ForwardClient = newForwardClient(allow)
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
	h.clientsMu.Lock()
	defer h.clientsMu.Unlock()
	clients := h.clients[endpointID]
	if len(clients) == 0 {
		delete(h.clients, endpointID)
		return
	}

	var buf bytes.Buffer
	err := dashboardTemplate.ExecuteTemplate(&buf, "request-item", req)
	if err != nil {
		log.Printf("Broadcast template error: %v", err)
		return
	}

	for i := len(clients) - 1; i >= 0; i-- {
		conn := clients[i]
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := conn.WriteJSON(map[string]interface{}{
			"type":    "new-request",
			"payload": buf.String(),
		}); err != nil {
			log.Printf("WebSocket send error, removing client: %v", err)
			// Remove disconnected client
			clients = append(clients[:i], clients[i+1:]...)
			conn.Close()
		}
	}
	if len(clients) == 0 {
		delete(h.clients, endpointID)
	} else {
		h.clients[endpointID] = clients
	}
}

func (h *Handler) closeEndpointConnections(endpointID string) {
	h.clientsMu.Lock()
	defer h.clientsMu.Unlock()

	clients := h.clients[endpointID]
	for _, conn := range clients {
		conn.Close()
	}
	delete(h.clients, endpointID)
}
