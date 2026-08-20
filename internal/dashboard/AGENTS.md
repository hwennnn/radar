# Radar Dashboard Instructions

This subtree owns the read-only HTTP API, presentation rules, and embedded web
application.

- API changes require handler tests. Keep feed limits bounded and canonicalize
  apply URLs before returning them.
- UI navigation uses real paths (`/jobs`, `/companies`, `/system`, `/docs`).
- Loading, empty, failure, narrow, and wide states must remain usable.
- Preserve `/api/jobs` incremental reconciliation and `/api/status` sanitization.
- Dashboard changes require Go tests, `make gate`, and a real browser pass.
- Tests and previews must omit Telegram credentials and use log delivery.
