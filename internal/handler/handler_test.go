package handler

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/PipeOpsHQ/pipehook/internal/store"
	"github.com/go-chi/chi/v5"
)

func testHandler(t *testing.T) (*Handler, *store.SQLiteStore) {
	t.Helper()
	database, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(database)
	t.Cleanup(func() { _ = database.Close() })
	return handler, database
}

func TestCaptureWebhookPreservesMetadataAndCustomResponse(t *testing.T) {
	handler, database := testHandler(t)
	endpoint, err := database.CreateEndpoint(t.Context(), "endpoint", "", "browser", store.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	settings := store.DefaultEndpointSettings()
	settings.DefaultStatus = http.StatusAccepted
	settings.DefaultBody = `{"accepted":true}`
	settings.DefaultContentType = "application/json"
	settings.EnableCORS = true
	settings.RequestLimit = 2
	if err := database.UpdateEndpointSettings(t.Context(), endpoint.ID, settings); err != nil {
		t.Fatal(err)
	}

	router := chi.NewRouter()
	router.HandleFunc("/h/{endpointID}/*", handler.CaptureWebhook)
	server := httptest.NewServer(router)
	defer server.Close()

	payload := []byte{0x00, 0x01, 0x02, 0xff}
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/h/endpoint/files?part=one", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/octet-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusAccepted || string(body) != settings.DefaultBody {
		t.Fatalf("unexpected response: status=%d body=%q", response.StatusCode, body)
	}
	if response.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("expected CORS response header")
	}

	requests, err := database.GetRequests(t.Context(), endpoint.ID, 10)
	if err != nil || len(requests) != 1 {
		t.Fatalf("expected captured request: len=%d err=%v", len(requests), err)
	}
	captured := requests[0]
	if captured.Path != "/h/endpoint/files" || captured.QueryString != "part=one" || captured.Scheme != "http" || !bytes.Equal(captured.Body, payload) {
		t.Fatalf("capture fidelity lost: %+v body=%v", captured, captured.Body)
	}
}

func TestReplayDoesNotDuplicateCapturePath(t *testing.T) {
	handler, database := testHandler(t)
	endpoint, err := database.CreateEndpoint(t.Context(), "endpoint", "", "browser", store.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.HandleFunc("/h/{endpointID}", handler.CaptureWebhook)
	router.HandleFunc("/h/{endpointID}/*", handler.CaptureWebhook)
	router.Post("/r/{requestID}/replay", handler.ReplayRequest)
	server := httptest.NewServer(router)
	defer server.Close()

	response, err := http.Post(server.URL+"/h/endpoint/events?source=test", "text/plain", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	requests, _ := database.GetRequests(t.Context(), endpoint.ID, 10)
	if len(requests) != 1 {
		t.Fatalf("expected initial request, got %d", len(requests))
	}

	replay, _ := http.NewRequest(http.MethodPost, server.URL+"/r/"+strconv.FormatInt(requests[0].ID, 10)+"/replay", nil)
	replay.AddCookie(&http.Cookie{Name: browserIDCookieName, Value: "browser"})
	replayed, err := http.DefaultClient.Do(replay)
	if err != nil {
		t.Fatal(err)
	}
	_ = replayed.Body.Close()
	if replayed.StatusCode != http.StatusOK {
		t.Fatalf("replay failed with %d", replayed.StatusCode)
	}

	requests, _ = database.GetRequests(t.Context(), endpoint.ID, 10)
	if len(requests) != 2 || requests[0].Path != "/h/endpoint/events" || requests[0].QueryString != "source=test" {
		t.Fatalf("replay path was reconstructed incorrectly: %+v", requests)
	}
}

func TestAPIAuthentication(t *testing.T) {
	handler, _ := testHandler(t)
	handler.APIKey = "secret"
	router := chi.NewRouter()
	router.Route("/api/v1", func(router chi.Router) {
		router.Use(handler.APIAuthMiddleware)
		router.Get("/endpoints", handler.APIListEndpoints)
	})

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/endpoints", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauthorized.Code)
	}
	authorizedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/endpoints", nil)
	authorizedRequest.Header.Set("Authorization", "Bearer secret")
	authorized := httptest.NewRecorder()
	router.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", authorized.Code, authorized.Body.String())
	}
}

func TestReadRequestBodyWithLimit(t *testing.T) {
	body, truncated, err := readRequestBodyWithLimit(io.NopCloser(strings.NewReader("abcdef")), 3)
	if err != nil || !truncated || string(body) != "abc" {
		t.Fatalf("body=%q truncated=%v err=%v", body, truncated, err)
	}
}

func TestBroadcastDoesNotRetainEmptyEndpoint(t *testing.T) {
	handler, _ := testHandler(t)
	handler.Broadcast("unused-endpoint", &store.Request{Method: http.MethodPost})
	handler.clientsMu.RLock()
	defer handler.clientsMu.RUnlock()
	if len(handler.clients) != 0 {
		t.Fatalf("empty websocket endpoint was retained: %+v", handler.clients)
	}
}

func TestLayoutUsesContentVersionedStylesheet(t *testing.T) {
	handler, _ := testHandler(t)
	response := httptest.NewRecorder()
	handler.Home(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("home failed with %d: %s", response.Code, response.Body.String())
	}
	want := "/static/app.css?v=" + appCSSVersion
	if appCSSVersion == "unknown" || !strings.Contains(response.Body.String(), want) {
		t.Fatalf("layout does not use versioned stylesheet %q", want)
	}
}
