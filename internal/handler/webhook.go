package handler

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
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

	body, wasTruncated, err := readRequestBodyWithLimit(r.Body, maxBodyBytes)
	if err != nil {
		log.Printf("Error reading body for %s %s: %v", r.Method, r.URL.Path, err)
		http.Error(w, "failed to read body", http.StatusInternalServerError)
		return
	}

	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if len(body) == 0 && strings.HasPrefix(contentType, "application/x-www-form-urlencoded") && r.URL.RawQuery != "" {
		body = []byte(r.URL.RawQuery)
	}

	// Capture all headers
	headersToStore := make(map[string][]string, len(r.Header)+2)
	for k, values := range r.Header {
		cloned := make([]string, len(values))
		copy(cloned, values)
		headersToStore[k] = cloned
	}
	if wasTruncated {
		headersToStore["X-Pipehook-Body-Truncated"] = []string{"true"}
		headersToStore["X-Pipehook-Body-Limit"] = []string{strconv.FormatInt(maxBodyBytes, 10)}
	}
	headersJSON, _ := json.Marshal(headersToStore)

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
	if wasTruncated {
		w.Header().Set("X-Pipehook-Body-Truncated", "true")
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func readRequestBodyWithLimit(body io.ReadCloser, maxBytes int64) ([]byte, bool, error) {
	defer body.Close()
	if maxBytes <= 0 {
		data, err := io.ReadAll(body)
		return data, false, err
	}

	limited := &io.LimitedReader{R: body, N: maxBytes + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, false, err
	}

	if int64(len(data)) > maxBytes {
		return data[:int(maxBytes)], true, nil
	}

	return data, false, nil
}
