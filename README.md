<div align="center">

# Radar

### Early-career engineering jobs, discovered at the source.

Radar continuously verifies company-owned career pages, finds internships and
new-grad roles, removes duplicates, and delivers each new opening once.

[![CI](https://github.com/hwennnn/radar/actions/workflows/ci.yml/badge.svg)](https://github.com/hwennnn/radar/actions/workflows/ci.yml)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-4169E1?logo=postgresql&logoColor=white)
![Delivery](https://img.shields.io/badge/delivery-durable-087F75)

[Live dashboard](https://radar.hwendev.com) ·
[Telegram alerts](https://t.me/earlycareerradar) ·
[How it works](https://radar.hwendev.com/docs) ·
[Architecture](docs/architecture.md) ·
[Documentation](docs/README.md)

</div>

![Radar job feed showing search, filters, live counts, and early-career engineering roles](docs/assets/radar-jobs.png)

<table>
  <tr>
    <td width="50%"><img src="docs/assets/radar-companies.png" alt="Radar monitored-company source roster" /></td>
    <td width="50%"><img src="docs/assets/radar-system.png" alt="Radar operational system pulse" /></td>
  </tr>
  <tr>
    <td align="center"><strong>Source coverage</strong><br />Every monitored route has visible health and provenance.</td>
    <td align="center"><strong>Operational truth</strong><br />Discovery, dedupe, delivery, and runtime state in one view.</td>
  </tr>
</table>

## Why Radar exists

Job aggregators are fast, but their data is often copied, stale, duplicated, or
detached from the employer that published it. Radar takes the slower and more
defensible path: it treats discovery as a lead, verifies a company-owned source
with a production extractor, and publishes only jobs that survive the same
rules on every cycle.

The result is a focused feed for software, ML, data, infrastructure, security,
and quantitative internships and new-grad roles in the U.S. and Singapore.

## What makes it trustworthy

| Property | How Radar enforces it |
| --- | --- |
| Official sources | A discovered URL cannot publish jobs until a company-owned route passes the production extractor. |
| Conservative identity | Native IDs and canonical apply URLs outrank requisitions and fallback fingerprints. Title and company alone are never enough. |
| Complete snapshots | A source closes missing jobs only after a complete successful crawl. Failed or partial reads retain the last healthy state. |
| One durable alert | Job persistence, identity aliases, provenance, and the delivery decision commit in one Postgres transaction. |
| Failure isolation | A broken source degrades independently while healthy sources continue. |
| Safe recovery | Initial snapshots and recovery baselines suppress historical jobs instead of flooding the delivery channel. |
| Single writer | A Postgres advisory lease allows only one process to own crawling and delivery draining at a time. |

## How the system fits together

```mermaid
flowchart LR
    Seeds[Discovery seed] --> Discover[Bounded discovery]
    Catalog[Verified source catalog] --> Extract[Production extractors]
    Discover --> Verify{Company-owned and extractable?}
    Verify -->|yes| Extract
    Verify -->|no| Backoff[Candidate backoff]
    Extract --> Filter[Early-career relevance]
    Filter --> Identity[Identity and provenance]
    Identity --> DB[(Postgres)]
    DB --> Web[Read-only dashboard and API]
    DB --> Outbox[Durable delivery outbox]
    Outbox --> Telegram[Telegram]
```

Radar is a single Go service with an embedded web application and Postgres as
its durable coordination layer. The full reasoning, transaction boundaries,
failure model, and deployment topology live in the
[architecture deep dive](docs/architecture.md).

## Quick start

### Preview the dashboard

```sh
make preview
```

Open [http://localhost:8789](http://localhost:8789). The preview is read-only
and proxies the configured Radar API; it does not crawl or deliver jobs.

### Run the complete service

Requirements: Go 1.26, PostgreSQL 16, and Node.js 24 for preview tests.

```sh
cp .env.example .env
# Configure a dedicated Postgres database and TINYFISH_API_KEY in .env
set -a; . ./.env; set +a

go run ./cmd/radar once   # initialize and run one complete cycle
go run ./cmd/radar serve  # serve the read-only dashboard and API
```

The `RADAR_LITE_*` environment prefix and default `radar_lite` schema are
retained for deployment and database compatibility; the product name is Radar.

### Docker Compose

The production-oriented Compose stack runs Radar and Postgres with a persistent
named volume. Set `SERVICE_PASSWORD_64_POSTGRES` and `TINYFISH_API_KEY`, then:

```sh
docker compose up --build
```

The application listens on container port `8080`; publish it through your
reverse proxy or add a local port mapping for direct host access. Delivery
defaults to `log`, so the stack cannot publish Telegram messages accidentally.

## Runtime modes

| Mode | What it does | Writes state? |
| --- | --- | --- |
| `routine` | Continuously discovers, crawls, drains delivery, and serves HTTP | Yes |
| `once` | Runs one complete writer cycle and exits | Yes |
| `serve` | Serves the dashboard and read-only API without crawling or migrations | No |
| `market-once` | Runs one bounded market-discovery pass | Yes |
| `reconcile` | Processes due discovery candidates | Yes |
| `drain` | Drains the durable delivery outbox | Yes |
| `discover` | Prints catalog coverage and gaps | No database |
| `audit` | Enforces the static catalog contract | No database |

## Repository map

```text
cmd/radar/          process modes, HTTP API, embedded dashboard
cmd/telegram-smoke/ guarded Telegram verification utility
internal/core/      source lifecycle, identity, persistence, leases, outbox
internal/provider/  structured ATS adapters
internal/scraper/   extraction and page normalization
internal/delivery/  log, webhook, and Telegram delivery transports
config/             verified source catalog and discovery research seed
docs/               architecture, data model, operations, API, development
```

The repository is designed for both humans and coding agents. Start at
[`docs/README.md`](docs/README.md); scoped `AGENTS.md` files define invariants
and verification requirements near the code they govern.

## Verification

```sh
# Catalog audit, race-enabled unit suite, dashboard preview tests, and vet
make gate

# Persistence, identity, outbox, restart, and lease integration tests
RADAR_TEST_DATABASE_URL='postgres://radar:radar@127.0.0.1:5432/radar_test?sslmode=disable' make test-db

# Deployment shape
docker compose config --quiet
make docker-build
```

Never point integration tests at production. Database tests require a
disposable database or schema, and tests/previews must omit Telegram
credentials.

## Delivery safety

External Telegram publishing is deliberately difficult to enable. It requires
Telegram delivery mode, valid bot and channel credentials, the explicit
publishing gate, and user authorization. `make telegram-check` validates the
configuration without sending; `make telegram-smoke` is a real external side
effect and must be used only with explicit approval.

## Documentation

- [Architecture](docs/architecture.md) — end-to-end design and invariants
- [Data model](docs/data-model.md) — tables, identity, provenance, and outbox
- [Source lifecycle](docs/source-lifecycle.md) — research through promotion
- [HTTP API](docs/http-api.md) — feed, status, health, and incremental reads
- [Operations](docs/operations.md) — deployment, observability, and cutover
- [Configuration](docs/configuration.md) — environment contract
- [Development](docs/development.md) — modes and verification levels
- [Contributing](CONTRIBUTING.md) — change workflow and completion evidence

## Contributing

Focused issues and pull requests are welcome. Preserve the product invariants,
keep commits reviewable, run the narrow test while iterating, and finish with
the verification required by the affected subsystem. See
[`CONTRIBUTING.md`](CONTRIBUTING.md) before changing runtime behavior.
