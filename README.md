# Spread Scanner Bot (Go)

Go rewrite of `spread_kushat_pora` with:
- concurrent exchange polling (goroutines),
- 1-second refresh loop,
- DDD-style layers (`domain`, `application`, `infrastructure`),
- PostgreSQL for sessions,
- Redis for watcher state/history,
- filter for non-tradable/delisted symbols.

## Run locally

```bash
cp .env.example .env
go mod tidy
go run ./cmd/spreadbot
```

## Docker

```bash
docker compose up --build -d
```

Health:
- `GET /health` on `:3000`

## Telegram commands

- `/start`
- `/menu`
- `/filters`
- `/mode spot|futures|spot-futures`
- `/minspread <value>`
- `/minvol <value>`
- `/alerts on|off`
