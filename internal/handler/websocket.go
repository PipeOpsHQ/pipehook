package handler

import (
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

func (h *Handler) WebSocket(w http.ResponseWriter, r *http.Request) {
	endpointID := chi.URLParam(r, "endpointID")
	if endpointID == "" {
		http.Error(w, "missing endpoint ID", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade error: %v", err)
		return
	}

	// Add connection to clients
	h.clientsMu.Lock()
	h.clients[endpointID] = append(h.clients[endpointID], conn)
	h.clientsMu.Unlock()

	// Remove connection when done
	defer func() {
		h.clientsMu.Lock()
		clients := h.clients[endpointID]
		for i, c := range clients {
			if c == conn {
				h.clients[endpointID] = append(clients[:i], clients[i+1:]...)
				break
			}
		}
		h.clientsMu.Unlock()
		conn.Close()
	}()

	// Keep connection alive by reading messages (ping/pong)
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("websocket error: %v", err)
			}
			break
		}
	}
}
