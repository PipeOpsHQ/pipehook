package handler

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"io"
	"log"
	"net/http"

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

	log.Printf("Incoming %s request to %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
	log.Printf("Headers: %v", r.Header)

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading body for %s %s: %v", r.Method, r.URL.Path, err)
		http.Error(w, "failed to read body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	log.Printf("Captured %d bytes. Content-Type: %s, User-Agent: %s", len(body), r.Header.Get("Content-Type"), r.UserAgent())
	if len(body) > 0 {
		previewLen := len(body)
		if previewLen > 500 {
			previewLen = 500
		}
		log.Printf("Body preview (first 500 bytes): %s", string(body[:previewLen]))
	} else {
		log.Printf("Warning: Captured body is empty!")
	}

	// Handle compression if present
	contentEncoding := r.Header.Get("Content-Encoding")
	var decompressedBody []byte
	switch contentEncoding {
	case "gzip":
		gr, err := gzip.NewReader(bytes.NewReader(body))
		if err == nil {
			if db, err := io.ReadAll(gr); err == nil {
				decompressedBody = db
			}
			gr.Close()
		}
	case "deflate":
		zr, err := zlib.NewReader(bytes.NewReader(body))
		if err == nil {
			if db, err := io.ReadAll(zr); err == nil {
				decompressedBody = db
			}
			zr.Close()
		}
	}

	// If we successfully decompressed, use that for internal storage/display
	// but we might want to keep original body for binary integrity if replaying?
	// For now, let's store the decompressed version if it exists, otherwise the raw body.
	bodyToStore := body
	if len(decompressedBody) > 0 {
		bodyToStore = decompressedBody
	}

	// Capture all headers
	headersMap := make(map[string][]string)
	for k, v := range r.Header {
		headersMap[k] = v
	}
	headersJSON, _ := json.Marshal(headersMap)

	req := &store.Request{
		EndpointID: endpointID,
		Method:     r.Method,
		Path:       r.URL.Path,
		RemoteAddr: r.RemoteAddr,
		Headers:    string(headersJSON),
		Body:       bodyToStore,
		StatusCode: http.StatusOK,
	}

	if err := h.Store.SaveRequest(r.Context(), req); err != nil {
		log.Printf("Error saving request: %v", err)
		http.Error(w, "failed to save request", http.StatusInternalServerError)
		return
	}

	h.Broadcast(endpointID, req)

	// Return success response
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}
