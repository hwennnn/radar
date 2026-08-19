# Architecture

Radar is one Go service backed by Postgres.

## Runtime

- `cmd/radar` owns the process modes, status API, JSON feed, and embedded
  dashboard.
- `internal/core` owns catalog management, discovery scheduling, job identity,
  filtering, persistence, delivery decisions, and the cycle lease.
- `internal/provider` and `internal/scraper` extract company-owned career
  sources.
- `internal/tinyfish` supports bounded source discovery. Discovery results are
  evidence only until a real source is verified by an extractor.
- `internal/delivery` contains the log and Telegram delivery clients.
- `config/sources.json` is the trusted source floor;
  `config/discovery-seed.json` is the research queue.

`routine` crawls, discovers, drains deliveries, and serves HTTP. `serve` exposes
the read-only dashboard without migrations, crawling, or delivery. `once` runs
one complete cycle. `audit` validates the static catalog without a database.

## Persistence

Radar owns its schema from `internal/core/postgres.go`. Migrations are embedded
in the application and remain compatible with the existing production database.

Postgres stores canonical jobs, identity aliases, source observations, source
health, discovery candidates, promoted routes, delivery decisions, and durable
runtime state. A session advisory lock prevents overlapping routine cycles.

## Delivery safety

Delivery defaults to `log`. Telegram requires all three controls:

1. `RADAR_LITE_DELIVERY_MODE=telegram`
2. Valid bot and chat credentials
3. `RADAR_LITE_PUBLISHING_ENABLED=true`

Tests and previews must not use production Telegram credentials.
