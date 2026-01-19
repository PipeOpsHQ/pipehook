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

	// Ensure the directory exists and is writable
	dbDir := "."
	if strings.Contains(dbPath, "/") {
		parts := strings.Split(dbPath, "/")
		dbDir = strings.Join(parts[:len(parts)-1], "/")
	}

	log.Printf("Checking database directory: %s", dbDir)
	if _, err := os.Stat(dbDir); os.IsNotExist(err) {
		if err := os.MkdirAll(dbDir, 0777); err != nil {
			log.Printf("Warning: failed to create database directory: %v", err)
		}
	} else {
		// Attempt to make directory writable just in case
		os.Chmod(dbDir, 0777)
	}

	// If database file exists, check its permissions
	if _, err := os.Stat(dbPath); err == nil {
		log.Printf("Database file exists, attempting to ensure it is writable...")
		if err := os.Chmod(dbPath, 0666); err != nil {
			log.Printf("Warning: could not chmod database file: %v", err)
		}
	}

	// Test if directory is writable
	testFile := dbDir + "/.write_test"
	if err := os.WriteFile(testFile, []byte("test"), 0666); err != nil {
		log.Printf("CRITICAL: Database directory %s is NOT writable: %v", dbDir, err)
		log.Printf("Current User ID: %d, Group ID: %d", os.Getuid(), os.Getgid())
	} else {
		os.Remove(testFile)
		log.Printf("Database directory %s is writable", dbDir)
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

	// Webhook receiver - accept ALL HTTP methods (GET, POST, PUT, PATCH, DELETE, etc.)
	// Using HandleFunc which accepts all methods, and also explicitly registering common methods
	r.HandleFunc("/h/{endpointID}", h.CaptureWebhook)
	r.HandleFunc("/h/{endpointID}/*", h.CaptureWebhook)

	// Explicitly register all common HTTP methods to ensure body is captured
	methods := []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD", "CONNECT", "TRACE"}
	for _, method := range methods {
		r.MethodFunc(method, "/h/{endpointID}", h.CaptureWebhook)
		r.MethodFunc(method, "/h/{endpointID}/*", h.CaptureWebhook)
	}

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
