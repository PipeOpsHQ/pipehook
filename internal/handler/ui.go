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
	// Get or create browser ID for this user
	browserID := h.GetBrowserID(w, r)

	// Only list endpoints created by this browser
	endpoints, err := h.Store.ListEndpoints(r.Context(), browserID, 50)
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
	// Get or create browser ID for this user
	browserID := h.GetBrowserID(w, r)

	id := uuid.New().String()
	_, err := h.Store.CreateEndpoint(r.Context(), id, "", browserID, store.DefaultTTL)
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

	// Get browser ID for this user
	browserID := h.GetBrowserID(w, r)

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

	// Default to 100 requests, configurable via query param
	limit := 100
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 500 {
			limit = l
		}
	}

	requests, err := h.Store.GetRequests(r.Context(), endpointID, limit)
	if err != nil {
		log.Printf("Error getting requests for endpoint %s: %v", endpointID, err)
		http.Error(w, "failed to fetch requests", http.StatusInternalServerError)
		return
	}

	// Get other endpoints for switching (only this user's endpoints)
	allEndpoints, _ := h.Store.ListEndpoints(r.Context(), browserID, 20)
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
	}

	// Get host, with fallback
	host := r.Host
	if host == "" {
		host = r.Header.Get("Host")
	}
	if host == "" {
		host = "webhook.pipeops.app" // fallback
	}

	// Get total count for pagination
	totalCount, _ := h.Store.CountRequests(r.Context(), endpointID)
	hasMore := len(requests) < totalCount

	data := struct {
		Endpoint       *store.Endpoint
		Requests       []*store.Request
		FirstRequest   *requestDetailData
		OtherEndpoints []*store.Endpoint
		Host           string
		TotalCount     int
		HasMore        bool
		Limit          int
	}{
		Endpoint:       endpoint,
		Requests:       requests,
		FirstRequest:   firstRequest,
		OtherEndpoints: otherEndpoints,
		Host:           host,
		TotalCount:     totalCount,
		HasMore:        hasMore,
		Limit:          limit,
	}

	if err := dashboardTemplate.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("template execution error: %v", err)
		log.Printf("Template data: Endpoint=%v, Requests=%d, FirstRequest=%v, Host=%s",
			endpoint != nil, len(requests), firstRequest != nil, host)
		http.Error(w, "failed to render page", http.StatusInternalServerError)
		return
	}
}

func (h *Handler) LoadMoreRequests(w http.ResponseWriter, r *http.Request) {
	endpointID := chi.URLParam(r, "endpointID")
	if endpointID == "" {
		http.Error(w, "missing endpoint ID", http.StatusBadRequest)
		return
	}

	// Parse offset and limit from query params
	offset := 0
	limit := 50
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	requests, err := h.Store.GetRequestsWithOffset(r.Context(), endpointID, limit, offset)
	if err != nil {
		http.Error(w, "failed to fetch requests", http.StatusInternalServerError)
		return
	}

	// Get total count to determine if there's more
	totalCount, _ := h.Store.CountRequests(r.Context(), endpointID)
	hasMore := (offset + len(requests)) < totalCount

	// Render request items as HTML fragments
	var buf strings.Builder
	for _, req := range requests {
		var itemBuf strings.Builder
		if err := dashboardTemplate.ExecuteTemplate(&itemBuf, "request-item", req); err != nil {
			continue
		}
		buf.WriteString(itemBuf.String())
	}

	// If there's more, include the "Load More" button
	if hasMore {
		nextOffset := offset + len(requests)
		buf.WriteString(`<li id="load-more-container" class="p-4 text-center">
			<button hx-get="/` + endpointID + `/more?offset=` + strconv.Itoa(nextOffset) + `&limit=` + strconv.Itoa(limit) + `"
					hx-target="#load-more-container"
					hx-swap="outerHTML"
					class="text-xs font-bold text-brand-400 hover:text-brand-300 bg-slate-800 hover:bg-slate-700 px-4 py-2 rounded-lg transition-all">
				<i class="fas fa-chevron-down mr-2"></i>Load More (` + strconv.Itoa(totalCount-nextOffset) + ` remaining)
			</button>
		</li>`)
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(buf.String()))
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

func (h *Handler) UpdateEndpointSettings(w http.ResponseWriter, r *http.Request) {
	endpointID := chi.URLParam(r, "endpointID")
	if endpointID == "" {
		http.Error(w, "missing endpoint ID", http.StatusBadRequest)
		return
	}

	// Get browser ID
	browserID := h.GetBrowserID(w, r)

	// Verify endpoint exists and belongs to this user
	endpoint, err := h.Store.GetEndpoint(r.Context(), endpointID)
	if err != nil {
		http.Error(w, "endpoint not found", http.StatusNotFound)
		return
	}

	// Check ownership
	if endpoint.CreatorID != "" && endpoint.CreatorID != browserID {
		http.Error(w, "unauthorized", http.StatusForbidden)
		return
	}

	// Parse form data
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form data", http.StatusBadRequest)
		return
	}

	alias := strings.TrimSpace(r.FormValue("alias"))
	ttlStr := r.FormValue("ttl")

	// Map TTL string to duration
	var ttl time.Duration
	switch ttlStr {
	case "1week":
		ttl = store.TTL1Week
	case "1month":
		ttl = store.TTL1Month
	case "3months":
		ttl = store.TTL3Months
	case "6months":
		ttl = store.TTL6Months
	default:
		ttl = store.DefaultTTL
	}

	// Update endpoint
	if err := h.Store.UpdateEndpoint(r.Context(), endpointID, alias, ttl); err != nil {
		log.Printf("Error updating endpoint %s: %v", endpointID, err)
		http.Error(w, "failed to update endpoint", http.StatusInternalServerError)
		return
	}

	// Return success with HX-Trigger to refresh the page
	w.Header().Set("HX-Trigger", "settingsUpdated")
	w.Header().Set("HX-Redirect", "/"+endpointID)
	w.WriteHeader(http.StatusOK)
}
