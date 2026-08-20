# Data model

`internal/postgres/store.go` is the schema source of truth. The application uses
an isolated Postgres schema, `radar_lite` by default, and applies idempotent
migrations in writer modes.

| Table | Responsibility |
| --- | --- |
| `jobs` | Canonical source-independent posting and active-seen timestamps |
| `job_identities` | Native ID, canonical URL, requisition, and fallback aliases |
| `job_source_observations` | Provenance and active state for each source sighting |
| `deliveries` | Durable, replay-safe outbox rows |
| `source_status` | Successful-empty versus failed source health |
| `bootstrap_state` | Initial snapshot and recovery-baseline state |
| `runtime_state` | Current cycle owner and last completed cycle summary |
| `discovery_candidates` | Scheduled company research and retry state |
| `discovered_sources` | Candidate, promoted, unhealthy, or duplicate routes |

## Identity and provenance

One canonical job may have several identity aliases and source observations.
Native provider IDs and canonical apply URLs are strongest; requisition aliases
and a conservative fallback fingerprint cover weaker providers. Title and
company alone are never sufficient identity.

Source reconciliation changes visibility only after a complete snapshot. An
incomplete or failed source cannot falsely close jobs or replace a previous
healthy snapshot.

## Atomic delivery decision

Job persistence, identity attachment, provenance, and the delivery decision are
committed together. A unique database index permits at most one delivery for a
given `(job_id, channel, recipient)` tuple. Bootstrap and recovery baselines use
suppressed or staged rows so historical jobs are not emitted as new alerts.

## Schema-change rules

- Keep migrations additive and idempotent unless a separately reviewed data
  migration proves a destructive change safe.
- Preserve existing schema names, identity keys, recipient keys, and delivery
  states across deployment cutovers.
- Test restart, concurrent lease, retry, alias collision, and outbox uniqueness
  behavior against disposable Postgres.
- Record before/after row counts for any live migration; never infer success
  only from process startup.
