package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSQLiteStoreMigratesAndRetainsRequests(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.Exec(`
		CREATE TABLE endpoints (id TEXT PRIMARY KEY, alias TEXT, created_at DATETIME, expires_at DATETIME);
		CREATE TABLE requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT, endpoint_id TEXT, method TEXT, path TEXT,
			remote_addr TEXT, headers TEXT, body BLOB, status_code INTEGER, created_at DATETIME
		);
	`)
	if err != nil {
		t.Fatal(err)
	}
	_ = legacy.Close()

	store, err := NewSQLiteStore(databasePath)
	if err != nil {
		t.Fatalf("migrate legacy database: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	endpoint, err := store.CreateEndpoint(ctx, "endpoint", "", "browser", DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.DefaultStatus != 200 || endpoint.RequestLimit != DefaultRequestLimit {
		t.Fatalf("unexpected defaults: %+v", endpoint)
	}

	for i, body := range []string{"first", "searchable payload", "third"} {
		request := &Request{
			EndpointID: "endpoint", Method: "POST", Path: "/h/endpoint/events",
			QueryString: "page=1", Host: "hooks.example", Scheme: "https",
			Headers: `{"Content-Type":["text/plain"]}`, Body: []byte(body), ContentLength: int64(len(body)),
			StatusCode: 202, CreatedAt: time.Now().Add(time.Duration(i) * time.Millisecond),
		}
		if err := store.SaveRequest(ctx, request); err != nil {
			t.Fatal(err)
		}
	}

	found, err := store.SearchRequests(ctx, "endpoint", "searchable", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || string(found[0].Body) != "searchable payload" || found[0].QueryString != "page=1" {
		t.Fatalf("search did not preserve request data: %+v", found)
	}
	if err := store.TrimRequests(ctx, "endpoint", 2); err != nil {
		t.Fatal(err)
	}
	count, err := store.CountRequests(ctx, "endpoint")
	if err != nil || count != 2 {
		t.Fatalf("expected two retained requests, got %d, err=%v", count, err)
	}
	stats, err := store.GetAdminStats(ctx)
	if err != nil || stats.TotalRequests != 2 || len(stats.EndpointUsageStats) != 1 || stats.EndpointUsageStats[0].LastRequestAt == nil {
		t.Fatalf("unexpected admin stats: %+v err=%v", stats, err)
	}
}

func TestUpdateEndpointSettings(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.CreateEndpoint(ctx, "endpoint", "", "browser", DefaultTTL); err != nil {
		t.Fatal(err)
	}
	settings := EndpointSettings{
		Alias: "payments", TTL: TTL1Week, DefaultStatus: 202, DefaultBody: `{"accepted":true}`,
		DefaultContentType: "application/json", ResponseDelayMS: 25, EnableCORS: true,
		ForwardURL: "https://example.com/hooks", RequestLimit: 50,
	}
	if err := store.UpdateEndpointSettings(ctx, "endpoint", settings); err != nil {
		t.Fatal(err)
	}
	endpoint, err := store.GetEndpoint(ctx, "endpoint")
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Alias != settings.Alias || endpoint.DefaultStatus != 202 || !endpoint.EnableCORS || endpoint.RequestLimit != 50 {
		t.Fatalf("settings were not persisted: %+v", endpoint)
	}
}
