package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/PipeOpsHQ/pipehook/internal/handler"
	"github.com/PipeOpsHQ/pipehook/internal/store"
	"github.com/PipeOpsHQ/pipehook/ui"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// parseSize parses a size string like "50MB", "100KB", "1GB" into bytes
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	s = strings.ToUpper(s)

	var multiplier int64 = 1
	if strings.HasSuffix(s, "KB") {
		multiplier = 1024
		s = strings.TrimSuffix(s, "KB")
	} else if strings.HasSuffix(s, "MB") {
		multiplier = 1024 * 1024
		s = strings.TrimSuffix(s, "MB")
	} else if strings.HasSuffix(s, "GB") {
		multiplier = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "GB")
	} else if strings.HasSuffix(s, "B") {
		s = strings.TrimSuffix(s, "B")
	}

	val, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size format: %v", err)
	}

	return val * multiplier, nil
}

func main() {
	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		dbPath = "webhook.db"
	}

	dbDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dbDir, 0750); err != nil {
		log.Fatalf("create database directory %s: %v", dbDir, err)
	}

	s, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	h := handler.NewHandler(s)

	maxWebhookBodySize := int64(2 * 1024 * 1024) // 2MB default
	if maxBodySizeStr := os.Getenv("MAX_WEBHOOK_BODY_SIZE"); maxBodySizeStr != "" {
		parsedSize, parseErr := parseSize(maxBodySizeStr)
		if parseErr != nil || parsedSize <= 0 {
			log.Printf("Invalid MAX_WEBHOOK_BODY_SIZE=%q, using default 2MB", maxBodySizeStr)
		} else {
			maxWebhookBodySize = parsedSize
		}
	}
	h.MaxWebhookBodyBytes = maxWebhookBodySize
	log.Printf("Webhook max body size configured to %d bytes", maxWebhookBodySize)

	// Get admin credentials from environment variables
	adminUsername := os.Getenv("ADMIN_USERNAME")
	adminPassword := os.Getenv("ADMIN_PASSWORD")

	// Set admin credentials in handler so it can check authentication
	h.AdminUsername = adminUsername
	h.AdminPassword = adminPassword
	h.APIKey = strings.TrimSpace(os.Getenv("API_KEY"))
	allowPrivateForward, _ := strconv.ParseBool(os.Getenv("ALLOW_PRIVATE_FORWARDING"))
	h.SetAllowPrivateForwarding(allowPrivateForward)

	if adminUsername != "" && adminPassword != "" {
		log.Printf("Admin authentication enabled for /admin endpoint")
	} else {
		log.Printf("WARNING: Admin routes are unavailable until ADMIN_USERNAME and ADMIN_PASSWORD are configured.")
	}
	if h.APIKey == "" {
		log.Printf("API routes are unavailable until API_KEY is configured.")
	}

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

	// Versioned assets are immutable; unversioned assets must revalidate after deploys.
	staticFiles := http.FileServer(http.FS(ui.FS))
	r.Handle("/static/*", http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		staticFiles.ServeHTTP(w, request)
	}))
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
	r.Post("/endpoint/{endpointID}/settings", h.UpdateEndpointSettings)
	r.Get("/endpoint/{endpointID}/export.json", h.ExportRequestsJSON)
	r.Get("/endpoint/{endpointID}/export.csv", h.ExportRequestsCSV)
	r.Get("/ws/{endpointID}", h.WebSocket)
	r.Get("/{endpointID}/more", h.LoadMoreRequests)
	r.Get("/{endpointID}", h.Dashboard)

	// Admin routes (protected with basic auth if credentials are set)
	r.Group(func(r chi.Router) {
		r.Use(handler.BasicAuthMiddleware(adminUsername, adminPassword))
		r.Get("/admin", h.AdminPage)
		r.Delete("/admin/endpoint/{endpointID}", h.AdminDeleteEndpoint)
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(h.APIAuthMiddleware)
		r.Get("/endpoints", h.APIListEndpoints)
		r.Post("/endpoints", h.APICreateEndpoint)
		r.Get("/endpoints/{endpointID}", h.APIGetEndpoint)
		r.Put("/endpoints/{endpointID}", h.APIUpdateEndpoint)
		r.Delete("/endpoints/{endpointID}", h.APIDeleteEndpoint)
		r.Get("/endpoints/{endpointID}/requests", h.APIListRequests)
		r.Get("/requests/{requestID}", h.APIGetRequest)
		r.Delete("/requests/{requestID}", h.APIDeleteRequest)
	})

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

	shutdownCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := s.Cleanup(shutdownCtx); err != nil && shutdownCtx.Err() == nil {
					log.Printf("cleanup error: %v", err)
				}
			case <-shutdownCtx.Done():
				return
			}
		}
	}()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:           ":" + port,
		Handler:        r,
		MaxHeaderBytes: 1 << 20, // 1MB max header size
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   45 * time.Second,
		IdleTimeout:    120 * time.Second,
	}
	go func() {
		<-shutdownCtx.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("server shutdown error: %v", err)
		}
	}()

	log.Printf("Starting server on :%s", port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
