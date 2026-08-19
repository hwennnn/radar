# Development

Radar requires Go 1.26, Node.js for dashboard preview tests, and Postgres 16 for
integration tests and local operation.

## Local setup

```sh
cp .env.example .env
set -a; . ./.env; set +a
go run ./cmd/radar once
go run ./cmd/radar serve
```

`once` performs real source requests, persists one complete cycle, and drains
the configured delivery channel. Keep delivery in `log` mode during development.
`serve` is read-only and expects an initialized schema.

For a self-contained stack:

```sh
docker compose up --build
```

## Runtime commands

| Command | Database | Network | Writes pipeline state | Purpose |
| --- | --- | --- | --- | --- |
| `radar routine` | required | yes | yes | Continuous discovery, crawling, delivery, and HTTP |
| `radar once` | required | yes | yes | One complete routine cycle |
| `radar market-once` | required | yes | yes | One bounded market-search pass |
| `radar serve` | required | no provider calls | no | Read-only API and dashboard |
| `radar discover` | no | no | no | Print catalog gaps and coverage |
| `radar audit` | no | no | no | Enforce static catalog coverage |
| `radar reconcile` | required | yes | yes | Process bounded discovery candidates only |
| `radar drain` | required | delivery only | yes | Drain the durable delivery outbox |

Aliases exist for compatibility, but documentation and automation should use
the canonical names above.

## Verification levels

Use the narrowest test while iterating, then finish with the applicable level:

```sh
# Deterministic catalog, race, preview, and vet checks
make gate

# Persistence, restart, identity, outbox, and lease integration
RADAR_TEST_DATABASE_URL='postgres://radar:radar@127.0.0.1:5432/radar_test?sslmode=disable' make test-db

# Container and Compose validation
docker compose config --quiet
make docker-build
```

Dashboard changes additionally require a real browser pass across `/jobs`,
`/companies`, `/system`, and `/docs`, including loading, empty, error, narrow,
and wide states as applicable.

Never point integration tests at production. Use a disposable database or
schema and omit Telegram credentials.
