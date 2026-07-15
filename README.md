# PipeHooks

A self-hosted webhook inspector built with Go and SQLite.

## Features

- Capture every HTTP method, raw body type, headers, path, query string, host, scheme, and source address.
- Inspect text, JSON, compressed, and binary payloads without loading bodies into request history views.
- Receive live request updates over WebSockets with bounded browser history and stale-client cleanup.
- Search, replay, delete, and export requests as streaming JSON or CSV.
- Configure response status, body, content type, delay, CORS, retention, and forwarding per endpoint.
- Manage endpoints and requests through an API-key protected REST API.
- Restrict dashboards to the creating browser cookie or an authenticated administrator.

## Running Locally

```bash
go run ./cmd/server/main.go
```

The server is available at `http://localhost:8080`.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `PORT` | `8080` | HTTP listen port. |
| `DATABASE_PATH` | `webhook.db` | SQLite database path. |
| `MAX_WEBHOOK_BODY_SIZE` | `2MB` | Maximum body bytes stored per request. Larger bodies are marked as truncated. |
| `ADMIN_USERNAME` | unset | Basic-auth username for `/admin` and cross-endpoint administration. |
| `ADMIN_PASSWORD` | unset | Basic-auth password. Admin routes return `503` until both values are configured. |
| `API_KEY` | unset | Shared bearer key for `/api/v1`. API routes return `503` until configured. |
| `ALLOW_PRIVATE_FORWARDING` | `false` | Allow forwarding to loopback/private IPs. Keep disabled outside trusted local development. |

## API

Authenticate with `Authorization: Bearer $API_KEY` or `X-API-Key: $API_KEY`.

```bash
curl -X POST http://localhost:8080/api/v1/endpoints \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "alias": "payments",
    "ttl": "3months",
    "default_status": 202,
    "default_body": "accepted",
    "default_content_type": "text/plain; charset=utf-8",
    "request_limit": 1000
  }'
```

Available routes:

- `GET|POST /api/v1/endpoints`
- `GET|PUT|DELETE /api/v1/endpoints/{endpointID}`
- `GET /api/v1/endpoints/{endpointID}/requests?q=&limit=&offset=`
- `GET|DELETE /api/v1/requests/{requestID}`

The API is limited to 300 authenticated requests per minute per process. Request bodies are returned as `body_base64` so binary payloads are lossless.

## Frontend Styles

The compiled Tailwind stylesheet is embedded in the Go binary. Rebuild it after changing template classes:

```bash
npm ci
npm run build:css
```

## Docker

```bash
docker build -t pipehook .
docker run --rm -p 8080:8080 \
  -v "$(pwd)/data:/app/data" \
  -e DATABASE_PATH=/app/data/webhook.db \
  -e ADMIN_USERNAME=admin \
  -e ADMIN_PASSWORD=change-me \
  -e API_KEY=change-me-too \
  pipehook
```
