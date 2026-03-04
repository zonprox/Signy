FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bot-gateway ./cmd/bot-gateway

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app && adduser -S app -G app
COPY --from=builder /bot-gateway /usr/local/bin/bot-gateway

USER app
EXPOSE 8080
ENTRYPOINT ["bot-gateway"]
