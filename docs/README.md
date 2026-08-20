# Documentation

This directory is the entry point for humans and coding agents. Read only the
references relevant to the task, plus the nearest `AGENTS.md`.

| Task | Read first | Required proof |
| --- | --- | --- |
| Understand component ownership | [Architecture](architecture.md) | Confirm the owning package before editing |
| Run or verify locally | [Development](development.md) | Narrow test, then the applicable gate |
| Change environment settings | [Configuration](configuration.md) | Config tests and a startup/config validation |
| Operate or deploy the service | [Operations](operations.md) | Health, readiness, status, and publishing state |
| Change HTTP or dashboard behavior | [HTTP API](http-api.md) and `internal/dashboard/AGENTS.md` | Go tests, `make gate`, real browser pass |
| Change persistence, identity, leases, or delivery state | [Data model](data-model.md), `internal/pipeline/AGENTS.md`, and `internal/postgres/AGENTS.md` | Disposable Postgres suite |
| Add or modify a source | [Source lifecycle](source-lifecycle.md) | Catalog audit and extractor tests |
| Prepare a contribution | [Contributing](../CONTRIBUTING.md) | Exact test and side-effect evidence |

## Sources of truth

- Runtime configuration and modes: `internal/app/main.go`
- HTTP response contracts: `internal/dashboard/feed.go` and `internal/dashboard/status.go`
- Database schema and migrations: `internal/postgres/store.go`
- Verified source floor: `config/sources.json`
- Discovery research queue: `config/discovery-seed.json`
- Deterministic verification: `Makefile` and `.github/workflows/ci.yml`

If a document and implementation disagree, stop relying on the document, verify
the behavior in code and tests, and update both in the same change.
