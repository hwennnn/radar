# Production cutover

The initial standalone release intentionally preserves the existing Radar Lite
schema. Production currently runs from `radar-full`; this repository becomes the
deployment source only after standalone parity is proven.

## Preconditions

- `make gate` passes.
- Database-backed tests pass against a disposable schema.
- Two complete log-only cycles pass against a restored production backup.
- Job, alias, source-observation, and delivery counts match the source backup.
- Production `/readyz` is green and its delivery outbox is empty.
- The production Postgres backup has been restored successfully in a disposable
  environment.

## Procedure

1. Stop the old routine process or temporarily change it to `serve`.
2. Confirm the Postgres cycle lease is released.
3. Repoint the existing Coolify resource to `hwennnn/radar` while preserving its
   project, secrets, domain, and named Postgres volume.
4. Deploy the new image in `serve` or log-only mode.
5. Verify `/healthz`, `/readyz`, `/api/status`, `/api/jobs`, database counts, and
   duplicate delivery keys.
6. Start one `routine` replica in log-only mode and observe a full cycle.
7. Restore Telegram mode and its explicit publishing gate only after parity is
   confirmed.

Do not create a replacement Coolify Compose resource for the cutover: its
project-scoped volume would be empty. Roll back by deploying the previous image
against the unchanged database volume.
