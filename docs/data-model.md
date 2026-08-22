# Data model

`internal/postgres/store.go` is the schema source of truth. The application uses
an isolated Postgres schema, `radar_lite` by default, and applies idempotent
migrations in writer modes.

| Table | Responsibility |
| --- | --- |
| `jobs` | Canonical posting, active-seen timestamps, and apply-link health |
| `job_identities` | Native ID, canonical URL, requisition, and fallback aliases |
| `job_source_observations` | Provenance and active state for each source sighting |
| `job_rejections` | Rejected source observations, reason code, and policy version |
| `deliveries` | Durable, replay-safe outbox rows |
| `source_status` | Successful-empty versus failed source health |
| `bootstrap_state` | Initial snapshot and recovery-baseline state |
| `runtime_state` | Current cycle owner and last completed cycle summary |
| `discovery_candidates` | Scheduled company research and retry state |
| `discovered_sources` | Candidate, promoted, unhealthy, or duplicate routes |
| `discovery_events` | Append-only admission, rejection, and failure evidence |
| `source_controls` | Current active or quarantined operator decision |
| `source_events` | Append-only quarantine and restore audit history |

## Identity and provenance

One canonical job may have several identity aliases and source observations.
Native provider IDs and canonical apply URLs are strongest; requisition aliases
and a conservative fallback fingerprint cover weaker providers. Title and
company alone are never sufficient identity.

Source reconciliation changes visibility only after a complete snapshot. An
incomplete or failed source cannot falsely close jobs or replace a previous
healthy snapshot.

Canonical jobs record the source and authority of their presentation fields.
Reviewed catalog routes outrank auto-discovered and broad-search evidence; a
weaker source may add provenance and identity evidence but cannot downgrade the
canonical company, title, location, description, or apply URL. Accepted jobs
also record the admission policy version used for the decision.

Apply-link health is stored on the canonical job. Writer cycles select a
bounded due set, prioritizing unchecked and newly publishable jobs, preserve
ambiguous failures, and hide a URL only after two
consecutive terminal results. When an observation changes `apply_url`, its
health, failure count, and schedule reset atomically so a re-uploaded
requisition is validated as new evidence.

## Atomic delivery decision

Job persistence, identity attachment, provenance, and the delivery decision are
committed together. A unique database index permits at most one delivery for a
given `(job_id, channel, recipient)` tuple. Bootstrap and recovery baselines use
suppressed or staged rows so historical jobs are not emitted as new alerts.
Confirmed Telegram responses store the provider message and chat IDs. A lost
response after a request may have been accepted externally; those rows move to
`uncertain` instead of retrying blindly and risking a duplicate alert.

Delivery claims require a current admission-policy version and at least one
active, non-quarantined source observation. Source snapshot activation, source
health, bootstrap completion, and staged-delivery activation share one
transaction, so a partial source pass cannot become visible or publishable.

## Schema-change rules

- Keep migrations additive and idempotent unless a separately reviewed data
  migration proves a destructive change safe.
- Preserve existing schema names, identity keys, recipient keys, and delivery
  states across deployment cutovers.
- Test restart, concurrent lease, retry, alias collision, and outbox uniqueness
  behavior against disposable Postgres.
- Record before/after row counts for any live migration; never infer success
  only from process startup.
