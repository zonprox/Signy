.PHONY: fmt lint test test-unit build up down logs clean setup gen-master-key

# Format code
fmt:
	go fmt ./...

# Run linter (requires golangci-lint in PATH)
lint:
	golangci-lint run ./...

# Run all tests (requires Redis running)
test:
	go test -v -race -count=1 ./...

# Run unit tests only (no Redis needed)
test-unit:
	go test -v -race -short ./...

# Build binary to bin/signy
build:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/signy ./cmd/signy

# Build frontend UI assets
build-ui:
	npm install
	npm run build:css

# Start dev stack (builds from source, ZSIGN_MOCK=true by default)
up:
	docker compose up --build -d

# Stop dev stack
down:
	docker compose down

# Follow container logs
logs:
	docker compose logs -f

# Remove build artifacts and volumes
clean:
	rm -rf bin/
	docker compose down -v

# Copy .env.example → .env and create storage directory
setup:
	@if [ ! -f .env ]; then cp .env.example .env && echo "Created .env — fill in TELEGRAM_BOT_TOKEN, TELEGRAM_API_ID, TELEGRAM_API_HASH, BASE_URL"; fi
	@mkdir -p storage
	@echo "Setup complete. Run 'make up' to start."

# Generate a secure MASTER_KEY
gen-master-key:
	@echo "MASTER_KEY=$$(openssl rand -hex 32)"
