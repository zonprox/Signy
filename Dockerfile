# ── Stage 1: Build signy binary ─────────────────────────────────────
FROM golang:1.24-alpine AS go-builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /signy ./cmd/signy

# ── Stage 2: Runtime ─────────────────────────────────────────────────
FROM ubuntu:22.04

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl libssl3 unzip \
    && rm -rf /var/lib/apt/lists/*

# Install zsign v0.7 pre-built binary (official GitHub release)
# URL is pinned to a fixed tag — will not 404.
RUN curl -fsSL -o /tmp/zsign.zip \
    https://github.com/zhlynn/zsign/releases/download/v0.7/zsign-v0.7-ubuntu-x64.zip \
    && unzip /tmp/zsign.zip -d /tmp/zsign \
    && install -m 755 /tmp/zsign/zsign /usr/local/bin/zsign \
    && rm -rf /tmp/zsign /tmp/zsign.zip

# Copy signy binary
COPY --from=go-builder /signy /usr/local/bin/signy

# Storage directory
RUN mkdir -p /storage

COPY entrypoint.sh /tmp/entrypoint.sh
RUN sed -i 's/\r$//' /tmp/entrypoint.sh && \
    cp /tmp/entrypoint.sh /entrypoint.sh && \
    chmod +x /entrypoint.sh && \
    rm /tmp/entrypoint.sh

HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD curl -sf http://localhost:7890/healthz || exit 1

EXPOSE 7890

ENTRYPOINT ["/entrypoint.sh"]
