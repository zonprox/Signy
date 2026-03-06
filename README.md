# Signy

Self-hosted Telegram bot for signing iOS IPA files.

[![CI](https://github.com/zonprox/Signy/actions/workflows/ci.yml/badge.svg)](https://github.com/zonprox/Signy/actions/workflows/ci.yml)

## Deploy with Portainer

1. Get credentials:
   - Bot token — [@BotFather](https://t.me/BotFather) → `/newbot`
   - API ID + Hash — [my.telegram.org/apps](https://my.telegram.org/apps)

2. Portainer → **Stacks** → **Add Stack** → paste [`portainer-stack.yml`](portainer-stack.yml)

3. Add **Environment Variables** in Portainer:

   | Variable             | Required | Description                |
   | -------------------- | -------- | -------------------------- |
   | `TELEGRAM_BOT_TOKEN` | ✅       | From @BotFather            |
   | `TELEGRAM_API_ID`    | ✅       | From my.telegram.org/apps  |
   | `TELEGRAM_API_HASH`  | ✅       | From my.telegram.org/apps  |
   | `MASTER_KEY`         | —        | `openssl rand -hex 32`     |
   | `ADMIN_PASSWORD`     | —        | Dashboard login password   |

4. Deploy → send `/start` to your bot.

## Local Development

```bash
# Set required env vars
export TELEGRAM_BOT_TOKEN=your-token
export TELEGRAM_API_ID=12345678
export TELEGRAM_API_HASH=abcdef...

make setup     # creates storage directory
make build-ui  # builds Tailwind CSS and downloads fonts
make up        # docker compose up --build -d
make logs
```

## Environment Variables

| Variable                       | Required | Default | Description                    |
| ------------------------------ | -------- | ------- | ------------------------------ |
| `TELEGRAM_BOT_TOKEN`           | ✅       | —       | Bot token from @BotFather      |
| `TELEGRAM_API_ID`              | ✅       | —       | App ID from my.telegram.org    |
| `TELEGRAM_API_HASH`            | ✅       | —       | App hash from my.telegram.org  |
| `BASE_URL`                     | —        | —       | Auto-detected from server IP   |
| `MASTER_KEY`                   | —        | —       | AES-256 key for P12 encryption |
| `ADMIN_PASSWORD`               | —        | —       | Dashboard login (`/login`)     |
| `WORKER_CONCURRENCY`           | —        | `2`     | Parallel signing jobs          |
| `USER_CONCURRENCY`             | —        | `1`     | Jobs per user concurrently     |
| `RETENTION_DAYS_DEFAULT`       | —        | `7`     | Days to keep artifacts         |
| `MAX_IPA_MB`                   | —        | `500`   | Max IPA upload size            |
| `MAX_P12_MB`                   | —        | `20`    | Max P12 file size              |
| `MAX_PROV_KB`                  | —        | `512`   | Max provisioning profile size  |
| `MAX_CERTSETS_PER_USER`        | —        | `3`     | Cert sets per user             |
| `JOB_TIMEOUT_SIGNING_SECONDS`  | —        | `900`   | Signing timeout                |
| `VISIBILITY_TIMEOUT_SECONDS`   | —        | `600`   | Queue visibility timeout       |
| `ZSIGN_MOCK`                   | —        | `false` | Skip real signing (testing)    |
| `DEBUG`                        | —        | `false` | Verbose startup logs           |

## License

MIT
