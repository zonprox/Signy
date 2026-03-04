# Changelog

All notable changes to Signy will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Initial project setup with bot-gateway and signing-worker services
- Telegram bot with inline keyboard menus (no commands needed)
- Certificate set management (Create, Set Default, Check Status, Delete)
- IPA signing job creation with progress tracking
- Redis Streams reliable queue with XAUTOCLAIM crash recovery
- AES-256-GCM + HKDF-SHA256 at-rest encryption for P12 and passwords
- Per-user and global concurrency limits
- Rate limiting and callback debouncing
- Prometheus metrics and structured `slog` logging
- Health (`/healthz`), readiness (`/readyz`), and metrics (`/metrics`) endpoints
- OTA manifest.plist generation for iOS installation
- Nginx HTTPS artifact hosting with correct MIME types
- Docker Compose setup with Redis, Nginx, and both Go services
- Periodic cleanup of expired artifacts and incoming files
- ZSIGN_MOCK mode for testing without real zsign binary
- Comprehensive unit tests (crypto, FSM, queue, manifest)
- Full documentation (README, architecture, runbook)
