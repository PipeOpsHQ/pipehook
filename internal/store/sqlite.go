package store

import (
	"context"
	"database/sql"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, err
	}

	s := &SQLiteStore{db: db}
	if err := s.init(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *SQLiteStore) init() error {
	// Check if we are in read-only mode
	var readOnly int
	err := s.db.QueryRow("PRAGMA query_only;").Scan(&readOnly)
	if err == nil && readOnly == 1 {
		log.Printf("CRITICAL WARNING: Database is opened in READ-ONLY mode!")
	}

	// Enable WAL mode and other optimizations
	// Note: WAL might fail on some network filesystems (NFS), so we log but don't hard fail
	if _, err := s.db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		log.Printf("Warning: Failed to enable WAL mode: %v", err)
	}
	_, _ = s.db.Exec("PRAGMA synchronous=NORMAL;")
	_, _ = s.db.Exec("PRAGMA foreign_keys=ON;")
	_, _ = s.db.Exec("PRAGMA busy_timeout=5000;")

	query := `
	CREATE TABLE IF NOT EXISTS endpoints (
		id TEXT PRIMARY KEY,
		alias TEXT,
		creator_id TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		expires_at DATETIME
	);
	CREATE INDEX IF NOT EXISTS idx_endpoints_creator_id ON endpoints(creator_id);
	CREATE TABLE IF NOT EXISTS requests (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		endpoint_id TEXT,
		method TEXT,
		path TEXT,
		remote_addr TEXT,
		headers TEXT,
		body BLOB,
		status_code INTEGER,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(endpoint_id) REFERENCES endpoints(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_requests_endpoint_id ON requests(endpoint_id);
	`
	// Migration: Add creator_id column if it doesn't exist
	s.db.Exec("ALTER TABLE endpoints ADD COLUMN creator_id TEXT")
	_, err = s.db.Exec(query)
	if err != nil {
		log.Printf("Database schema initialization failed: %v", err)
	}
	return err
}

func (s *SQLiteStore) CreateEndpoint(ctx context.Context, id string, alias string, creatorID string, ttl time.Duration) (*Endpoint, error) {
	now := time.Now()
	expiresAt := now.Add(ttl)
	_, err := s.db.ExecContext(ctx, "INSERT INTO endpoints (id, alias, creator_id, created_at, expires_at) VALUES (?, ?, ?, ?, ?)", id, alias, creatorID, now, expiresAt)
	if err != nil {
		return nil, err
	}
	return &Endpoint{ID: id, Alias: alias, CreatorID: creatorID, CreatedAt: now, ExpiresAt: expiresAt}, nil
}

func (s *SQLiteStore) GetEndpoint(ctx context.Context, id string) (*Endpoint, error) {
	var e Endpoint
	err := s.db.QueryRowContext(ctx, "SELECT id, alias, COALESCE(creator_id, ''), created_at, expires_at FROM endpoints WHERE id = ?", id).
		Scan(&e.ID, &e.Alias, &e.CreatorID, &e.CreatedAt, &e.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *SQLiteStore) UpdateEndpoint(ctx context.Context, id string, alias string, ttl time.Duration) error {
	// Get current endpoint to calculate new expiry from creation time
	var createdAt time.Time
	err := s.db.QueryRowContext(ctx, "SELECT created_at FROM endpoints WHERE id = ?", id).Scan(&createdAt)
	if err != nil {
		return err
	}
	newExpiresAt := createdAt.Add(ttl)
	// Don't allow expiry in the past - use at least now + 1 day
	if newExpiresAt.Before(time.Now()) {
		newExpiresAt = time.Now().Add(24 * time.Hour)
	}
	_, err = s.db.ExecContext(ctx, "UPDATE endpoints SET alias = ?, expires_at = ? WHERE id = ?", alias, newExpiresAt, id)
	return err
}

func (s *SQLiteStore) DeleteEndpoint(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM endpoints WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) ListEndpoints(ctx context.Context, creatorID string, limit int) ([]*Endpoint, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, alias, COALESCE(creator_id, ''), created_at, expires_at
		FROM endpoints
		WHERE creator_id = ? AND expires_at > ?
		ORDER BY created_at DESC
		LIMIT ?
	`, creatorID, time.Now(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var endpoints []*Endpoint
	for rows.Next() {
		var e Endpoint
		err := rows.Scan(&e.ID, &e.Alias, &e.CreatorID, &e.CreatedAt, &e.ExpiresAt)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, &e)
	}
	return endpoints, nil
}

func (s *SQLiteStore) SaveRequest(ctx context.Context, req *Request) error {
	now := time.Now()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO requests (endpoint_id, method, path, remote_addr, headers, body, status_code, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, req.EndpointID, req.Method, req.Path, req.RemoteAddr, req.Headers, req.Body, req.StatusCode, now)
	if err != nil {
		return err
	}
	req.ID, _ = res.LastInsertId()
	req.CreatedAt = now
	return nil
}

func (s *SQLiteStore) GetRequests(ctx context.Context, endpointID string, limit int) ([]*Request, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, endpoint_id, method, path, remote_addr, headers, body, status_code, created_at
		FROM requests
		WHERE endpoint_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, endpointID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reqs []*Request
	for rows.Next() {
		var r Request
		err := rows.Scan(&r.ID, &r.EndpointID, &r.Method, &r.Path, &r.RemoteAddr, &r.Headers, &r.Body, &r.StatusCode, &r.CreatedAt)
		if err != nil {
			return nil, err
		}
		reqs = append(reqs, &r)
	}
	return reqs, nil
}

func (s *SQLiteStore) GetRequestsWithOffset(ctx context.Context, endpointID string, limit int, offset int) ([]*Request, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, endpoint_id, method, path, remote_addr, headers, body, status_code, created_at
		FROM requests
		WHERE endpoint_id = ?
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`, endpointID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reqs []*Request
	for rows.Next() {
		var r Request
		err := rows.Scan(&r.ID, &r.EndpointID, &r.Method, &r.Path, &r.RemoteAddr, &r.Headers, &r.Body, &r.StatusCode, &r.CreatedAt)
		if err != nil {
			return nil, err
		}
		reqs = append(reqs, &r)
	}
	return reqs, nil
}

func (s *SQLiteStore) CountRequests(ctx context.Context, endpointID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM requests WHERE endpoint_id = ?`, endpointID).Scan(&count)
	return count, err
}

func (s *SQLiteStore) GetRequest(ctx context.Context, id int64) (*Request, error) {
	var r Request
	err := s.db.QueryRowContext(ctx, `
		SELECT id, endpoint_id, method, path, remote_addr, headers, body, status_code, created_at
		FROM requests
		WHERE id = ?
	`, id).Scan(&r.ID, &r.EndpointID, &r.Method, &r.Path, &r.RemoteAddr, &r.Headers, &r.Body, &r.StatusCode, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *SQLiteStore) DeleteRequest(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM requests WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) Cleanup(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM endpoints WHERE expires_at < ?", time.Now())
	return err
}
