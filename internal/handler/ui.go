package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/PipeOpsHQ/pipehook/internal/store"
)

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	homeTemplate.ExecuteTemplate(w, "layout", nil)
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

	type requestDetailData struct {
		*store.Request
		HeadersMap map[string][]string
	}

	var firstRequest *requestDetailData
	if len(requests) > 0 {
		var headers map[string][]string
		json.Unmarshal([]byte(requests[0].Headers), &headers)
		firstRequest = &requestDetailData{
			Request:    requests[0],
			HeadersMap: headers,
		}
	}

	data := struct {
		Endpoint     *store.Endpoint
		Requests     []*store.Request
		FirstRequest *requestDetailData
		Host         string
	}{
		Endpoint:     endpoint,
		Requests:     requests,
		FirstRequest: firstRequest,
		Host:         r.Host,
	}

	dashboardTemplate.ExecuteTemplate(w, "layout", data)
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

	data := struct {
		*store.Request
		HeadersMap map[string][]string
	}{
		Request:    req,
		HeadersMap: headers,
	}

	detailTemplate.ExecuteTemplate(w, "request-detail", data)
}
