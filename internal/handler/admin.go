package handler

import (
	"log"
	"net/http"

	"github.com/PipeOpsHQ/pipehook/internal/store"
)

func (h *Handler) AdminPage(w http.ResponseWriter, r *http.Request) {
	stats, err := h.Store.GetAdminStats(r.Context())
	if err != nil {
		log.Printf("failed to get admin stats: %v", err)
		http.Error(w, "failed to load admin statistics", http.StatusInternalServerError)
		return
	}

	data := struct {
		Stats *store.AdminStats
	}{
		Stats: stats,
	}

	if err := adminTemplate.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("template execution error: %v", err)
		http.Error(w, "failed to render page", http.StatusInternalServerError)
		return
	}
}
