# PipeHooks

A lightweight, high-performance webhook inspector built with pure Go and SQLite. Branded for PipeOps.io.

## Features
- **Instant Endpoints**: Generate unique URLs with one click.
- **Real-time Dashboard**: Stream new requests instantly via SSE.
- **Compact & Technical UI**: Optimized for developer workflows.
- **Request Replay**: Easily debug integrations by replaying captured requests.
- **Persistent Storage**: Uses SQLite to keep your history safe.

## Running Locally

```bash
go run cmd/server/main.go
```
The server will be available at `https://localhost:8080`.

## Running with Docker

You can use the pre-built image from GitHub Container Registry:

```bash
docker run -p 8080:8080 \
  -v $(pwd)/data:/app/data \
  -e DATABASE_PATH=/app/data/webhook.db \
  ghcr.io/pipeopshq/pipehook:main
```

Or build it yourself:

```bash
docker build -t pipehook .
docker run -p 8080:8080 -v $(pwd)/data:/app/data pipehook
```

## Environment Variables
- `PORT`: The port to listen on (default: `8080`).
- `DATABASE_PATH`: Path to the SQLite database file (default: `webhook.db`).
- `MAX_WEBHOOK_BODY_SIZE`: Max webhook payload to store (e.g. `2MB`, `512KB`, default: `2MB`).
- `ADMIN_USERNAME`: Username for admin page basic authentication (optional, if not set, admin page is unprotected).
- `ADMIN_PASSWORD`: Password for admin page basic authentication (optional, if not set, admin page is unprotected).
