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

	// Log request details BEFORE reading body
	contentLength := r.ContentLength
	contentType := r.Header.Get("Content-Type")
	transferEncoding := r.Header.Get("Transfer-Encoding")
	queryParams := r.URL.RawQuery

	log.Printf("=== REQUEST DEBUG ===")
	log.Printf("Method: %s, Path: %s, RemoteAddr: %s", r.Method, r.URL.Path, r.RemoteAddr)
	log.Printf("Content-Length header: %d", contentLength)
	log.Printf("Content-Type: %s", contentType)
	log.Printf("Transfer-Encoding: %s", transferEncoding)
	log.Printf("Query params: %s", queryParams)
	log.Printf("All headers: %+v", r.Header)

	// Read body - even if Content-Length is 0, we should still try to read
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading body for %s %s: %v", r.Method, r.URL.Path, err)
		http.Error(w, "failed to read body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	actualBodyLen := len(body)
	log.Printf("=== BODY DEBUG ===")
	log.Printf("Content-Length header: %d, Actual bytes read: %d", contentLength, actualBodyLen)
	log.Printf("Transfer-Encoding: %s", transferEncoding)

	if actualBodyLen > 0 {
		previewLen := actualBodyLen
		if previewLen > 500 {
			previewLen = 500
		}
		log.Printf("Body preview (first %d bytes): %q", previewLen, string(body[:previewLen]))
	} else if contentLength > 0 {
		// Content-Length says there should be a body, but we got nothing
		log.Printf("⚠️  WARNING: Content-Length=%d but captured 0 bytes! Body may have been consumed by middleware or proxy.", contentLength)
		log.Printf("⚠️  This suggests a proxy/load balancer may be stripping the body before it reaches the application.")
	} else if transferEncoding == "chunked" {
		// Chunked encoding might not have Content-Length
		log.Printf("Transfer-Encoding is chunked but body is empty - this is unusual")
	} else {
		// Content-Length is 0, so empty body is expected
		log.Printf("Empty body (Content-Length=0, this is expected)")
	}

	// Check if body might be in query parameters (some proxies do this)
	if queryParams != "" && actualBodyLen == 0 {
		log.Printf("⚠️  NOTE: Query params present but body is empty: %s", queryParams)
	}

	// For form-urlencoded with empty body, check if it's in query params
	// (some systems send form data as query params when body is empty)
	if contentType == "application/x-www-form-urlencoded" && actualBodyLen == 0 && queryParams != "" {
		log.Printf("⚠️  Form-urlencoded with empty body but query params exist - body might be in query string")
		body = []byte(queryParams)
		actualBodyLen = len(body)
		log.Printf("⚠️  Using query params as body: %d bytes", actualBodyLen)
	}
	log.Printf("==================")

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
