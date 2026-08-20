# Radar

Radar is a Go service for discovering and verifying early-career engineering
jobs from company-owned career sites. It maintains source health, canonical job
identity, provenance, and durable delivery state in PostgreSQL, and exposes the
result through a read-only dashboard and HTTP API.

[![CI](https://github.com/hwennnn/radar/actions/workflows/ci.yml/badge.svg)](https://github.com/hwennnn/radar/actions/workflows/ci.yml)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-4169E1?logo=postgresql&logoColor=white)

[Live dashboard](https://radar.hwendev.com) ·
[Architecture](docs/architecture.md) ·
[Documentation](docs/README.md) ·
[Telegram](https://t.me/earlycareerradar)

![Radar dashboard showing search, filters, live counts, and verified jobs](docs/assets/radar-jobs.png)

## Why

Job listings copied through aggregators lose useful guarantees: the employer may
not own the source, openings may be stale, and the same role may appear under
several URLs. Radar treats discovery as untrusted input. A source is promoted
only after a production extractor verifies that it is company-owned and returns
a valid technical-job snapshot.

The pipeline is intentionally conservative:

- failed or incomplete source reads do not close previously observed jobs;
- source failures are isolated, so healthy routes continue processing;
- job identity uses native IDs, canonical apply URLs, requisitions, and fallback
  fingerprints instead of title and company alone;
- initial snapshots and recovery baselines do not emit historical jobs;
- persistence and delivery creation occur in one transaction;
- a PostgreSQL lease permits only one crawler and delivery writer at a time.

## What

Radar covers the complete path from source research to a public, deduplicated
feed:

| Component | Responsibility |
| --- | --- |
| Discovery | Research candidate employers and career routes with bounded retries. |
| Verification | Require ownership evidence and a successful production extraction before promotion. |
| Extraction | Read supported ATS providers and normalized company career pages. |
| Filtering | Retain technical internships and new-grad roles in configured markets. |
| Identity | Resolve aliases, canonical jobs, cross-source observations, and provenance. |
| Persistence | Store snapshots, health, leases, jobs, and the delivery outbox in PostgreSQL. |
| HTTP | Serve incremental job reads, source status, health endpoints, and the embedded dashboard. |
| Delivery | Drain an idempotent outbox to a configured transport; external publishing is off by default. |

Repository layout:

```text
cmd/radar/          thin process entrypoint
internal/app/       modes, lifecycle, and process wiring
internal/dashboard/ read-only HTTP API and embedded dashboard
internal/pipeline/  source lifecycle, filtering, identity, and orchestration
internal/postgres/  migrations, durable state, leases, and outbox storage
internal/source/    discovery clients, extractors, and provider adapters
internal/delivery/  delivery transports and retry behavior
config/             verified source catalog and discovery research seed
docs/               architecture, data model, operations, API, development
```

## How

```mermaid
flowchart LR
    Seed[Discovery candidates] --> Verify{Owned and extractable?}
    Catalog[Verified catalog] --> Extract[Production extractors]
    Verify -->|yes| Catalog
    Verify -->|no| Retry[Backoff]
    Extract --> Filter[Early-career filter]
    Filter --> Identity[Identity and provenance]
    Identity --> DB[(PostgreSQL)]
    DB --> API[Dashboard and API]
    DB --> Outbox[Delivery outbox]
    Outbox --> Transport[Configured transport]
```

Radar runs as one binary with several explicit modes. PostgreSQL is both the
durable state store and the coordination boundary. See the
[architecture document](docs/architecture.md) for transaction boundaries,
identity precedence, snapshot semantics, failure isolation, and deployment
topology.

### Run locally

Requirements: Go 1.26, PostgreSQL 16, and Node.js 24 for dashboard tests.

```sh
cp .env.example .env
# Set a disposable or dedicated database and TINYFISH_API_KEY in .env.
set -a; . ./.env; set +a

go run ./cmd/radar once   # initialize state and run one writer cycle
go run ./cmd/radar serve  # serve the read-only dashboard and API
```

For a dashboard-only preview:

```sh
make preview
```

Open [http://localhost:8789](http://localhost:8789). Preview mode does not crawl
sources or deliver jobs.

The production-oriented Compose stack runs Radar with PostgreSQL and a named
data volume:

```sh
docker compose up --build
```

Set `SERVICE_PASSWORD_64_POSTGRES` and `TINYFISH_API_KEY` before starting it.
The application listens on container port `8080`. The legacy `RADAR_LITE_*`
configuration prefix and `radar_lite` schema remain for deployment and database
compatibility.

### Runtime modes

| Mode | Behavior | Writes state |
| --- | --- | --- |
| `routine` | Continuously discover, crawl, drain delivery, and serve HTTP | Yes |
| `once` | Run one complete writer cycle and exit | Yes |
| `serve` | Serve the dashboard and API without crawling or migrations | No |
| `market-once` | Run one bounded market-discovery pass | Yes |
| `reconcile` | Process due discovery candidates | Yes |
| `drain` | Drain the durable delivery outbox | Yes |
| `discover` | Print catalog coverage and gaps | No database |
| `audit` | Enforce the static catalog contract | No database |
| `telegram-check` | Verify configured delivery identity and permissions; `--send` remains explicitly guarded | No |

### Verify changes

```sh
# Catalog audit, race-enabled unit tests, dashboard tests, and vet.
make gate

# Persistence, identity, outbox, restart, and lease integration tests.
RADAR_TEST_DATABASE_URL='postgres://radar:radar@127.0.0.1:5432/radar_test?sslmode=disable' make test-db

# Deployment shape.
docker compose config --quiet
make docker-build
```

Never run integration tests against production. External delivery requires
explicit configuration and authorization; tests and previews must use log
delivery without transport credentials.

## Further reading

- [Architecture](docs/architecture.md) — components, data flow, and invariants
- [Data model](docs/data-model.md) — identity, provenance, snapshots, and outbox
- [Source lifecycle](docs/source-lifecycle.md) — discovery through promotion
- [HTTP API](docs/http-api.md) — feed, status, health, and incremental reads
- [Operations](docs/operations.md) — deployment, observability, and cutover
- [Configuration](docs/configuration.md) — environment contract
- [Development](docs/development.md) — modes and verification levels
- [Contributing](CONTRIBUTING.md) — change workflow and required evidence
