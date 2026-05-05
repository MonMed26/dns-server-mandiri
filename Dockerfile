# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-X main.version=1.0.0 -X main.buildTime=$(date -u '+%Y-%m-%d_%H:%M:%S') -s -w" \
    -o /dns-server ./cmd/dns-server

# Runtime stage - minimal image
FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN adduser -D -H -s /sbin/nologin dns-server

# Copy binary
COPY --from=builder /dns-server /usr/local/bin/dns-server

# Copy default config
COPY config.yaml /etc/dns-server/config.yaml

# Use non-root user
USER dns-server

# Expose ports
EXPOSE 53/udp 53/tcp 9153/tcp

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://localhost:9153/health || exit 1

ENTRYPOINT ["/usr/local/bin/dns-server"]
CMD ["-config", "/etc/dns-server/config.yaml"]
