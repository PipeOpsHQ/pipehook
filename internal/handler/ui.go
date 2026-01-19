package handler

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PipeOpsHQ/pipehook/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	endpoints, err := h.Store.ListEndpoints(r.Context(), 50)
	if err != nil {
		log.Printf("failed to list endpoints: %v", err)
		endpoints = []*store.Endpoint{}
	}

	data := struct {
		Endpoints []*store.Endpoint
		Host      string
	}{
		Endpoints: endpoints,
		Host:      r.Host,
	}

	if err := homeTemplate.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("template execution error: %v", err)
		http.Error(w, "failed to render page", http.StatusInternalServerError)
		return
	}
}

func (h *Handler) CreateEndpoint(w http.ResponseWriter, r *http.Request) {
	id := uuid.New().String()
	_, err := h.Store.CreateEndpoint(r.Context(), id, "", 24*time.Hour)
	if err != nil {
		http.Error(w, "failed to create endpoint", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/"+id, http.StatusSeeOther)
}

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	endpointID := chi.URLParam(r, "endpointID")
	endpoint, err := h.Store.GetEndpoint(r.Context(), endpointID)
	if err != nil {
		http.Error(w, "endpoint not found", http.StatusNotFound)
		return
	}

	requests, err := h.Store.GetRequests(r.Context(), endpointID, 50)
	if err != nil {
		http.Error(w, "failed to fetch requests", http.StatusInternalServerError)
		return
	}

	// Get other endpoints for switching
	allEndpoints, _ := h.Store.ListEndpoints(r.Context(), 20)
	otherEndpoints := []*store.Endpoint{}
	for _, e := range allEndpoints {
		if e.ID != endpointID {
			otherEndpoints = append(otherEndpoints, e)
		}
	}

	type requestDetailData struct {
		*store.Request
		HeadersMap  map[string][]string
		HeadersJSON string
		BodyString  string
		ContentType string
		IsBinary    bool
	}

	var firstRequest *requestDetailData
	if len(requests) > 0 {
		var headers map[string][]string
		json.Unmarshal([]byte(requests[0].Headers), &headers)

		// Format headers as JSON for display
		headersJSON, _ := json.MarshalIndent(headers, "", "  ")

		// Detect content type
		contentType := "text/plain"
		if ct, ok := headers["Content-Type"]; ok && len(ct) > 0 {
			contentType = ct[0]
			if idx := strings.Index(contentType, ";"); idx != -1 {
				contentType = contentType[:idx]
			}
			contentType = strings.TrimSpace(contentType)
		}

		// Check if binary (only if significant portion is non-printable)
		isBinary := false
		if len(requests[0].Body) > 0 {
			// If Content-Type suggests text/json, be extremely lenient
			isTextType := false
			ct := strings.ToLower(contentType)
			if strings.Contains(ct, "json") || strings.Contains(ct, "text") || strings.Contains(ct, "xml") || strings.Contains(ct, "html") || strings.Contains(ct, "form-urlencoded") {
				isTextType = true
			}

			nonPrintableCount := 0
			sampleSize := len(requests[0].Body)
			if sampleSize > 1000 {
				sampleSize = 1000 // Sample first 1000 bytes for performance
			}
			for i := 0; i < sampleSize; i++ {
				b := requests[0].Body[i]
				if b < 32 && b != 9 && b != 10 && b != 13 {
					nonPrintableCount++
				}
			}

			// Thresholds:
			// If text type: 50% non-printable to consider binary (very lenient)
			// If unknown: 25% non-printable
			threshold := 0.25
			if isTextType {
				threshold = 0.50
			}

			if sampleSize > 0 && float64(nonPrintableCount)/float64(sampleSize) > threshold {
				isBinary = true
			}
		}

		firstRequest = &requestDetailData{
			Request:     requests[0],
			HeadersMap:  headers,
			HeadersJSON: string(headersJSON),
			BodyString:  string(requests[0].Body),
			ContentType: contentType,
			IsBinary:    isBinary,
		}
	}

	data := struct {
		Endpoint       *store.Endpoint
		Requests       []*store.Request
		FirstRequest   *requestDetailData
		OtherEndpoints []*store.Endpoint
		Host           string
	}{
		Endpoint:       endpoint,
		Requests:       requests,
		FirstRequest:   firstRequest,
		OtherEndpoints: otherEndpoints,
		Host:           r.Host,
	}

	if err := dashboardTemplate.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("template execution error: %v", err)
		http.Error(w, "failed to render page", http.StatusInternalServerError)
		return
	}
}

func (h *Handler) RequestDetail(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "requestID")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	req, err := h.Store.GetRequest(r.Context(), id)
	if err != nil {
		http.Error(w, "request not found", http.StatusNotFound)
		return
	}

	var headers map[string][]string
	json.Unmarshal([]byte(req.Headers), &headers)

	// Format headers as JSON for display
	headersJSON, _ := json.MarshalIndent(headers, "", "  ")

	// Detect content type from headers
	contentType := "text/plain"
	if ct, ok := headers["Content-Type"]; ok && len(ct) > 0 {
		contentType = ct[0]
		// Extract just the MIME type (remove charset, etc.)
		if idx := strings.Index(contentType, ";"); idx != -1 {
			contentType = contentType[:idx]
		}
		contentType = strings.TrimSpace(contentType)
	}

	// Check if body is binary (non-printable characters)
	// Only consider it binary if a significant portion is non-printable
	isBinary := false
	if len(req.Body) > 0 {
		// If Content-Type suggests text/json, be extremely lenient
		isTextType := false
		ct := strings.ToLower(contentType)
		if strings.Contains(ct, "json") || strings.Contains(ct, "text") || strings.Contains(ct, "xml") || strings.Contains(ct, "html") || strings.Contains(ct, "form-urlencoded") {
			isTextType = true
		}

		nonPrintableCount := 0
		sampleSize := len(req.Body)
		if sampleSize > 1000 {
			sampleSize = 1000 // Sample first 1000 bytes for performance
		}
		for i := 0; i < sampleSize; i++ {
			b := req.Body[i]
			if b < 32 && b != 9 && b != 10 && b != 13 {
				nonPrintableCount++
			}
		}

		// Thresholds:
		// If text type: 50% non-printable to consider binary (very lenient)
		// If unknown: 25% non-printable
		threshold := 0.25
		if isTextType {
			threshold = 0.50
		}

		if sampleSize > 0 && float64(nonPrintableCount)/float64(sampleSize) > threshold {
			isBinary = true
		}
	}

	data := struct {
		*store.Request
		HeadersMap  map[string][]string
		HeadersJSON string
		BodyString  string
		ContentType string
		IsBinary    bool
	}{
		Request:     req,
		HeadersMap:  headers,
		HeadersJSON: string(headersJSON),
		BodyString:  string(req.Body),
		ContentType: contentType,
		IsBinary:    isBinary,
	}

	detailTemplate.ExecuteTemplate(w, "request-detail", data)
}

func (h *Handler) DeleteRequest(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "requestID")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid request ID", http.StatusBadRequest)
		return
	}

	// Verify request exists
	_, err = h.Store.GetRequest(r.Context(), id)
	if err != nil {
		http.Error(w, "request not found", http.StatusNotFound)
		return
	}

	// Delete the request
	if err := h.Store.DeleteRequest(r.Context(), id); err != nil {
		http.Error(w, "failed to delete request", http.StatusInternalServerError)
		return
	}

	// Return empty response for HTMX to handle removal
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) DeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	endpointID := chi.URLParam(r, "endpointID")
	if endpointID == "" {
		http.Error(w, "missing endpoint ID", http.StatusBadRequest)
		return
	}

	// Verify endpoint exists
	_, err := h.Store.GetEndpoint(r.Context(), endpointID)
	if err != nil {
		http.Error(w, "endpoint not found", http.StatusNotFound)
		return
	}

	// Close all WebSocket connections for this endpoint
	h.clientsMu.Lock()
	clients := h.clients[endpointID]
	for _, conn := range clients {
		conn.Close()
	}
	delete(h.clients, endpointID)
	h.clientsMu.Unlock()

	// Delete the endpoint (this will cascade delete all requests due to FOREIGN KEY)
	if err := h.Store.DeleteEndpoint(r.Context(), endpointID); err != nil {
		http.Error(w, "failed to delete endpoint", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
