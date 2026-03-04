# Contributing to Signy

Thank you for your interest in contributing to Signy! This document provides guidelines and information for contributors.

## Getting Started

1. Fork the repository
2. Clone your fork
3. Create a feature branch: `git checkout -b feature/my-feature`
4. Make your changes
5. Run tests: `make test`
6. Run lint: `make lint`
7. Commit with a descriptive message
8. Push and create a Pull Request

## Development Setup

```bash
# Prerequisites
# - Docker & Docker Compose
# - Go 1.22+ (optional, for local development)

make setup          # Generate certs + .env
make up             # Start services
make test-unit      # Run unit tests
make test           # Run all tests (needs Redis)
make fmt            # Format code
make lint           # Run linter
```

## Code Standards

- **Go**: Follow [Effective Go](https://go.dev/doc/effective_go) and standard Go conventions
- **Errors**: Always handle errors — no `_` on error returns unless justified
- **Logging**: Use `slog` structured logging. Never log secrets/passwords
- **Context**: Pass `context.Context` as the first parameter to functions that do I/O
- **Tests**: Write tests for new functionality. Use table-driven tests where applicable

## Commit Messages

Use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add user notification on job completion
fix: prevent duplicate job creation on rapid button press
docs: update README with webhook setup guide
test: add integration tests for queue recovery
```

## Reporting Issues

- Use GitHub Issues
- Include: Go version, Docker version, steps to reproduce, expected vs actual behavior
- **Never include tokens, passwords, or keys in issues**

## Security

If you discover a security vulnerability, do **NOT** open an issue. See [SECURITY.md](SECURITY.md) instead.
