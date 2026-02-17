package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/PipeOpsHQ/pipehook/internal/store"
	"github.com/go-chi/chi/v5"
)

func (h *Handler) CaptureWebhook(w http.ResponseWriter, r *http.Request) {
	endpointID := chi.URLParam(r, "endpointID")
	if endpointID == "" {
		http.Error(w, "missing endpoint ID", http.StatusBadRequest)
		return
	}

	// Check if endpoint exists
	_, err := h.Store.GetEndpoint(r.Context(), endpointID)
	if err != nil {
		http.Error(w, "endpoint not found", http.StatusNotFound)
		return
	}

	maxBodyBytes := h.MaxWebhookBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = 2 * 1024 * 1024
	}
	limitedBody := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	body, err := io.ReadAll(limitedBody)
	_ = limitedBody.Close()
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		log.Printf("Error reading body for %s %s: %v", r.Method, r.URL.Path, err)
		http.Error(w, "failed to read body", http.StatusInternalServerError)
		return
	}

	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if len(body) == 0 && strings.HasPrefix(contentType, "application/x-www-form-urlencoded") && r.URL.RawQuery != "" {
		body = []byte(r.URL.RawQuery)
	}

	// Capture all headers
	headersJSON, _ := json.Marshal(r.Header)

	req := &store.Request{
		EndpointID: endpointID,
		Method:     r.Method,
		Path:       r.URL.Path,
		RemoteAddr: r.RemoteAddr,
		Headers:    string(headersJSON),
		Body:       body,
		StatusCode: http.StatusOK,
	}

	if err := h.Store.SaveRequest(r.Context(), req); err != nil {
		log.Printf("Error saving request: %v", err)
		http.Error(w, "failed to save request", http.StatusInternalServerError)
		return
	}

	// Broadcast only list-relevant fields to avoid carrying large bodies through the websocket path.
	h.Broadcast(endpointID, &store.Request{
		ID:         req.ID,
		EndpointID: req.EndpointID,
		Method:     req.Method,
		Path:       req.Path,
		RemoteAddr: req.RemoteAddr,
		CreatedAt:  req.CreatedAt,
	})

	// Return success response
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}
