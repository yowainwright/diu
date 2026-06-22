# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod ./
RUN go mod download

# Copy source code
COPY . .

# Build the Linux container binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o diu ./cmd/diu

# Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates bash curl jq

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/diu /usr/local/bin/diu

# Create directories
RUN mkdir -p /var/lib/diu/.config/diu /var/log/diu

# Copy default container config
COPY configs/docker.json /var/lib/diu/.config/diu/config.json

# Create non-root user
RUN addgroup -g 1000 diu && \
    adduser -D -u 1000 -G diu diu && \
    chown -R diu:diu /var/lib/diu /var/log/diu

USER diu

ENV HOME=/var/lib/diu
ENV DIU_DAEMON_FOREGROUND=1

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD diu daemon status || exit 1

EXPOSE 8081

ENTRYPOINT ["diu"]
CMD ["daemon", "start"]
