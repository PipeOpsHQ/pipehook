# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the application
# CGO_ENABLED=0 ensures a static binary for scratch/alpine
RUN CGO_ENABLED=0 GOOS=linux go build -o webhook ./cmd/server/main.go

# Final stage
FROM alpine:3.19

WORKDIR /app

# Create a non-root user
RUN addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser

# Copy the binary from the builder stage
COPY --from=builder /app/webhook .

# Create a data directory for the SQLite database and set ownership
RUN mkdir -p /app/data && \
    chown -R appuser:appuser /app

# Switch to non-root user
USER appuser

# Set environment variables
ENV PORT=8080
ENV DATABASE_PATH=/app/data/webhook.db

# Expose the port
EXPOSE 8080

# Command to run the application
CMD ["./webhook"]
