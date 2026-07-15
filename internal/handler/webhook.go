package handler

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/PipeOpsHQ/pipehook/internal/store"
	"github.com/go-chi/chi/v5"
)

func (h *Handler) CaptureWebhook(w http.ResponseWriter, r *http.Request) {
	endpointID := chi.URLParam(r, "endpointID")
	if endpointID == "" {
		http.Error(w, "missing endpoint ID", http.StatusBadRequest)
		return
	}
	endpoint, err := h.Store.GetEndpoint(r.Context(), endpointID)
	if err != nil || endpoint.ExpiresAt.Before(time.Now()) {
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

	headersToStore := make(map[string][]string, len(r.Header)+2)
	for key, values := range r.Header {
		headersToStore[key] = append([]string(nil), values...)
	}
	if wasTruncated {
		headersToStore["X-Pipehook-Body-Truncated"] = []string{"true"}
		headersToStore["X-Pipehook-Body-Limit"] = []string{strconv.FormatInt(maxBodyBytes, 10)}
	}
	headersJSON, _ := json.Marshal(headersToStore)

	captured := &store.Request{
		EndpointID: endpointID, Method: r.Method, Path: r.URL.Path, QueryString: r.URL.RawQuery,
		Host: r.Host, Scheme: requestScheme(r), RemoteAddr: r.RemoteAddr, Headers: string(headersJSON),
		Body: body, ContentLength: r.ContentLength, BodyTruncated: wasTruncated, StatusCode: responseStatus(endpoint),
	}
	if err := h.Store.SaveRequest(r.Context(), captured); err != nil {
		log.Printf("Error saving request: %v", err)
		http.Error(w, "failed to save request", http.StatusInternalServerError)
		return
	}
	if err := h.Store.TrimRequests(r.Context(), endpointID, endpoint.RequestLimit); err != nil {
		log.Printf("Error enforcing request retention for %s: %v", endpointID, err)
	}

	h.Broadcast(endpointID, &store.Request{
		ID: captured.ID, EndpointID: captured.EndpointID, Method: captured.Method, Path: captured.Path,
		QueryString: captured.QueryString, RemoteAddr: captured.RemoteAddr, CreatedAt: captured.CreatedAt,
	})
	if endpoint.ForwardURL != "" {
		if err := h.forwardRequest(r.Context(), endpoint, captured); err != nil {
			log.Printf("Forwarding request %d failed: %v", captured.ID, err)
		}
	}

	if endpoint.EnableCORS {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS, HEAD")
	}
	if endpoint.DefaultContentType != "" {
		w.Header().Set("Content-Type", endpoint.DefaultContentType)
	}
	if wasTruncated {
		w.Header().Set("X-Pipehook-Body-Truncated", "true")
	}
	if endpoint.ResponseDelayMS > 0 {
		select {
		case <-time.After(time.Duration(endpoint.ResponseDelayMS) * time.Millisecond):
		case <-r.Context().Done():
			return
		}
	}
	w.WriteHeader(responseStatus(endpoint))
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(endpoint.DefaultBody))
	}
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
