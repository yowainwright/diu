# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary for macOS (for testing in Docker on Mac)
# Note: When running on Mac, we'll use the native binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o diu ./cmd/diu

# Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates bash curl jq

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/diu /usr/local/bin/diu

# Copy default config
COPY configs/default.yaml /etc/diu/config.yaml

# Create directories
RUN mkdir -p /var/lib/diu /var/log/diu

# Create non-root user
RUN addgroup -g 1000 diu && \
    adduser -D -u 1000 -G diu diu && \
    chown -R diu:diu /var/lib/diu /var/log/diu /etc/diu

USER diu

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD diu daemon status || exit 1

EXPOSE 8080 8081

ENTRYPOINT ["diu"]
CMD ["daemon", "start"]