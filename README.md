# Radar

Radar is an autonomous early-career job intelligence service. It monitors
company-owned career sources, discovers and verifies new ATS boards, filters for
technical internships and new-grad roles, deduplicates openings in Postgres,
publishes durable notifications, and serves a read-only dashboard.

This repository contains the standalone Radar Lite product extracted from the
private `hwennnn/radar-full` monorepo at source commit
`4c308ba3d9e01d3d06ccd16d0bf820890add4ace`.

## Run locally

Copy `.env.example` to `.env`, configure a dedicated Postgres database, then run:

```sh
set -a; . ./.env; set +a
go run ./cmd/radar-lite once
go run ./cmd/radar-lite serve
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

`coolify/docker-compose.yaml` runs the service with a private Postgres 16
database and persistent volume. Production cutover must reuse the existing
Coolify resource and database volume; creating a new Compose resource would
create a fresh empty volume.
