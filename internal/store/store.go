package store

import (
	"context"
	"time"
)

const (
	DefaultResponseStatus      = 200
	DefaultResponseBody        = "ok"
	DefaultResponseContentType = "text/plain; charset=utf-8"
	DefaultRequestLimit        = 1000
	MaxRequestLimit            = 10000
	MaxResponseDelayMS         = 30000
)

type Endpoint struct {
	ID                 string    `json:"id"`
	Alias              string    `json:"alias"`
	CreatorID          string    `json:"creator_id"`
	CreatedAt          time.Time `json:"created_at"`
	ExpiresAt          time.Time `json:"expires_at"`
	DefaultStatus      int       `json:"default_status"`
	DefaultBody        string    `json:"default_body"`
	DefaultContentType string    `json:"default_content_type"`
	ResponseDelayMS    int       `json:"response_delay_ms"`
	EnableCORS         bool      `json:"enable_cors"`
	ForwardURL         string    `json:"forward_url"`
	RequestLimit       int       `json:"request_limit"`
}

type EndpointSettings struct {
	Alias              string        `json:"alias"`
	TTL                time.Duration `json:"-"`
	DefaultStatus      int           `json:"default_status"`
	DefaultBody        string        `json:"default_body"`
	DefaultContentType string        `json:"default_content_type"`
	ResponseDelayMS    int           `json:"response_delay_ms"`
	EnableCORS         bool          `json:"enable_cors"`
	ForwardURL         string        `json:"forward_url"`
	RequestLimit       int           `json:"request_limit"`
}

func DefaultEndpointSettings() EndpointSettings {
	return EndpointSettings{
		TTL:                DefaultTTL,
		DefaultStatus:      DefaultResponseStatus,
		DefaultBody:        DefaultResponseBody,
		DefaultContentType: DefaultResponseContentType,
		RequestLimit:       DefaultRequestLimit,
	}
}

type Request struct {
	ID            int64     `json:"id"`
	EndpointID    string    `json:"endpoint_id"`
	Method        string    `json:"method"`
	Path          string    `json:"path"`
	QueryString   string    `json:"query_string"`
	Host          string    `json:"host"`
	Scheme        string    `json:"scheme"`
	RemoteAddr    string    `json:"remote_addr"`
	Headers       string    `json:"headers"`
	Body          []byte    `json:"body"`
	ContentLength int64     `json:"content_length"`
	BodyTruncated bool      `json:"body_truncated"`
	StatusCode    int       `json:"status_code"`
	CreatedAt     time.Time `json:"created_at"`
}

const (
	TTL1Week   = 7 * 24 * time.Hour
	TTL1Month  = 30 * 24 * time.Hour
	TTL3Months = 90 * 24 * time.Hour
	TTL6Months = 180 * 24 * time.Hour
	DefaultTTL = TTL3Months
)

type Store interface {
	CreateEndpoint(ctx context.Context, id string, alias string, creatorID string, ttl time.Duration) (*Endpoint, error)
	GetEndpoint(ctx context.Context, id string) (*Endpoint, error)
	UpdateEndpoint(ctx context.Context, id string, alias string, ttl time.Duration) error
	UpdateEndpointSettings(ctx context.Context, id string, settings EndpointSettings) error
	DeleteEndpoint(ctx context.Context, id string) error
	ListEndpoints(ctx context.Context, creatorID string, limit int) ([]*Endpoint, error)
	ListAllEndpoints(ctx context.Context, limit int, offset int) ([]*Endpoint, error)

	SaveRequest(ctx context.Context, req *Request) error
	GetRequests(ctx context.Context, endpointID string, limit int) ([]*Request, error)
	GetRequestsWithOffset(ctx context.Context, endpointID string, limit int, offset int) ([]*Request, error)
	GetRequestSummaries(ctx context.Context, endpointID string, limit int) ([]*Request, error)
	GetRequestSummariesWithOffset(ctx context.Context, endpointID string, limit int, offset int) ([]*Request, error)
	SearchRequestSummaries(ctx context.Context, endpointID string, query string, limit int, offset int) ([]*Request, error)
	SearchRequests(ctx context.Context, endpointID string, query string, limit int, offset int) ([]*Request, error)
	CountRequests(ctx context.Context, endpointID string) (int, error)
	CountRequestsFiltered(ctx context.Context, endpointID string, query string) (int, error)
	GetRequest(ctx context.Context, id int64) (*Request, error)
	DeleteRequest(ctx context.Context, id int64) error
	TrimRequests(ctx context.Context, endpointID string, keep int) error

	Cleanup(ctx context.Context) error
	GetAdminStats(ctx context.Context) (*AdminStats, error)
}

type AdminStats struct {
	TotalEndpoints     int                 `json:"total_endpoints"`
	TotalRequests      int                 `json:"total_requests"`
	EndpointUsageStats []EndpointUsageStat `json:"endpoint_usage_stats"`
}

type EndpointUsageStat struct {
	EndpointID    string     `json:"endpoint_id"`
	Alias         string     `json:"alias"`
	RequestCount  int        `json:"request_count"`
	CreatedAt     time.Time  `json:"created_at"`
	LastRequestAt *time.Time `json:"last_request_at,omitempty"`
}
