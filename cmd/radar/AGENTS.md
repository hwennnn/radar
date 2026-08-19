# Radar Service Agent Instructions

This subtree owns process modes, configuration parsing, HTTP contracts, health
semantics, and the embedded dashboard.

- Preserve canonical CLI names and keep compatibility aliases out of primary
  documentation and automation.
- `serve` must remain read-only: no migration, crawling, discovery, or delivery.
- `/healthz` is liveness; `/readyz` is role-aware readiness; `/api/status` reads
  durable operational truth and must not expose secrets or raw private errors.
- API changes require handler tests. Keep feed limits bounded and apply URLs
  canonicalized before returning them.
- UI navigation uses real paths (`/jobs`, `/companies`, `/system`, `/docs`).
  Loading, empty, failure, narrow, and wide states must remain usable.
- Dashboard changes require Go tests, `make gate`, and a real browser pass.
- Tests and previews must use log delivery with Telegram credentials absent.
  Never activate publishing to validate formatting or UI behavior.
