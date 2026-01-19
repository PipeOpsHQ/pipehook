package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/nitrocode/webhook/internal/handler"
	"github.com/nitrocode/webhook/internal/store"
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
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// UI
	r.Get("/", h.Home)
	r.Post("/new", h.CreateEndpoint)
	r.Get("/{endpointID}", h.Dashboard)
	r.Get("/r/{requestID}", h.RequestDetail)
	r.Post("/r/{requestID}/replay", h.ReplayRequest)
	r.Get("/sse/{endpointID}", h.SSE)

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
