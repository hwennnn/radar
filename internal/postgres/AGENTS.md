# Radar PostgreSQL Instructions

This package owns connections, additive migrations, durable pipeline state,
bootstrap suppression, delivery storage, and the cycle lease.

- Keep schema changes idempotent and compatible with existing rows.
- Commit jobs, identities, provenance, and delivery decisions atomically.
- Preserve unique `(job, channel, recipient)` delivery semantics.
- Initial snapshots, recovery, and destination transitions must not replay
  historical jobs.
- Claims, retries, and restart recovery must remain idempotent and bounded.
- One session advisory lease owns a writer cycle.
- Run `make gate` and the disposable PostgreSQL suite for every change here.
