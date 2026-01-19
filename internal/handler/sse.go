package handler

import (
	"bytes"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/PipeOpsHQ/pipehook/internal/store"
)

func (h *Handler) SSE(w http.ResponseWriter, r *http.Request) {
	endpointID := chi.URLParam(r, "endpointID")
	if endpointID == "" {
		http.Error(w, "missing endpoint ID", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Flush the headers to establish the connection
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	ch := make(chan *store.Request, 10)
	h.clientsMu.Lock()
	h.clients[endpointID] = append(h.clients[endpointID], ch)
	h.clientsMu.Unlock()

	defer func() {
		h.clientsMu.Lock()
		clients := h.clients[endpointID]
		for i, c := range clients {
			if c == ch {
				h.clients[endpointID] = append(clients[:i], clients[i+1:]...)
				break
			}
		}
		h.clientsMu.Unlock()
		close(ch)
	}()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case req, ok := <-ch:
			if !ok {
				return
			}
			var buf bytes.Buffer
			err := dashboardTemplate.ExecuteTemplate(&buf, "request-item", req)
			if err != nil {
				fmt.Fprintf(w, "event: error\ndata: %v\n\n", err)
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
				continue
			}
			// Format SSE data properly - each line needs "data: " prefix
			htmlData := buf.Bytes()
			lines := bytes.Split(htmlData, []byte("\n"))
			fmt.Fprintf(w, "event: new-request\n")
			for _, line := range lines {
				if len(line) > 0 {
					fmt.Fprintf(w, "data: %s\n", line)
				}
			}
			fmt.Fprintf(w, "\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		case <-ticker.C:
			// Heartbeat to keep connection alive
			fmt.Fprintf(w, ": keepalive\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}
