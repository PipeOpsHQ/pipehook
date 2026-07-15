package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PipeOpsHQ/pipehook/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type apiEndpointInput struct {
	Alias              string `json:"alias"`
	TTL                string `json:"ttl"`
	DefaultStatus      int    `json:"default_status"`
	DefaultBody        string `json:"default_body"`
	DefaultContentType string `json:"default_content_type"`
	ResponseDelayMS    int    `json:"response_delay_ms"`
	EnableCORS         bool   `json:"enable_cors"`
	ForwardURL         string `json:"forward_url"`
	RequestLimit       int    `json:"request_limit"`
}

type apiRequestSummary struct {
	ID            int64     `json:"id"`
	EndpointID    string    `json:"endpoint_id"`
	Method        string    `json:"method"`
	Path          string    `json:"path"`
	QueryString   string    `json:"query_string"`
	Host          string    `json:"host"`
	Scheme        string    `json:"scheme"`
	RemoteAddr    string    `json:"remote_addr"`
	ContentLength int64     `json:"content_length"`
	BodyTruncated bool      `json:"body_truncated"`
	StatusCode    int       `json:"status_code"`
	CreatedAt     time.Time `json:"created_at"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 128*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return false
	}
	return true
}

func ttlFromName(value string) time.Duration {
	switch value {
	case "1week":
		return store.TTL1Week
	case "1month":
		return store.TTL1Month
	case "6months":
		return store.TTL6Months
	default:
		return store.TTL3Months
	}
}

func settingsFromAPI(input apiEndpointInput) store.EndpointSettings {
	settings := store.DefaultEndpointSettings()
	settings.Alias = strings.TrimSpace(input.Alias)
	settings.TTL = ttlFromName(input.TTL)
	if input.DefaultStatus != 0 {
		settings.DefaultStatus = input.DefaultStatus
	}
	if input.DefaultContentType != "" {
		settings.DefaultContentType = strings.TrimSpace(input.DefaultContentType)
	}
	if input.RequestLimit != 0 {
		settings.RequestLimit = input.RequestLimit
	}
	settings.DefaultBody = input.DefaultBody
	settings.ResponseDelayMS = input.ResponseDelayMS
	settings.EnableCORS = input.EnableCORS
	settings.ForwardURL = strings.TrimSpace(input.ForwardURL)
	return settings
}

func (h *Handler) APIListEndpoints(w http.ResponseWriter, r *http.Request) {
	limit, offset := apiPagination(r)
	endpoints, err := h.Store.ListAllEndpoints(r.Context(), limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list endpoints"})
		return
	}
	writeJSON(w, http.StatusOK, endpoints)
}

func (h *Handler) APICreateEndpoint(w http.ResponseWriter, r *http.Request) {
	var input apiEndpointInput
	if !decodeJSON(w, r, &input) {
		return
	}
	settings := settingsFromAPI(input)
	if err := validateEndpointSettings(settings); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	id := uuid.NewString()
	if _, err := h.Store.CreateEndpoint(r.Context(), id, settings.Alias, "api", settings.TTL); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create endpoint"})
		return
	}
	if err := h.Store.UpdateEndpointSettings(r.Context(), id, settings); err != nil {
		_ = h.Store.DeleteEndpoint(r.Context(), id)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to configure endpoint"})
		return
	}
	endpoint, _ := h.Store.GetEndpoint(r.Context(), id)
	writeJSON(w, http.StatusCreated, endpoint)
}

func (h *Handler) APIGetEndpoint(w http.ResponseWriter, r *http.Request) {
	endpoint, err := h.Store.GetEndpoint(r.Context(), chi.URLParam(r, "endpointID"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "endpoint not found"})
		return
	}
	writeJSON(w, http.StatusOK, endpoint)
}

func (h *Handler) APIUpdateEndpoint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "endpointID")
	if _, err := h.Store.GetEndpoint(r.Context(), id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "endpoint not found"})
		return
	}
	var input apiEndpointInput
	if !decodeJSON(w, r, &input) {
		return
	}
	settings := settingsFromAPI(input)
	if err := validateEndpointSettings(settings); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := h.Store.UpdateEndpointSettings(r.Context(), id, settings); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update endpoint"})
		return
	}
	_ = h.Store.TrimRequests(r.Context(), id, settings.RequestLimit)
	endpoint, _ := h.Store.GetEndpoint(r.Context(), id)
	writeJSON(w, http.StatusOK, endpoint)
}

func (h *Handler) APIDeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "endpointID")
	if _, err := h.Store.GetEndpoint(r.Context(), id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "endpoint not found"})
		return
	}
	h.closeEndpointConnections(id)
	if err := h.Store.DeleteEndpoint(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete endpoint"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) APIListRequests(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "endpointID")
	if _, err := h.Store.GetEndpoint(r.Context(), id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "endpoint not found"})
		return
	}
	limit, offset := apiPagination(r)
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	requests, err := h.Store.SearchRequestSummaries(r.Context(), id, query, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list requests"})
		return
	}
	summaries := make([]apiRequestSummary, 0, len(requests))
	for _, request := range requests {
		summaries = append(summaries, apiRequestSummary{
			ID: request.ID, EndpointID: request.EndpointID, Method: request.Method, Path: request.Path,
			QueryString: request.QueryString, Host: request.Host, Scheme: request.Scheme,
			RemoteAddr: request.RemoteAddr, ContentLength: request.ContentLength,
			BodyTruncated: request.BodyTruncated, StatusCode: request.StatusCode, CreatedAt: request.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, summaries)
}

func (h *Handler) APIGetRequest(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "requestID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request ID"})
		return
	}
	request, err := h.Store.GetRequest(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "request not found"})
		return
	}
	writeJSON(w, http.StatusOK, exportRequest(request))
}

func (h *Handler) APIDeleteRequest(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "requestID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request ID"})
		return
	}
	if _, err := h.Store.GetRequest(r.Context(), id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "request not found"})
		return
	}
	if err := h.Store.DeleteRequest(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete request"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func apiPagination(r *http.Request) (int, int) {
	limit, offset := 100, 0
	if parsed, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && parsed > 0 && parsed <= 500 {
		limit = parsed
	}
	if parsed, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && parsed >= 0 {
		offset = parsed
	}
	return limit, offset
}
