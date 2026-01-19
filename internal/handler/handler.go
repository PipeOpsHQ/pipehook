package handler

import (
	"html/template"
	"sync"

	"github.com/nitrocode/webhook/internal/store"
	"github.com/nitrocode/webhook/ui"
)

var (
	homeTemplate      = template.Must(template.ParseFS(ui.FS, "templates/layout.html", "templates/home.html"))
	dashboardTemplate = template.Must(template.ParseFS(ui.FS, "templates/layout.html", "templates/dashboard.html"))
	detailTemplate    = template.Must(template.ParseFS(ui.FS, "templates/request-detail.html"))
)

type Handler struct {
	Store     store.Store
	clients   map[string][]chan *store.Request // endpointID -> channels
	clientsMu sync.RWMutex
}

func NewHandler(s store.Store) *Handler {
	return &Handler{
		Store:   s,
		clients: make(map[string][]chan *store.Request),
	}
}

func (h *Handler) Broadcast(endpointID string, req *store.Request) {
	h.clientsMu.RLock()
	defer h.clientsMu.RUnlock()

	for _, ch := range h.clients[endpointID] {
		select {
		case ch <- req:
		default:
			// Client slow or disconnected, skip
		}
	}
}
