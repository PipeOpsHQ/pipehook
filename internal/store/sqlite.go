package store

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	endpointColumns = `id, COALESCE(alias, ''), COALESCE(creator_id, ''), created_at, expires_at,
		COALESCE(default_status, 200), COALESCE(default_body, 'ok'),
		COALESCE(default_content_type, 'text/plain; charset=utf-8'),
		COALESCE(response_delay_ms, 0), COALESCE(enable_cors, 0),
		COALESCE(forward_url, ''), COALESCE(request_limit, 1000)`
	requestColumns = `id, endpoint_id, method, path, COALESCE(query_string, ''),
		COALESCE(host, ''), COALESCE(scheme, ''), remote_addr, headers, body,
		COALESCE(content_length, 0), COALESCE(body_truncated, 0), status_code, created_at`
)

type SQLiteStore struct {
	db *sql.DB
}

type scanner interface {
	Scan(dest ...any) error
}

func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}

	s := &SQLiteStore{db: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) init() error {
	var readOnly int
	if err := s.db.QueryRow("PRAGMA query_only;").Scan(&readOnly); err == nil && readOnly == 1 {
		log.Printf("CRITICAL WARNING: Database is opened in READ-ONLY mode!")
	}

	if _, err := s.db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		log.Printf("Warning: Failed to enable WAL mode: %v", err)
	}
	_, _ = s.db.Exec("PRAGMA synchronous=NORMAL;")
	_, _ = s.db.Exec("PRAGMA foreign_keys=ON;")
	_, _ = s.db.Exec("PRAGMA busy_timeout=5000;")

	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS endpoints (
			id TEXT PRIMARY KEY,
			alias TEXT,
			creator_id TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			expires_at DATETIME,
			default_status INTEGER NOT NULL DEFAULT 200,
			default_body TEXT NOT NULL DEFAULT 'ok',
			default_content_type TEXT NOT NULL DEFAULT 'text/plain; charset=utf-8',
			response_delay_ms INTEGER NOT NULL DEFAULT 0,
			enable_cors INTEGER NOT NULL DEFAULT 0,
			forward_url TEXT NOT NULL DEFAULT '',
			request_limit INTEGER NOT NULL DEFAULT 1000
		);
		CREATE TABLE IF NOT EXISTS requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			endpoint_id TEXT NOT NULL,
			method TEXT,
			path TEXT,
			query_string TEXT NOT NULL DEFAULT '',
			host TEXT NOT NULL DEFAULT '',
			scheme TEXT NOT NULL DEFAULT '',
			remote_addr TEXT,
			headers TEXT,
			body BLOB,
			content_length INTEGER NOT NULL DEFAULT 0,
			body_truncated INTEGER NOT NULL DEFAULT 0,
			status_code INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(endpoint_id) REFERENCES endpoints(id) ON DELETE CASCADE
		);
	`); err != nil {
		return fmt.Errorf("initialize database schema: %w", err)
	}

	migrations := []struct {
		table, column, definition string
	}{
		{"endpoints", "creator_id", "TEXT"},
		{"endpoints", "default_status", "INTEGER NOT NULL DEFAULT 200"},
		{"endpoints", "default_body", "TEXT NOT NULL DEFAULT 'ok'"},
		{"endpoints", "default_content_type", "TEXT NOT NULL DEFAULT 'text/plain; charset=utf-8'"},
		{"endpoints", "response_delay_ms", "INTEGER NOT NULL DEFAULT 0"},
		{"endpoints", "enable_cors", "INTEGER NOT NULL DEFAULT 0"},
		{"endpoints", "forward_url", "TEXT NOT NULL DEFAULT ''"},
		{"endpoints", "request_limit", "INTEGER NOT NULL DEFAULT 1000"},
		{"requests", "query_string", "TEXT NOT NULL DEFAULT ''"},
		{"requests", "host", "TEXT NOT NULL DEFAULT ''"},
		{"requests", "scheme", "TEXT NOT NULL DEFAULT ''"},
		{"requests", "content_length", "INTEGER NOT NULL DEFAULT 0"},
		{"requests", "body_truncated", "INTEGER NOT NULL DEFAULT 0"},
	}
	for _, migration := range migrations {
		if err := s.ensureColumn(migration.table, migration.column, migration.definition); err != nil {
			return err
		}
	}

	_, err := s.db.Exec(`
		DROP INDEX IF EXISTS idx_requests_endpoint_id;
		CREATE INDEX IF NOT EXISTS idx_endpoints_creator_id ON endpoints(creator_id);
		CREATE INDEX IF NOT EXISTS idx_requests_endpoint_created ON requests(endpoint_id, created_at DESC);
	`)
	return err
}

func (s *SQLiteStore) ensureColumn(table, column, definition string) error {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	if _, err := s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition)); err != nil {
		return fmt.Errorf("add %s.%s: %w", table, column, err)
	}
	return nil
}

func scanEndpoint(row scanner) (*Endpoint, error) {
	var endpoint Endpoint
	if err := row.Scan(
		&endpoint.ID, &endpoint.Alias, &endpoint.CreatorID, &endpoint.CreatedAt, &endpoint.ExpiresAt,
		&endpoint.DefaultStatus, &endpoint.DefaultBody, &endpoint.DefaultContentType,
		&endpoint.ResponseDelayMS, &endpoint.EnableCORS, &endpoint.ForwardURL, &endpoint.RequestLimit,
	); err != nil {
		return nil, err
	}
	return &endpoint, nil
}

func scanRequest(row scanner) (*Request, error) {
	var request Request
	if err := row.Scan(
		&request.ID, &request.EndpointID, &request.Method, &request.Path, &request.QueryString,
		&request.Host, &request.Scheme, &request.RemoteAddr, &request.Headers, &request.Body,
		&request.ContentLength, &request.BodyTruncated, &request.StatusCode, &request.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &request, nil
}

func (s *SQLiteStore) CreateEndpoint(ctx context.Context, id, alias, creatorID string, ttl time.Duration) (*Endpoint, error) {
	now := time.Now()
	settings := DefaultEndpointSettings()
	endpoint := &Endpoint{
		ID: id, Alias: alias, CreatorID: creatorID, CreatedAt: now, ExpiresAt: now.Add(ttl),
		DefaultStatus: settings.DefaultStatus, DefaultBody: settings.DefaultBody,
		DefaultContentType: settings.DefaultContentType, RequestLimit: settings.RequestLimit,
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO endpoints (
			id, alias, creator_id, created_at, expires_at, default_status, default_body,
			default_content_type, response_delay_ms, enable_cors, forward_url, request_limit
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, endpoint.ID, endpoint.Alias, endpoint.CreatorID, endpoint.CreatedAt, endpoint.ExpiresAt,
		endpoint.DefaultStatus, endpoint.DefaultBody, endpoint.DefaultContentType,
		endpoint.ResponseDelayMS, endpoint.EnableCORS, endpoint.ForwardURL, endpoint.RequestLimit)
	if err != nil {
		return nil, err
	}
	return endpoint, nil
}

func (s *SQLiteStore) GetEndpoint(ctx context.Context, id string) (*Endpoint, error) {
	return scanEndpoint(s.db.QueryRowContext(ctx, "SELECT "+endpointColumns+" FROM endpoints WHERE id = ?", id))
}

func (s *SQLiteStore) UpdateEndpoint(ctx context.Context, id, alias string, ttl time.Duration) error {
	endpoint, err := s.GetEndpoint(ctx, id)
	if err != nil {
		return err
	}
	return s.UpdateEndpointSettings(ctx, id, EndpointSettings{
		Alias: alias, TTL: ttl, DefaultStatus: endpoint.DefaultStatus, DefaultBody: endpoint.DefaultBody,
		DefaultContentType: endpoint.DefaultContentType, ResponseDelayMS: endpoint.ResponseDelayMS,
		EnableCORS: endpoint.EnableCORS, ForwardURL: endpoint.ForwardURL, RequestLimit: endpoint.RequestLimit,
	})
}

func (s *SQLiteStore) UpdateEndpointSettings(ctx context.Context, id string, settings EndpointSettings) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE endpoints SET alias = ?, expires_at = ?, default_status = ?, default_body = ?,
			default_content_type = ?, response_delay_ms = ?, enable_cors = ?, forward_url = ?, request_limit = ?
		WHERE id = ?
	`, settings.Alias, time.Now().Add(settings.TTL), settings.DefaultStatus, settings.DefaultBody,
		settings.DefaultContentType, settings.ResponseDelayMS, settings.EnableCORS, settings.ForwardURL,
		settings.RequestLimit, id)
	return err
}

func (s *SQLiteStore) DeleteEndpoint(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM endpoints WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) ListEndpoints(ctx context.Context, creatorID string, limit int) ([]*Endpoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+endpointColumns+`
		FROM endpoints WHERE creator_id = ? AND expires_at > ? ORDER BY created_at DESC LIMIT ?`,
		creatorID, time.Now(), limit)
	if err != nil {
		return nil, err
	}
	return collectEndpoints(rows)
}

func (s *SQLiteStore) ListAllEndpoints(ctx context.Context, limit, offset int) ([]*Endpoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+endpointColumns+`
		FROM endpoints WHERE expires_at > ? ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		time.Now(), limit, offset)
	if err != nil {
		return nil, err
	}
	return collectEndpoints(rows)
}

func collectEndpoints(rows *sql.Rows) ([]*Endpoint, error) {
	defer rows.Close()
	endpoints := make([]*Endpoint, 0)
	for rows.Next() {
		endpoint, err := scanEndpoint(rows)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints, rows.Err()
}

func (s *SQLiteStore) SaveRequest(ctx context.Context, request *Request) error {
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO requests (
			endpoint_id, method, path, query_string, host, scheme, remote_addr, headers, body,
			content_length, body_truncated, status_code, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, request.EndpointID, request.Method, request.Path, request.QueryString, request.Host, request.Scheme,
		request.RemoteAddr, request.Headers, request.Body, request.ContentLength, request.BodyTruncated,
		request.StatusCode, now)
	if err != nil {
		return err
	}
	request.ID, _ = result.LastInsertId()
	request.CreatedAt = now
	return nil
}

func (s *SQLiteStore) GetRequests(ctx context.Context, endpointID string, limit int) ([]*Request, error) {
	return s.GetRequestsWithOffset(ctx, endpointID, limit, 0)
}

func (s *SQLiteStore) GetRequestsWithOffset(ctx context.Context, endpointID string, limit, offset int) ([]*Request, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+requestColumns+`
		FROM requests WHERE endpoint_id = ? ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
		endpointID, limit, offset)
	if err != nil {
		return nil, err
	}
	return collectRequests(rows)
}

func (s *SQLiteStore) GetRequestSummaries(ctx context.Context, endpointID string, limit int) ([]*Request, error) {
	return s.GetRequestSummariesWithOffset(ctx, endpointID, limit, 0)
}

func (s *SQLiteStore) GetRequestSummariesWithOffset(ctx context.Context, endpointID string, limit, offset int) ([]*Request, error) {
	return s.SearchRequestSummaries(ctx, endpointID, "", limit, offset)
}

func (s *SQLiteStore) SearchRequestSummaries(ctx context.Context, endpointID, query string, limit, offset int) ([]*Request, error) {
	where, args := requestSearchWhere(endpointID, query)
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, endpoint_id, method, path, COALESCE(query_string, ''), COALESCE(host, ''),
			COALESCE(scheme, ''), remote_addr, COALESCE(content_length, 0),
			COALESCE(body_truncated, 0), status_code, created_at
		FROM requests WHERE `+where+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	requests := make([]*Request, 0)
	for rows.Next() {
		var request Request
		if err := rows.Scan(&request.ID, &request.EndpointID, &request.Method, &request.Path,
			&request.QueryString, &request.Host, &request.Scheme, &request.RemoteAddr,
			&request.ContentLength, &request.BodyTruncated, &request.StatusCode, &request.CreatedAt); err != nil {
			return nil, err
		}
		requests = append(requests, &request)
	}
	return requests, rows.Err()
}

func (s *SQLiteStore) SearchRequests(ctx context.Context, endpointID, query string, limit, offset int) ([]*Request, error) {
	where, args := requestSearchWhere(endpointID, query)
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, `SELECT `+requestColumns+`
		FROM requests WHERE `+where+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	return collectRequests(rows)
}

func requestSearchWhere(endpointID, query string) (string, []any) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return "endpoint_id = ?", []any{endpointID}
	}
	pattern := "%" + query + "%"
	return `endpoint_id = ? AND (
		LOWER(method) LIKE ? OR LOWER(path) LIKE ? OR LOWER(COALESCE(query_string, '')) LIKE ? OR
		LOWER(remote_addr) LIKE ? OR LOWER(headers) LIKE ? OR LOWER(CAST(body AS TEXT)) LIKE ?
	)`, []any{endpointID, pattern, pattern, pattern, pattern, pattern, pattern}
}

func collectRequests(rows *sql.Rows) ([]*Request, error) {
	defer rows.Close()
	requests := make([]*Request, 0)
	for rows.Next() {
		request, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}

func (s *SQLiteStore) CountRequests(ctx context.Context, endpointID string) (int, error) {
	return s.CountRequestsFiltered(ctx, endpointID, "")
}

func (s *SQLiteStore) CountRequestsFiltered(ctx context.Context, endpointID, query string) (int, error) {
	where, args := requestSearchWhere(endpointID, query)
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM requests WHERE "+where, args...).Scan(&count)
	return count, err
}

func (s *SQLiteStore) GetRequest(ctx context.Context, id int64) (*Request, error) {
	return scanRequest(s.db.QueryRowContext(ctx, "SELECT "+requestColumns+" FROM requests WHERE id = ?", id))
}

func (s *SQLiteStore) DeleteRequest(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM requests WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) TrimRequests(ctx context.Context, endpointID string, keep int) error {
	if keep < 1 {
		keep = 1
	}
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM requests WHERE endpoint_id = ? AND id NOT IN (
			SELECT id FROM requests WHERE endpoint_id = ? ORDER BY created_at DESC, id DESC LIMIT ?
		)
	`, endpointID, endpointID, keep)
	return err
}

func (s *SQLiteStore) Cleanup(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM endpoints WHERE expires_at < ?", time.Now())
	return err
}

func (s *SQLiteStore) GetAdminStats(ctx context.Context) (*AdminStats, error) {
	stats := &AdminStats{EndpointUsageStats: []EndpointUsageStat{}}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM endpoints").Scan(&stats.TotalEndpoints); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM requests").Scan(&stats.TotalRequests); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, COALESCE(e.alias, ''), e.created_at, COUNT(r.id), MAX(r.created_at)
		FROM endpoints e LEFT JOIN requests r ON e.id = r.endpoint_id
		GROUP BY e.id, e.alias, e.created_at
		ORDER BY COUNT(r.id) DESC, e.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var stat EndpointUsageStat
		var lastRequest any
		if err := rows.Scan(&stat.EndpointID, &stat.Alias, &stat.CreatedAt, &stat.RequestCount, &lastRequest); err != nil {
			return nil, err
		}
		if parsed := parseSQLiteTime(lastRequest); parsed != nil {
			stat.LastRequestAt = parsed
		}
		stats.EndpointUsageStats = append(stats.EndpointUsageStats, stat)
	}
	return stats, rows.Err()
}

func parseSQLiteTime(value any) *time.Time {
	if value == nil {
		return nil
	}
	if parsed, ok := value.(time.Time); ok {
		return &parsed
	}
	var raw string
	switch typed := value.(type) {
	case string:
		raw = typed
	case []byte:
		raw = string(typed)
	default:
		return nil
	}
	if index := strings.Index(raw, " m="); index >= 0 {
		raw = raw[:index]
	}
	formats := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999 -0700",
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, format := range formats {
		if parsed, err := time.Parse(format, raw); err == nil {
			return &parsed
		}
	}
	return nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
