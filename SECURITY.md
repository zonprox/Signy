# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Signy, please report it responsibly.

**Do NOT open a public GitHub issue for security vulnerabilities.**

Instead, please email the maintainer directly or use GitHub's private vulnerability reporting feature.

### What to include

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if you have one)

### Response Timeline

- **Acknowledgment**: Within 48 hours
- **Assessment**: Within 7 days
- **Fix**: As soon as possible, depending on severity

## Security Best Practices

### For Operators

1. **Always set `MASTER_KEY`** in production — this enables AES-256-GCM encryption for P12 files and passwords
2. **Use strong `MASTER_KEY`** — generate with `openssl rand -hex 32`
3. **Protect `.env`** — restrict file permissions (`chmod 600 .env`)
4. **Use Docker secrets** or a vault service for production secrets
5. **Enable HTTPS** with a valid TLS certificate (not self-signed) in production
6. **Restrict Redis access** — use authentication and network isolation
7. **Rotate tokens regularly** — Telegram bot token, MASTER_KEY
8. **Monitor logs** — watch for unusual patterns in Prometheus metrics

### Built-in Protections

| Feature | Details |
|---------|---------|
| Password handling | Never logged, auto-deleted from Telegram chat, encrypted in Redis |
| P12 encryption | AES-256-GCM with HKDF-SHA256 derived keys |
| Ephemeral tokens | 10-minute TTL, one-time use, encrypted with per-process key |
| Rate limiting | Per-user rate limiting to prevent abuse |
| Callback debouncing | 2-second window prevents duplicate processing |
| Idempotency | Terminal jobs aren't re-processed on message re-delivery |
| Structured logging | `slog` with redacted sensitive fields |

## Supported Versions

| Version | Supported |
|---------|-----------|
| latest  | ✅         |
