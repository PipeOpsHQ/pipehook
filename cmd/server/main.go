package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/PipeOpsHQ/pipehook/internal/handler"
	"github.com/PipeOpsHQ/pipehook/internal/store"
	"github.com/PipeOpsHQ/pipehook/ui"
)

func main() {
	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		dbPath = "webhook.db"
	}
	s, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		log.Fatal(err)
	}

	h := handler.NewHandler(s)

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	// Logger middleware - skip for webhook routes to preserve body
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/h/") {
				middleware.Logger(next).ServeHTTP(w, r)
			} else {
				next.ServeHTTP(w, r)
			}
		})
	})

	// Static files (must be before catch-all routes)
	r.Handle("/static/*", http.FileServer(http.FS(ui.FS)))
	r.Get("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/static/pipehook.svg", http.StatusMovedPermanently)
	})

	// UI
	r.Get("/", h.Home)
	r.Post("/new", h.CreateEndpoint)
	r.Get("/r/{requestID}", h.RequestDetail)
	r.Post("/r/{requestID}/replay", h.ReplayRequest)
	r.Delete("/r/{requestID}", h.DeleteRequest)
	r.Delete("/endpoint/{endpointID}", h.DeleteEndpoint)
	r.Get("/ws/{endpointID}", h.WebSocket)
	r.Get("/{endpointID}", h.Dashboard)

	// Webhook receiver
	r.HandleFunc("/h/{endpointID}", h.CaptureWebhook)
	r.HandleFunc("/h/{endpointID}/*", h.CaptureWebhook)

	// Cleanup worker
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		for range ticker.C {
			if err := s.Cleanup(context.Background()); err != nil {
				log.Printf("cleanup error: %v", err)
			}
		}
	}()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Starting server on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatal(err)
	}
}
