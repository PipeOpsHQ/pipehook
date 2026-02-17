package handler

import (
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
		BaseTemplateData
		Endpoints []*store.Endpoint
		Host      string
	}{
		BaseTemplateData: BaseTemplateData{
			IsAdmin: h.IsAdminAuthenticated(r),
		},
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

	requests, err := h.Store.GetRequestSummaries(r.Context(), endpointID, limit)
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

	var firstRequest *requestDetailData
	if len(requests) > 0 {
		fullRequest, err := h.Store.GetRequest(r.Context(), requests[0].ID)
		if err != nil {
			log.Printf("Warning: failed to load full request %d: %v", requests[0].ID, err)
		}

		if fullRequest != nil {
			firstRequest = h.buildRequestDetailData(fullRequest)
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
		BaseTemplateData
		Endpoint       *store.Endpoint
		Requests       []*store.Request
		FirstRequest   *requestDetailData
		OtherEndpoints []*store.Endpoint
		Host           string
		TotalCount     int
		HasMore        bool
		Limit          int
	}{
		BaseTemplateData: BaseTemplateData{
			IsAdmin: h.IsAdminAuthenticated(r),
		},
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

	requests, err := h.Store.GetRequestSummariesWithOffset(r.Context(), endpointID, limit, offset)
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

	data := h.buildRequestDetailData(req)
	if err := detailTemplate.ExecuteTemplate(w, "request-detail", data); err != nil {
		log.Printf("template execution error: %v", err)
		http.Error(w, "failed to render request", http.StatusInternalServerError)
	}
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

	browserID := h.GetBrowserID(w, r)

	// Verify endpoint exists
	endpoint, err := h.Store.GetEndpoint(r.Context(), endpointID)
	if err != nil {
		http.Error(w, "endpoint not found", http.StatusNotFound)
		return
	}

	// Endpoint owners can delete their own endpoints, and admins can delete any endpoint.
	if endpoint.CreatorID != "" && endpoint.CreatorID != browserID && !h.IsAdminAuthenticated(r) {
		http.Error(w, "unauthorized", http.StatusForbidden)
		return
	}

	h.closeEndpointConnections(endpointID)

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
