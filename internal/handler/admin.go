package handler

import (
	"log"
	"net/http"

	"github.com/PipeOpsHQ/pipehook/internal/store"
	"github.com/go-chi/chi/v5"
)

func (h *Handler) AdminPage(w http.ResponseWriter, r *http.Request) {
	stats, err := h.Store.GetAdminStats(r.Context())
	if err != nil {
		log.Printf("failed to get admin stats: %v", err)
		http.Error(w, "failed to load admin statistics", http.StatusInternalServerError)
		return
	}

	data := struct {
		BaseTemplateData
		Stats *store.AdminStats
	}{
		BaseTemplateData: BaseTemplateData{
			IsAdmin: h.IsAdminAuthenticated(r),
		},
		Stats: stats,
	}

	if err := adminTemplate.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("template execution error: %v", err)
		http.Error(w, "failed to render page", http.StatusInternalServerError)
		return
	}
}

func (h *Handler) AdminDeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	endpointID := chi.URLParam(r, "endpointID")
	if endpointID == "" {
		http.Error(w, "missing endpoint ID", http.StatusBadRequest)
		return
	}

	if _, err := h.Store.GetEndpoint(r.Context(), endpointID); err != nil {
		http.Error(w, "endpoint not found", http.StatusNotFound)
		return
	}

	h.closeEndpointConnections(endpointID)
	if err := h.Store.DeleteEndpoint(r.Context(), endpointID); err != nil {
		http.Error(w, "failed to delete endpoint", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", "endpointDeleted")
	w.WriteHeader(http.StatusOK)
}
