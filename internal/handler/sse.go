package handler

import (
	"bytes"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/nitrocode/webhook/internal/store"
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
	}()

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
				w.(http.Flusher).Flush()
				continue
			}
			// SSE data cannot have newlines unless prefixed with "data: "
			data := bytes.ReplaceAll(buf.Bytes(), []byte("\n"), []byte(""))
			fmt.Fprintf(w, "event: new-request\ndata: %s\n\n", data)
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
			return
		}
	}
}
