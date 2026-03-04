FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /signing-worker ./cmd/signing-worker

FROM ubuntu:22.04
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    unzip \
    curl \
    libssl3 \
    && rm -rf /var/lib/apt/lists/*

# Install zsign — update URL to a working release as needed.
# For production, build zsign from https://github.com/nicethink/zsign and copy the binary.
RUN curl -L -o /usr/local/bin/zsign \
    https://github.com/nicethink/zsign-docker/releases/download/v0.5/zsign_linux_amd64 2>/dev/null \
    && chmod +x /usr/local/bin/zsign \
    || echo "WARN: zsign binary not available from release URL. Place zsign manually at /usr/local/bin/zsign or use ZSIGN_MOCK=true."

COPY --from=builder /signing-worker /usr/local/bin/signing-worker

EXPOSE 8081
ENTRYPOINT ["signing-worker"]
