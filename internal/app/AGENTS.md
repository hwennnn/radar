# Radar Service Agent Instructions

This subtree owns process modes, configuration parsing, lifecycle, health
semantics, and dependency wiring. Dashboard and HTTP presentation live in
`internal/dashboard`.

- Preserve canonical CLI names and keep compatibility aliases out of primary
  documentation and automation.
- `serve` must remain read-only: no migration, crawling, discovery, or delivery.
- `/healthz` is liveness; `/readyz` is role-aware readiness; `/api/status` reads
  durable operational truth and must not expose secrets or raw private errors.
- Tests and previews must use log delivery with Telegram credentials absent.
  Never activate publishing to validate formatting or UI behavior.
