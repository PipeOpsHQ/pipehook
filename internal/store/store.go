package store

import (
	"context"
	"time"
)

type Endpoint struct {
	ID        string    `json:"id"`
	Alias     string    `json:"alias"`
	CreatorID string    `json:"creator_id"` // Browser fingerprint ID
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Request struct {
	ID         int64     `json:"id"`
	EndpointID string    `json:"endpoint_id"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	RemoteAddr string    `json:"remote_addr"`
	Headers    string    `json:"headers"` // JSON string
	Body       []byte    `json:"body"`
	StatusCode int       `json:"status_code"`
	CreatedAt  time.Time `json:"created_at"`
}

type Store interface {
	CreateEndpoint(ctx context.Context, id string, alias string, creatorID string, ttl time.Duration) (*Endpoint, error)
	GetEndpoint(ctx context.Context, id string) (*Endpoint, error)
	DeleteEndpoint(ctx context.Context, id string) error
	ListEndpoints(ctx context.Context, creatorID string, limit int) ([]*Endpoint, error)

	SaveRequest(ctx context.Context, req *Request) error
	GetRequests(ctx context.Context, endpointID string, limit int) ([]*Request, error)
	GetRequestsWithOffset(ctx context.Context, endpointID string, limit int, offset int) ([]*Request, error)
	CountRequests(ctx context.Context, endpointID string) (int, error)
	GetRequest(ctx context.Context, id int64) (*Request, error)
	DeleteRequest(ctx context.Context, id int64) error

	Cleanup(ctx context.Context) error
}
