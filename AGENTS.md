# Radar Agent Instructions

Radar is an autonomous early-career job intelligence service. Work from
evidence, preserve user changes, and keep commits focused and green.

## Product boundary

This repository is the complete Radar product. Keep it focused on source
discovery, ATS verification, source health, job identity and provenance,
early-career filtering, the read-only dashboard, and durable delivery.

## Non-negotiable invariants

- Discovery evidence is control-plane input. A job becomes visible only after a
  company-owned source passes the production extractor.
- Empty, ambiguous, mismatched, or nontechnical boards do not promote.
- A failing source never blocks healthy sources.
- Never deduplicate solely on title and company.
- Job persistence and delivery creation are atomic, with at most one row per
  `(job, channel, recipient)`.
- Initial snapshots and recovery baselines suppress historical jobs.
- A single Postgres cycle lease owns crawling and delivery draining.
- External publishing stays disabled unless the user says exactly
  `enable publishing`. Tests and previews must blank Telegram credentials and
  use log delivery.

## Target bar

Prioritize big tech, frontier AI/data/devtools/security, strong unicorns,
credible YC-backed startups, and established quant firms. Include genuinely
technical software, ML, data, infrastructure, security, and quantitative roles.
Suppress defense-first employers, low-signal agencies, support, QA-only,
business analyst, generic operations, management, and experienced-only roles.

## Verification

Run `make gate` for deterministic coverage. Shared or persistence changes also
require `RADAR_TEST_DATABASE_URL='postgres://...' make test-db` against a
disposable database. Dashboard changes require a real browser pass.

Never log or commit secrets. Do not push or change production unless the user
explicitly asks.
