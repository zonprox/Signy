# Signy — Runbook

## Health Checks

```bash
# Is signy running?
curl http://YOUR_IP:7890/healthz

# Is Redis connected?
curl http://YOUR_IP:7890/readyz

# Container status
docker ps --filter name=signy
```

## Common Operations

### View logs

```bash
# All services
docker compose logs -f

# Signy only
docker logs -f signy

# telegram-bot-api
docker logs -f signy-tg-api
```

### Restart a service

```bash
docker restart signy
docker restart signy-tg-api
docker restart signy-redis
```

### Update to latest image

```bash
docker pull ghcr.io/zonprox/signy:latest
docker compose -f portainer-stack.yml up -d
```

Or in Portainer: **Stacks → signy → Update the stack**.

---

## Troubleshooting

### Bot not responding

1. Check bot token: `docker logs signy | grep "telegram"`
2. Verify `TELEGRAM_BOT_TOKEN` is correct
3. Check telegram-bot-api is running: `docker logs signy-tg-api`

### File too large error

- Ensure `TELEGRAM_API_ID` and `TELEGRAM_API_HASH` are set in the stack
- Ensure `telegram-bot-api` service is running and healthy
- Check: `docker logs signy-tg-api`

### Signing fails

```bash
# Check worker logs
docker logs signy | grep -i "sign\|error\|zsign"

# Verify zsign binary exists
docker exec signy zsign --version
```

### OTA install not working (iOS)

- `BASE_URL` must be HTTPS for OTA install to work on iOS 17+
- Self-signed certificates will be rejected by iOS unless installed as trusted
- Use Caddy with a real domain for automatic Let's Encrypt TLS

### Admin dashboard shows no jobs

- Navigate to `http://IP:7890/` (or `https://domain/`)
- If `ADMIN_PASSWORD` is set, enter it in the Basic Auth dialog
- Username field can be anything

---

## Backup

### What to back up

| Data         | Location                            | Contents                                        |
| ------------ | ----------------------------------- | ----------------------------------------------- |
| Certificates | `signy_storage:/storage/certs/`     | P12 + provisioning profiles                     |
| Artifacts    | `signy_storage:/storage/artifacts/` | Signed IPAs (kept per `RETENTION_DAYS_DEFAULT`) |
| Redis        | `signy_redis:/data/`                | Job queue + session state                       |

### Backup commands

```bash
# Export Redis data
docker exec signy-redis redis-cli BGSAVE
docker cp signy-redis:/data/dump.rdb ./backup-redis.rdb

# Export storage volume
docker run --rm -v signy_storage:/data -v $(pwd):/backup ubuntu \
  tar czf /backup/storage-backup.tar.gz /data
```

---

## Environment Variables Reference

| Variable                 | Default                | Description                                              |
| ------------------------ | ---------------------- | -------------------------------------------------------- |
| `TELEGRAM_BOT_TOKEN`     | —                      | **Required.** Bot token from @BotFather                  |
| `TELEGRAM_API_ID`        | —                      | App ID from my.telegram.org (enables large file support) |
| `TELEGRAM_API_HASH`      | —                      | App hash from my.telegram.org                            |
| `BASE_URL`               | —                      | **Required.** Public URL for download links              |
| `REDIS_URL`              | `redis://redis:6379/0` | Redis connection string                                  |
| `STORAGE_PATH`           | `/storage`             | Storage directory path                                   |
| `MASTER_KEY`             | —                      | AES-256 key for P12 encryption                           |
| `ADMIN_PASSWORD`         | —                      | Basic Auth password for `/` dashboard                    |
| `APP_PORT`               | `7890`                 | HTTP port                                                |
| `WORKER_CONCURRENCY`     | `2`                    | Parallel signing jobs                                    |
| `RETENTION_DAYS_DEFAULT` | `7`                    | Days to keep signed artifacts                            |
| `MAX_IPA_MB`             | `500`                  | Max IPA upload size in MB                                |
| `ZSIGN_MOCK`             | `false`                | Skip real signing (for testing)                          |
| `DEBUG`                  | `false`                | Verbose entrypoint logging                               |
