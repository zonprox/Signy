# Signy

Self-hosted Telegram bot for signing iOS IPA files.

[![CI](https://github.com/zonprox/Signy/actions/workflows/ci.yml/badge.svg)](https://github.com/zonprox/Signy/actions/workflows/ci.yml)

## Deploy with Portainer

1. Get credentials:
   - Bot token — [@BotFather](https://t.me/BotFather) → `/newbot`
   - API ID + Hash — [my.telegram.org/apps](https://my.telegram.org/apps)

2. Portainer → **Stacks** → **Add Stack** → paste [`portainer-stack.yml`](portainer-stack.yml)

3. Edit the required values:

   ```yaml
   TELEGRAM_BOT_TOKEN=   # from @BotFather
   TELEGRAM_API_ID=      # from my.telegram.org
   TELEGRAM_API_HASH=    # from my.telegram.org
   # BASE_URL=             # auto-detected (optional)
   ```

4. Deploy → send `/start` to your bot.

## Local Development

```bash
make setup     # copies .env.example → .env
# edit .env
make build-ui  # builds the Tailwind CSS and downloads fonts into internal/web/static
make up        # docker compose up --build -d
make logs
```

## Environment Variables

| Variable                 | Required | Default | Description                    |
| ------------------------ | -------- | ------- | ------------------------------ |
| `TELEGRAM_BOT_TOKEN`     | ✅       | —       | Bot token from @BotFather      |
| `TELEGRAM_API_ID`        | ✅       | —       | App ID from my.telegram.org    |
| `TELEGRAM_API_HASH`      | ✅       | —       | App hash from my.telegram.org  |
| `BASE_URL`               | —        | —       | Optional: auto-detected as IP  |
| `MASTER_KEY`             | —        | —       | AES-256 key for P12 encryption |
| `ADMIN_PASSWORD`         | —        | —       | Dashboard login (`/login`)     |
| `WORKER_CONCURRENCY`     | —        | `2`     | Parallel signing jobs          |
| `RETENTION_DAYS_DEFAULT` | —        | `7`     | Days to keep artifacts         |
| `ZSIGN_MOCK`             | —        | `false` | Skip real signing (testing)    |

## License

MIT
