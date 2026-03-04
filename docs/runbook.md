# Runbook

Operational procedures for Signy IPA Signing Service.

## Service Health

### Check service health
```bash
# Bot gateway
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz

# Signing worker
curl http://localhost:8081/healthz
curl http://localhost:8081/readyz
```

### View metrics
```bash
curl http://localhost:8080/metrics  # Bot metrics
curl http://localhost:8081/metrics  # Worker metrics
```

Key metrics to monitor:
- `signy_jobs_in_flight` — currently processing jobs
- `signy_queue_depth` — pending jobs
- `signy_jobs_total{status="failed"}` — failed job count
- `signy_redis_errors_total` — Redis errors

## Rotate Telegram Bot Token

1. Generate a new token via [@BotFather](https://t.me/BotFather) → `/revoke`
2. Update `TELEGRAM_BOT_TOKEN` in `.env`
3. Restart bot-gateway:
   ```bash
   docker compose restart bot-gateway
   ```

## Rotate MASTER_KEY

> **WARNING**: Changing MASTER_KEY will make all existing encrypted cert sets unreadable.

1. Notify affected users to re-upload their certificates
2. Delete existing cert set directories: `rm -rf storage/users/*/certsets/*/p12*.enc`
3. Update `MASTER_KEY` in `.env`
4. Restart both services:
   ```bash
   docker compose restart bot-gateway signing-worker
   ```

## Debug Failed Jobs

### 1. Check job status in Redis
```bash
docker compose exec redis redis-cli HGETALL signy:job:<job_id>
```

### 2. Check sign log
```bash
cat storage/artifacts/<job_id>/sign.log
```

### 3. Check event timeline
```bash
cat storage/artifacts/<job_id>/events.jsonl
```

### 4. Check worker logs
```bash
docker compose logs signing-worker | grep <job_id>
```

### Common failure reasons
| Error Code | Cause | Fix |
|-----------|-------|-----|
| `SIGN_ERROR` | zsign failed | Check sign.log for details |
| `NO_PASSWORD` | Password token expired | User must start a new job |
| `MAX_RETRIES` | Job failed 3 times | Investigate sign.log and retry manually |
| `DECRYPT_FAIL` | MASTER_KEY changed | User must re-upload cert set |

## Clean Storage Manually

### Remove old artifacts (keeping recent)
```bash
find storage/artifacts -maxdepth 1 -mtime +7 -type d -exec rm -rf {} \;
```

### Remove all incoming files
```bash
find storage/users/*/incoming -type f -mtime +1 -delete
```

### Check storage usage
```bash
du -sh storage/
du -sh storage/artifacts/
du -sh storage/users/
```

## Redis Operations

### View queue info
```bash
docker compose exec redis redis-cli XINFO STREAM signy:jobs:stream
docker compose exec redis redis-cli XINFO GROUPS signy:jobs:stream
docker compose exec redis redis-cli XPENDING signy:jobs:stream signing-workers
```

### Clear stuck messages
```bash
# ACK a specific message
docker compose exec redis redis-cli XACK signy:jobs:stream signing-workers <msg_id>

# View pending messages
docker compose exec redis redis-cli XPENDING signy:jobs:stream signing-workers - + 10
```

### View job data
```bash
# List recent jobs for a user
docker compose exec redis redis-cli LRANGE signy:user_jobs:<user_id> 0 9

# Get job details
docker compose exec redis redis-cli HGETALL signy:job:<job_id>
```

## Backup & Restore

### Backup
```bash
# Redis
docker compose exec redis redis-cli BGSAVE
cp storage/redis/dump.rdb backup/

# Storage
tar -czf backup/storage-$(date +%Y%m%d).tar.gz storage/
```

### Restore
```bash
docker compose down
cp backup/dump.rdb storage/redis/
tar -xzf backup/storage-*.tar.gz
docker compose up -d
```

## Scaling

The signing-worker supports horizontal scaling:
```bash
docker compose up --scale signing-worker=3 -d
```

Each worker instance uses a unique consumer name and participates in the same Redis Stream consumer group. Work is distributed automatically.

**Note**: When scaling without MASTER_KEY, ephemeral password tokens are encrypted with per-process keys. This means each job must be processed by the same worker instance that received the password. Use MASTER_KEY in multi-worker deployments.
