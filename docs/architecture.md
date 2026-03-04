# Architecture

## System Overview

```
                          ┌──────────────────┐
                          │   Telegram API    │
                          └────────┬─────────┘
                                   │
                          ┌────────▼─────────┐
                          │   bot-gateway     │
                          │   (Go service)    │
                          │                   │
                          │  • Telegram FSM   │
                          │  • File downloads │
                          │  • Cert CRUD      │
                          │  • Job enqueue    │
                          │  • :8080 health   │
                          └────────┬─────────┘
                                   │
                          ┌────────▼─────────┐
                          │      Redis        │
                          │                   │
                          │  • Streams queue  │
                          │  • Job state hash │
                          │  • User locks     │
                          │  • Session tokens │
                          │  • Pub/Sub events │
                          └────────┬─────────┘
                                   │
                          ┌────────▼─────────┐
                          │  signing-worker   │
                          │   (Go service)    │
                          │                   │
                          │  • Stream consume │
                          │  • zsign execute  │
                          │  • Manifest gen   │
                          │  • Cleanup loop   │
                          │  • :8081 health   │
                          └────────┬─────────┘
                                   │
                          ┌────────▼─────────┐
                          │     Nginx         │
                          │                   │
                          │  • HTTPS :8443    │
                          │  • /artifacts/*   │
                          │  • OTA manifests  │
                          └──────────────────┘
```

## Data Flow

### Signing Job Flow
```
1. User sends /start → Bot shows inline menu
2. User taps [➕ New Signing Job]
3. Bot: "Choose cert" → User selects default or another
4. Bot: "Send IPA" → User uploads .ipa
5. Bot: Shows summary → User confirms [✅ Start Signing]
6. Bot:
   a. Streams IPA to /storage/users/<uid>/incoming/
   b. Creates job record in Redis hash
   c. XADD to Redis Stream
7. Worker:
   a. XREADGROUP picks up message
   b. Acquires per-user lock
   c. Updates status → SIGNING (Redis + Pub/Sub)
   d. Decrypts P12 if MASTER_KEY present
   e. Runs: zsign -k p12 -p pass -m prov -o signed.ipa input.ipa
   f. Generates manifest.plist
   g. Updates status → DONE (Redis + Pub/Sub)
   h. XACK message
8. Bot receives Pub/Sub event → Sends user notification with install link
```

### Reliable Queue (Redis Streams)
```
Producer (bot):     XADD signy:jobs:stream * job_id ... 
Consumer (worker):  XREADGROUP GROUP signing-workers worker-1 ... > 
On success:         XACK signy:jobs:stream signing-workers <msg_id>
Crash recovery:     XAUTOCLAIM every 30s for idle > visibility_timeout
```

### Certificate Storage
```
/storage/users/<uid>/
├── certsets/
│   └── <set_id>/
│       ├── meta.json           # CertSet metadata
│       ├── p12.enc             # Encrypted P12 (if MASTER_KEY)
│       ├── p12.p12             # Plain P12 (if no MASTER_KEY)
│       ├── p12pass.enc         # Encrypted password (if MASTER_KEY)
│       └── provision.mobileprovision
├── default_set_id.txt          # Current default cert set ID
└── incoming/
    └── <timestamp>_<file_id>.ipa

/storage/artifacts/<job_id>/
├── signed.ipa
├── manifest.plist
├── sign.log
├── events.jsonl
└── meta.json
```

### Encryption Model (MASTER_KEY present)
```
MASTER_KEY (env var)
    │
    ├─ HKDF-SHA256("p12-<uid>-<setid>") → AES-256-GCM key → p12.enc
    └─ HKDF-SHA256("pass-<uid>-<setid>") → AES-256-GCM key → p12pass.enc

Each file stores: nonce (12 bytes) || ciphertext (variable)
```

### Encryption Model (no MASTER_KEY)
```
Per-process random key (32 bytes, in memory only)
    │
    └─ AES-256-GCM → ephemeral token in Redis (TTL 10min)
       Worker retrieves and decrypts before signing
```

## Concurrency Model
- **Global**: Worker pool limited by WORKER_CONCURRENCY semaphore
- **Per-user**: Redis SETNX lock with TTL (1 concurrent job per user by default)
- **Idempotency**: Terminal jobs (DONE/FAILED) are skipped on re-delivery
- **Dedup**: Bot uses SETNX with 5min TTL to prevent duplicate job creation
