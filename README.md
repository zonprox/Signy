# Signy — Telegram IPA Resigning Service

A production-hardened Telegram bot service for signing iOS IPA files. Users interact through Telegram inline menus to manage certificates and submit signing jobs.

## Architecture

| Service | Role | Port |
|---------|------|------|
| **bot-gateway** | Telegram bot, FSM, file downloads, job enqueue | 8080 |
| **signing-worker** | Queue consumer, zsign execution, artifact publishing | 8081 |
| **redis** | Reliable queue (Streams), job state, session data | 6379 |
| **nginx** | HTTPS artifact hosting, OTA manifest serving | 8443 |

## Quick Start

### Prerequisites
- Docker & Docker Compose
- A Telegram Bot Token (from [@BotFather](https://t.me/BotFather))

### 1. Create a Telegram Bot
1. Open Telegram and message [@BotFather](https://t.me/BotFather)
2. Send `/newbot` and follow prompts
3. Copy the bot token

### 2. Setup
```bash
# Clone and setup
make setup

# Edit .env with your bot token and base URL
# TELEGRAM_BOT_TOKEN=your_token_here
# BASE_URL=https://your-domain.com (use https://localhost:8443 for local)
```

### 3. Run
```bash
# Start all services
make up

# View logs
make logs

# Stop
make down
```

### 4. Test with Mock Signing
```bash
# Set ZSIGN_MOCK=true in .env for testing without real zsign
ZSIGN_MOCK=true make up
```

## Telegram Bot Usage

### Main Menu (`/start`)
```
[➕ New Signing Job] [🪪 Certificates]
[🧾 My Jobs]         [⚙ Settings]
[❓ Help]
```

### Certificate Management
The bot supports up to 3 certificate sets per user (configurable).

#### Add Certificate Set
`🪪 Certificates` → `➕ Add Cert Set`
1. Enter a name (2-32 chars)
2. Upload `.p12` file
3. Send P12 password (auto-deleted from chat)
4. Upload `.mobileprovision` file
5. Certificate is validated and stored

#### Set Default
`🪪 Certificates` → `⭐ Set Default`
- Select from your cert sets; the default is used for new signing jobs

#### Check Status
`🪪 Certificates` → `✅ Check Status`
- Validates file existence, decryption, and provision structure

#### Delete
`🪪 Certificates` → `🗑 Delete`
- Select a cert set and confirm deletion
- Files are hard-deleted from disk

### Signing Flow
`➕ New Signing Job` →
1. Choose cert set (use default or pick another)
2. Upload IPA file
3. Review summary and confirm
4. Receive progress updates: `QUEUED → SIGNING → PUBLISHING → DONE`
5. Get OTA install link

## Webhook vs Polling

| Mode | Config | Use Case |
|------|--------|----------|
| **Polling** (default) | `TELEGRAM_MODE=polling` | Local dev, simple setup |
| **Webhook** | `TELEGRAM_MODE=webhook` + `TELEGRAM_WEBHOOK_URL` | Production, lower latency |

For webhook mode, the bot must be reachable at the webhook URL over HTTPS.

## OTA Installation Requirements

iOS OTA (Over-The-Air) installation requires:
1. **HTTPS** with a valid TLS certificate (not self-signed for production)
2. The `BASE_URL` must be publicly accessible
3. Correct `Content-Type` headers for `.ipa` and `.plist` (handled by Nginx config)

For local testing, use `itms-services://` links won't work without a real HTTPS endpoint. Use a tunnel (e.g., ngrok) or deploy to a server.

## Security

### MASTER_KEY (Recommended)
Set `MASTER_KEY` in `.env` (generate with `openssl rand -hex 32`):
- P12 files encrypted at rest (AES-256-GCM)
- P12 passwords encrypted at rest
- Key derived using HKDF-SHA256

### Without MASTER_KEY
- P12 files stored with `chmod 600`
- Passwords are **not stored** — the bot asks for the password each time a job is created
- Passwords are transmitted to the worker via short-lived encrypted Redis tokens (10min TTL)
- **Not recommended for production**

### General
- Passwords never appear in logs
- Password messages auto-deleted from Telegram chat
- Redis session tokens are one-time-use with TTL

## Configuration

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `TELEGRAM_BOT_TOKEN` | ✅ | | Bot token from BotFather |
| `BASE_URL` | ✅ | | Public HTTPS URL for artifact downloads |
| `TELEGRAM_MODE` | | `polling` | `polling` or `webhook` |
| `TELEGRAM_WEBHOOK_URL` | webhook only | | Webhook endpoint URL |
| `REDIS_URL` | | `redis://redis:6379/0` | Redis connection URL |
| `STORAGE_PATH` | | `/storage` | Base storage path |
| `MASTER_KEY` | | | AES master key (hex-encoded, 32 bytes) |
| `MAX_IPA_MB` | | `500` | Max IPA file size in MB |
| `MAX_P12_MB` | | `20` | Max P12 file size in MB |
| `MAX_PROV_KB` | | `512` | Max provision file size in KB |
| `MAX_CERTSETS_PER_USER` | | `3` | Max cert sets per user |
| `WORKER_CONCURRENCY` | | `2` | Global worker goroutine pool size |
| `USER_CONCURRENCY` | | `1` | Max concurrent jobs per user |
| `JOB_TIMEOUT_SIGNING_SECONDS` | | `900` | zsign process timeout |
| `VISIBILITY_TIMEOUT_SECONDS` | | `600` | Queue message visibility timeout |
| `RETENTION_DAYS_DEFAULT` | | `7` | Days to keep artifacts |

## Development

```bash
make fmt        # Format code
make lint       # Run golangci-lint
make test       # Run all tests (needs Redis)
make test-unit  # Run unit tests only
make build      # Build binaries to bin/
```

## Troubleshooting

| Problem | Solution |
|---------|----------|
| Provision mismatch | Ensure P12 and mobileprovision are from the same team/profile |
| Invalid P12 | Verify the P12 password is correct |
| OTA install fails | Check `BASE_URL` is HTTPS and publicly accessible |
| zsign not found | Ensure zsign binary is in the worker container at `/usr/local/bin/zsign` |
| Redis connection refused | Check `REDIS_URL` and ensure Redis is running |
| Job stuck in SIGNING | Worker may have crashed; it will auto-recover via XAUTOCLAIM after visibility timeout |
| DECRYPT_FAIL status | `MASTER_KEY` may have changed since cert was stored; re-upload cert |

## Production Deployment Notes

1. **TLS**: Use Let's Encrypt with certbot or a reverse proxy (Cloudflare, AWS ALB)
2. **Redis**: Use Redis with persistence (AOF) and authentication
3. **Storage**: Mount a persistent volume for `/storage`
4. **Monitoring**: Scrape `/metrics` endpoints with Prometheus
5. **Secrets**: Use Docker secrets or Vault for `TELEGRAM_BOT_TOKEN` and `MASTER_KEY`
6. **zsign**: Build zsign from source for your target platform or provide the binary
