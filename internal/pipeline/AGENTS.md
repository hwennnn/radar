# Core Agent Instructions

This package owns Radar's highest-risk invariants: discovery scheduling,
eligibility, job identity and provenance, Postgres persistence, bootstrap
suppression, the delivery outbox, and the cycle lease.

- Never deduplicate solely on title and company. Preserve native IDs, canonical
  apply URLs, requisition aliases, and conservative fallback identity.
- Commit job persistence, identity, provenance, and delivery decisions
  atomically. Preserve unique `(job, channel, recipient)` delivery semantics.
- Do not let incomplete or failed snapshots close healthy observations.
- Initial snapshots, route recovery, and recipient/sender transitions must not
  flood historical jobs.
- Claims, retries, and restart recovery must remain idempotent and bounded.
- One Postgres advisory lease owns a routine cycle. Standby readers consume the
  durable runtime handoff and must not mutate it.
- Schema changes must be idempotent and compatible with existing rows.

Run focused package tests while iterating, then `make gate`. Any persistence,
identity, delivery-state, bootstrap, or lease change also requires:

```sh
RADAR_TEST_DATABASE_URL='postgres://...' make test-db
```

Use a disposable database and log delivery. Report row-count and concurrency
evidence for migrations or recovery changes.
