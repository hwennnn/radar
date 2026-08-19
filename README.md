# Radar

Radar is an autonomous early-career job intelligence service. It monitors
company-owned career sources, discovers and verifies new ATS boards, filters for
technical internships and new-grad roles, deduplicates openings in Postgres,
publishes durable notifications, and serves a read-only dashboard.

## Run locally

Copy `.env.example` to `.env`, configure a dedicated Postgres database, then run:

```sh
set -a; . ./.env; set +a
go run ./cmd/radar once
go run ./cmd/radar serve
```

The dashboard is available at `http://localhost:8080`. Delivery defaults to the
safe `log` mode. Telegram additionally requires credentials and the explicit
`RADAR_LITE_PUBLISHING_ENABLED=true` gate.

## Verify

```sh
make gate
```

Database-backed lease, persistence, alias, and outbox tests require a disposable
Postgres database:

```sh
RADAR_TEST_DATABASE_URL='postgres://...' make test-db
```

## Deploy

`compose.yaml` runs Radar with a private Postgres 16 database and persistent
volume:

```sh
docker compose up --build
```
