.PHONY: fmt lint test build up down clean gen-certs

# Format code
fmt:
	go fmt ./...

# Lint code (requires golangci-lint)
lint:
	golangci-lint run ./...

# Run tests
test:
	go test -v -race -count=1 ./...

# Run tests with short flag (skip integration tests needing Redis)
test-unit:
	go test -v -race -short ./...

# Build binaries
build:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/bot-gateway ./cmd/bot-gateway
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/signing-worker ./cmd/signing-worker

# Docker compose up
up:
	docker compose up --build -d

# Docker compose down
down:
	docker compose down

# Docker compose logs
logs:
	docker compose logs -f

# Generate self-signed certs for local dev
gen-certs:
	bash deploy/nginx/gen-certs.sh

# Clean build artifacts
clean:
	rm -rf bin/
	docker compose down -v

# Setup: generate certs + copy env
setup: gen-certs
	@if [ ! -f .env ]; then cp .env.example .env; echo "Created .env from .env.example — edit it with your values!"; fi
	@mkdir -p storage
	@echo "Setup complete. Edit .env and run 'make up'"
