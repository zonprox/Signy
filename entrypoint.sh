#!/bin/sh
set -e

load_secret() {
    local file="/run/secrets/$1"
    if [ -f "$file" ]; then
        export "$2=$(tr -d '\n\r' < "$file")"
    fi
}

load_secret "telegram_bot_token" "TELEGRAM_BOT_TOKEN"
load_secret "master_key" "MASTER_KEY"

[ -z "$TELEGRAM_BOT_TOKEN" ] && echo "[signy] ERROR: TELEGRAM_BOT_TOKEN is required" && exit 1

# Auto-detect BASE_URL from server IP if not set
if [ -z "$BASE_URL" ]; then
    _PORT="${APP_PORT:-7890}"
    _IP=$(hostname -I 2>/dev/null | awk '{print $1}')
    [ -z "$_IP" ] && _IP="localhost"
    export BASE_URL="http://${_IP}:${_PORT}"
    echo "[signy] BASE_URL auto-detected: $BASE_URL"
fi

# Wait for local telegram-bot-api if configured
if [ -n "$TELEGRAM_API_ID" ] && [ -n "$TELEGRAM_API_HASH" ]; then
    echo "[signy] Waiting for telegram-bot-api..."
    i=0
    until curl -s http://telegram-bot-api:8081/ >/dev/null 2>&1; do
        i=$((i+1))
        if [ "$i" -ge 30 ]; then
            echo "[signy] WARNING: telegram-bot-api not ready after 60s, continuing anyway"
            break
        fi
        sleep 2
    done
    echo "[signy] telegram-bot-api ready"
fi

if [ "$DEBUG" = "true" ]; then
    echo "[signy] TELEGRAM_BOT_TOKEN=$(echo "$TELEGRAM_BOT_TOKEN" | cut -c1-10)..."
    echo "[signy] BASE_URL=$BASE_URL"
    echo "[signy] REDIS_URL=${REDIS_URL:-redis://redis:6379/0}"
    echo "[signy] APP_PORT=${APP_PORT:-7890}"
    echo "[signy] LOCAL_API=$([ -n "$TELEGRAM_API_ID" ] && echo enabled || echo disabled)"
    echo "[signy] ZSIGN_MOCK=${ZSIGN_MOCK:-false}"
fi

echo "[signy] Starting..."
exec signy "$@"
