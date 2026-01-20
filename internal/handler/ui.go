package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
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
	// Panic recovery
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("PANIC in Dashboard handler: %v", rec)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
	}()

	endpointID := chi.URLParam(r, "endpointID")
	if endpointID == "" {
		http.Error(w, "missing endpoint ID", http.StatusBadRequest)
		return
	}

	endpoint, err := h.Store.GetEndpoint(r.Context(), endpointID)
	if err != nil {
		log.Printf("Error getting endpoint %s: %v", endpointID, err)
		http.Error(w, "endpoint not found", http.StatusNotFound)
		return
	}

	requests, err := h.Store.GetRequests(r.Context(), endpointID, 50)
	if err != nil {
		log.Printf("Error getting requests for endpoint %s: %v", endpointID, err)
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
		// Initialize headers map to avoid nil pointer issues
		headers = make(map[string][]string)

		// Safely unmarshal headers JSON
		if requests[0].Headers != "" {
			if err := json.Unmarshal([]byte(requests[0].Headers), &headers); err != nil {
				log.Printf("Warning: Failed to unmarshal headers for request %d: %v", requests[0].ID, err)
				// Continue with empty headers map
				headers = make(map[string][]string)
			}
		}

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
				// Allow common control characters: 9 (tab), 10 (LF), 13 (CR), 27 (ESC/ANSI)
				if b < 32 && b != 9 && b != 10 && b != 13 && b != 27 {
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

		// #region agent log
		func() {
			f, _ := os.OpenFile("/Users/nitrocode/webhook/.cursor/debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if f != nil {
				defer f.Close()
				json.NewEncoder(f).Encode(map[string]interface{}{
					"sessionId": "debug-session", "runId": "run1", "hypothesisId": "A",
					"location": "ui.go:159", "message": "FirstRequest data before template",
					"data": map[string]interface{}{
						"requestID": requests[0].ID, "endpointID": requests[0].EndpointID,
						"idHasQuotes":         strings.Contains(fmt.Sprintf("%d", requests[0].ID), "\""),
						"endpointIDHasQuotes": strings.Contains(requests[0].EndpointID, "\""),
						"idHasSpecialChars":   len(fmt.Sprintf("%d", requests[0].ID)) != len(strings.TrimSpace(fmt.Sprintf("%d", requests[0].ID))),
					},
					"timestamp": time.Now().UnixMilli(),
				})
			}
		}()
		// #endregion
	}

	// Get host, with fallback
	host := r.Host
	if host == "" {
		host = r.Header.Get("Host")
	}
	if host == "" {
		host = "webhook.pipeops.app" // fallback
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
		Host:           host,
	}

	// #region agent log
	func() {
		f, _ := os.OpenFile("/Users/nitrocode/webhook/.cursor/debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if f != nil {
			defer f.Close()
			firstReqID := int64(0)
			firstReqEndpointID := ""
			if firstRequest != nil && firstRequest.Request != nil {
				firstReqID = firstRequest.ID
				firstReqEndpointID = firstRequest.EndpointID
			}
			json.NewEncoder(f).Encode(map[string]interface{}{
				"sessionId": "debug-session", "runId": "run1", "hypothesisId": "B",
				"location": "ui.go:192", "message": "Before template execution",
				"data": map[string]interface{}{
					"firstRequestID": firstReqID, "firstRequestEndpointID": firstReqEndpointID,
					"endpointID": endpointID, "host": host,
					"idStr":                    fmt.Sprintf("%d", firstReqID),
					"idContainsQuotes":         strings.Contains(fmt.Sprintf("%d", firstReqID), "\""),
					"endpointIDContainsQuotes": strings.Contains(firstReqEndpointID, "\""),
				},
				"timestamp": time.Now().UnixMilli(),
			})
		}
	}()
	// #endregion

	if err := dashboardTemplate.ExecuteTemplate(w, "layout", data); err != nil {
		// #region agent log
		func() {
			f, _ := os.OpenFile("/Users/nitrocode/webhook/.cursor/debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if f != nil {
				defer f.Close()
				json.NewEncoder(f).Encode(map[string]interface{}{
					"sessionId": "debug-session", "runId": "run1", "hypothesisId": "C",
					"location": "ui.go:212", "message": "Template execution error",
					"data": map[string]interface{}{
						"error": err.Error(), "errorString": fmt.Sprintf("%v", err),
					},
					"timestamp": time.Now().UnixMilli(),
				})
			}
		}()
		// #endregion
		log.Printf("template execution error: %v", err)
		log.Printf("Template data: Endpoint=%v, Requests=%d, FirstRequest=%v, Host=%s",
			endpoint != nil, len(requests), firstRequest != nil, host)
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
	// Initialize headers map to avoid nil pointer issues
	headers = make(map[string][]string)

	// Safely unmarshal headers JSON
	if req.Headers != "" {
		if err := json.Unmarshal([]byte(req.Headers), &headers); err != nil {
			log.Printf("Warning: Failed to unmarshal headers for request %d: %v", req.ID, err)
			// Continue with empty headers map
			headers = make(map[string][]string)
		}
	}

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
			// Allow common control characters: 9 (tab), 10 (LF), 13 (CR), 27 (ESC/ANSI)
			if b < 32 && b != 9 && b != 10 && b != 13 && b != 27 {
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
