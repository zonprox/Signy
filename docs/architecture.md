# Signy — Architecture

## Overview

Signy is a single-binary Go application that combines a Telegram bot gateway and an async signing worker. It runs alongside Redis, a local Telegram Bot API server, and an optional Caddy reverse proxy.

## Component Diagram

```
Telegram Cloud
      │
      ▼
telegram-bot-api:8081   ← local Bot API server (removes 20 MB limit)
      │
      ▼
   signy:7890
  ┌────────────────────────────────────┐
  │  Bot (polling)   │  Web Server     │
  │  ─────────────── │  ──────────────  │
  │  /start, /help   │  /jobs/{id}     │
  │  FSM sessions    │  /              │
  │  cert upload     │  /healthz       │
  │  IPA upload      │  /readyz        │
  │                  │                 │
  │  ─────── Redis Streams ─────────   │
  │  Enqueue jobs ──► Worker pool      │
  │                   └─ zsign          │
  └────────────────────────────────────┘
         │ artifacts
         ▼
   /storage (named volume)
         │
         ▼
   Caddy (optional)  ← TLS termination + artifact file server
         │
         ▼
   iOS devices (OTA install via itms-services://)
```

## Services

### telegram-bot-api

- Image: `aiogram/telegram-bot-api`
- Removes Telegram's 20 MB download limit (supports up to 2 GB)
- Signy connects to `http://telegram-bot-api:8082` when `TELEGRAM_API_ID` is set

### signy

Single binary running two goroutines:

| Goroutine   | Function                                                             |
| ----------- | -------------------------------------------------------------------- |
| Bot         | Polls Telegram, manages FSM sessions, enqueues jobs                  |
| Worker pool | Consumes Redis Stream, calls zsign, updates job state                |
| Web server  | Serves `/jobs/{id}` download page, admin dashboard, health endpoints |

### redis

- Job queue: Redis Streams (`signy:jobs`)
- Job state: Redis Hash per job (`signy:job:{id}`)
- Session state: Redis Hash per user FSM
- Pub/Sub: `signy:job:events` for real-time bot notifications

### caddy _(optional)_

- Automatic TLS via Let's Encrypt (domain) or self-signed (IP)
- Serves `/artifacts/{job_id}/signed.ipa` and `manifest.plist` directly from volume
- Proxies everything else to `signy:7890`

## Data Flow — Signing Job

```
1. User sends IPA file via Telegram
2. Bot downloads file, saves to /storage/ipa/{job_id}.ipa
3. Bot calls JobManager.Create() → writes to Redis Hash + enqueues to Stream
4. Bot sends "queued" message to user
5. Worker reads job from Stream
6. Worker calls zsign with P12 + provisioning profile
7. Worker writes signed.ipa + manifest.plist to /storage/artifacts/{job_id}/
8. Worker publishes to signy:job:events
9. Bot receives event, sends Telegram notification with inline button
10. User opens /jobs/{job_id} download page → OTA install or download
```

## Storage Layout

```
/storage/
├── ipa/              # uploaded (unsigned) IPA files
├── artifacts/
│   └── {job_id}/
│       ├── signed.ipa
│       └── manifest.plist
└── certs/
    └── {user_id}/
        └── {certset_id}/
            ├── cert.p12  (encrypted with MASTER_KEY if set)
            └── profile.mobileprovision
```

## Security Notes

- P12 files are AES-256 encrypted at rest when `MASTER_KEY` is set
- Job IDs are UUIDs (128-bit entropy) — the ID itself is the access token for `/jobs/{id}`
- Admin dashboard protected with HTTP Basic Auth when `ADMIN_PASSWORD` is set
- Caddy handles TLS; HTTP-only mode is available for internal deployments
