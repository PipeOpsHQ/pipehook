package store

import (
	"context"
	"database/sql"
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
	query := `
	CREATE TABLE IF NOT EXISTS endpoints (
		id TEXT PRIMARY KEY,
		alias TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		expires_at DATETIME
	);
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
	_, err := s.db.Exec(query)
	return err
}

func (s *SQLiteStore) CreateEndpoint(ctx context.Context, id string, alias string, ttl time.Duration) (*Endpoint, error) {
	now := time.Now()
	expiresAt := now.Add(ttl)
	_, err := s.db.ExecContext(ctx, "INSERT INTO endpoints (id, alias, created_at, expires_at) VALUES (?, ?, ?, ?)", id, alias, now, expiresAt)
	if err != nil {
		return nil, err
	}
	return &Endpoint{ID: id, Alias: alias, CreatedAt: now, ExpiresAt: expiresAt}, nil
}

func (s *SQLiteStore) GetEndpoint(ctx context.Context, id string) (*Endpoint, error) {
	var e Endpoint
	err := s.db.QueryRowContext(ctx, "SELECT id, alias, created_at, expires_at FROM endpoints WHERE id = ?", id).
		Scan(&e.ID, &e.Alias, &e.CreatedAt, &e.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *SQLiteStore) DeleteEndpoint(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM endpoints WHERE id = ?", id)
	return err
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
