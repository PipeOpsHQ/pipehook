package handler

import (
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

func (h *Handler) WebSocket(w http.ResponseWriter, r *http.Request) {
	endpointID := chi.URLParam(r, "endpointID")
	if endpointID == "" {
		http.Error(w, "missing endpoint ID", http.StatusBadRequest)
		return
	}
	if _, ok := h.requireEndpointAccess(w, r, endpointID); !ok {
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade error: %v", err)
		return
	}
	// We don't consume application payloads from clients, so keep inbound frame size tiny.
	conn.SetReadLimit(1024)
	_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	})
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					_ = conn.Close()
					return
				}
			case <-done:
				return
			}
		}
	}()

	// Add connection to clients
	h.clientsMu.Lock()
	h.clients[endpointID] = append(h.clients[endpointID], conn)
	h.clientsMu.Unlock()

	// Remove connection when done
	defer func() {
		close(done)
		h.clientsMu.Lock()
		clients := h.clients[endpointID]
		for i, c := range clients {
			if c == conn {
				clients = append(clients[:i], clients[i+1:]...)
				break
			}
		}
		if len(clients) == 0 {
			delete(h.clients, endpointID)
		} else {
			h.clients[endpointID] = clients
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
