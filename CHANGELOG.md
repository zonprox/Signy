# Changelog

All notable changes to Signy will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.0] - 2026-03-04

### Added
- Single-container architecture with embedded Redis (pull once, run immediately)
- Auto-detection of BASE_URL from server public IP
- Telegram bot with inline keyboard menus (no commands needed)
- Certificate set management (Create, Set Default, Check Status, Delete)
- IPA signing job creation with real-time progress tracking
- Redis Streams reliable queue with XAUTOCLAIM crash recovery
- AES-256-GCM + HKDF-SHA256 at-rest encryption for P12 and passwords
- Per-user and global concurrency limits
- Rate limiting and callback debouncing
- Prometheus metrics and structured `slog` logging
- Health (`/healthz`), readiness (`/readyz`), and metrics (`/metrics`) endpoints
- OTA manifest.plist generation for iOS installation
- GitHub Actions CI/CD (lint, test, build & push to GHCR)
- Periodic cleanup of expired artifacts and incoming files
- ZSIGN_MOCK mode for testing without real zsign binary
- Comprehensive unit tests (crypto, FSM, queue, manifest)
- Full documentation (README, architecture, runbook)

### Tech Stack
- Go 1.24, go-redis v9.18, Prometheus v1.23.2
- Docker with multi-stage build
- golangci-lint with errcheck, govet, staticcheck
