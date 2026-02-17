package handler

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PipeOpsHQ/pipehook/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const (
	maxDisplayTextBytes   = 256 * 1024
	maxDisplayHexBytes    = 64 * 1024
	maxDecodedDisplaySize = 2 * 1024 * 1024
)

type requestDetailData struct {
	*store.Request
	HeadersMap    map[string][]string
	HeadersJSON   string
	BodyString    string
	BodyHex       string
	ContentType   string
	IsBinary      bool
	DisplayNotice string
}

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

func (h *Handler) buildRequestDetailData(req *store.Request) *requestDetailData {
	headers := parseRequestHeaders(req.ID, req.Headers)
	headersJSON, _ := json.MarshalIndent(headers, "", "  ")

	contentType := normalizeContentType(headerValue(headers, "Content-Type"))
	if contentType == "" {
		contentType = "text/plain"
	}

	displayBody := req.Body
	notices := []string{}

	contentEncoding := strings.TrimSpace(headerValue(headers, "Content-Encoding"))
	if contentEncoding != "" {
		decoded, truncated, err := decodeBodyForDisplay(req.Body, contentEncoding, maxDecodedDisplaySize)
		if err == nil {
			displayBody = decoded
			notices = append(notices, "Decoded "+contentEncoding+" payload for display.")
			if truncated {
				notices = append(notices, "Decoded body view is truncated for performance.")
			}
		} else {
			notices = append(notices, "Showing raw "+contentEncoding+" payload (decode failed).")
		}
	}

	if strings.EqualFold(strings.TrimSpace(headerValue(headers, "X-Pipehook-Body-Truncated")), "true") {
		limit := strings.TrimSpace(headerValue(headers, "X-Pipehook-Body-Limit"))
		if limit != "" {
			notices = append(notices, "Stored body was truncated at "+limit+" bytes.")
		} else {
			notices = append(notices, "Stored body was truncated at capture time.")
		}
	}

	isBinary := isBinaryBody(displayBody, contentType)
	bodyString := ""
	bodyHex := ""

	if isBinary {
		hexBytes := displayBody
		if len(hexBytes) > maxDisplayHexBytes {
			hexBytes = hexBytes[:maxDisplayHexBytes]
			notices = append(notices, "Showing first 65536 bytes as hex.")
		}
		bodyHex = hex.Dump(hexBytes)
	} else {
		textBytes := displayBody
		if len(textBytes) > maxDisplayTextBytes {
			textBytes = textBytes[:maxDisplayTextBytes]
			notices = append(notices, "Showing first 262144 bytes in viewer.")
		}
		bodyString = string(textBytes)
	}

	return &requestDetailData{
		Request:       req,
		HeadersMap:    headers,
		HeadersJSON:   string(headersJSON),
		BodyString:    bodyString,
		BodyHex:       bodyHex,
		ContentType:   contentType,
		IsBinary:      isBinary,
		DisplayNotice: strings.Join(notices, " "),
	}
}

func parseRequestHeaders(requestID int64, rawHeaders string) map[string][]string {
	headers := make(map[string][]string)
	if strings.TrimSpace(rawHeaders) == "" {
		return headers
	}

	if err := json.Unmarshal([]byte(rawHeaders), &headers); err == nil {
		return headers
	}

	legacyHeaders := make(map[string]string)
	if err := json.Unmarshal([]byte(rawHeaders), &legacyHeaders); err == nil {
		for k, v := range legacyHeaders {
			headers[k] = []string{v}
		}
		return headers
	}

	log.Printf("Warning: Failed to parse headers for request %d", requestID)
	return headers
}

func headerValue(headers map[string][]string, key string) string {
	for k, values := range headers {
		if strings.EqualFold(k, key) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func normalizeContentType(contentType string) string {
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		return ""
	}

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err == nil {
		return strings.TrimSpace(mediaType)
	}

	if idx := strings.Index(contentType, ";"); idx > 0 {
		contentType = contentType[:idx]
	}
	return strings.TrimSpace(contentType)
}

func isBinaryBody(body []byte, contentType string) bool {
	if len(body) == 0 {
		return false
	}

	ct := strings.ToLower(strings.TrimSpace(contentType))
	isTextType := strings.HasPrefix(ct, "text/") ||
		strings.Contains(ct, "json") ||
		strings.Contains(ct, "xml") ||
		strings.Contains(ct, "html") ||
		strings.Contains(ct, "javascript") ||
		strings.Contains(ct, "form-urlencoded")

	sampleSize := len(body)
	if sampleSize > 1000 {
		sampleSize = 1000
	}

	nonPrintableCount := 0
	for i := 0; i < sampleSize; i++ {
		b := body[i]
		if b < 32 && b != 9 && b != 10 && b != 13 && b != 27 {
			nonPrintableCount++
		}
	}

	threshold := 0.25
	if isTextType {
		threshold = 0.50
	}
	return float64(nonPrintableCount)/float64(sampleSize) > threshold
}

func decodeBodyForDisplay(body []byte, contentEncoding string, maxBytes int) ([]byte, bool, error) {
	encodings := strings.Split(strings.ToLower(contentEncoding), ",")
	decoded := body
	wasTruncated := false

	for i := len(encodings) - 1; i >= 0; i-- {
		encoding := strings.TrimSpace(encodings[i])
		if encoding == "" || encoding == "identity" {
			continue
		}

		var (
			reader io.ReadCloser
			err    error
		)

		switch encoding {
		case "gzip":
			reader, err = gzip.NewReader(bytes.NewReader(decoded))
		case "deflate":
			reader, err = zlib.NewReader(bytes.NewReader(decoded))
		default:
			return body, false, fmt.Errorf("unsupported content-encoding %q", encoding)
		}
		if err != nil {
			return body, false, err
		}

		nextBody, truncated, readErr := readLimited(reader, maxBytes)
		_ = reader.Close()
		if readErr != nil {
			return body, false, readErr
		}
		if truncated {
			wasTruncated = true
		}
		decoded = nextBody
	}

	return decoded, wasTruncated, nil
}

func readLimited(r io.Reader, limit int) ([]byte, bool, error) {
	limited := &io.LimitedReader{R: r, N: int64(limit) + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, false, err
	}
	if len(data) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}
