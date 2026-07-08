# family-visits

Telegram bot that captures one-off family appointments (маникюр, педикюр,
ортодонт, врачи…) from **plain text** and reminds about them.

The hard part of tracking these is data entry — so you just write like you
would to a person:

```
8.07 11:30 Педикюр Олежа
16.07 11:45 Ортодонт
22.07 13:30 Маникюр в обох
```

The bot parses it with Gemini (structured output), shows a confirmation card,
and on **✅ Сохранить** stores each visit. It then pushes a morning "today"
digest and a weekly "this week" digest to the family group.

## How it works

- **Capture**: free text → `internal/parse` (Gemini `gemini-flash-lite-latest`,
  forced JSON schema) → confirmation card → SQLite.
- **Who**: `person` is taken from the text; unspecified (or self-referential
  «я/мне/себе») defaults to the message sender.
- **Dates**: no year needed — relative and bare dates resolve to the nearest
  future in `APP_TZ`.
- **Query**: `/week` (next 7 days), `/list` (all upcoming).
- **Digests**: off by default. Set `NOTIFICATIONS_ENABLED=true` to turn on the
  minute-resolution scheduler, which then fires `DAILY_DIGEST_TIME` and the
  weekly digest at `WEEKLY_DIGEST_DOW`/`WEEKLY_DIGEST_TIME`. Left off, Home
  Assistant's Remote Calendar (reading the ICS feed) owns the summaries and the
  app won't double-post. Interactive bot replies are unaffected either way.
- **Storage**: SQLite is the source of truth. Columns `ha_uid` / `ha_synced_at`
  / `updated_at` are in place for a later Home Assistant calendar exporter
  (outbox pattern) — no schema change needed to add it.

## Run

```sh
cp .env.example .env   # fill TELEGRAM_BOT_TOKEN, TELEGRAM_ALLOWED_CHATS,
                       # TELEGRAM_NOTIFY_CHAT, GEMINI_API_KEY
go run ./cmd/server
```

The bot needs its **own** Telegram token (`@BotFather` → `/newbot`) — it cannot
share a token with another polling consumer (e.g. Home Assistant).

Config is via env (`.env` is loaded for local dev); see `.env.example`.

## Test

```sh
go build ./... && go vet ./...
set -a && . ./.env && set +a && go test ./internal/parse/ -run TestParseSample -v
```

The parse test hits the real Gemini API and is skipped when `GEMINI_API_KEY`
is unset.

## Roadmap

- Home Assistant calendar export (exporter job over the outbox columns).
- Voice messages (transcribe → parse).
- Reschedule / cancel a saved visit from the bot.
