# Operations

## Deployment shapes

The default `routine` process owns crawling, delivery draining, and HTTP. A
split deployment may run one routine process and one or more read-only `serve`
processes against the same schema. A Postgres advisory lease prevents two
routine processes from owning a cycle concurrently.

Routine modes run embedded migrations. `serve` does not migrate and therefore
must start only after a writer has initialized the schema.

## Health and status

- `GET /healthz` is process liveness and returns 200 while HTTP is responsive.
- `GET /readyz` returns 200 only when the process can serve its role. It returns
  503 before a usable writer cycle or when durable runtime state cannot be read.
- `GET /api/status` is the operational truth for runtime ownership, source
  health, discovery, identity counts, delivery states, and Telegram gating.

A degraded cycle can remain ready: isolated source or delivery failures should
not remove healthy data from service. A failed or stale cycle requires log and
durable-state investigation.

## Delivery safety

The outbox is durable and unique by `(job, channel, recipient)`. Delivery moves
through `staged`, `pending`, `claimed`, `sent`, `failed`, or `suppressed`.
Claims expire for replay after interruption; retries do not create a second row.

During an owned routine cycle, a delivery pump checks the durable outbox every
500 ms while source crawling continues. A job can be claimed only after its
complete source snapshot activates the delivery row. Telegram messages are then
paced at the configured transport-safe interval; a final drain closes the race
between the last source activation and pump shutdown. Standby and read-only
processes never run this pump.

External Telegram publishing is active only when all of these are true:

1. `RADAR_LITE_DELIVERY_MODE=telegram`
2. Bot token and chat ID are present
3. `RADAR_LITE_PUBLISHING_ENABLED=true`
4. The user explicitly authorized publishing

Use `make telegram-check` for a non-sending credential/channel check. The
`make telegram-smoke` target sends a real message and is an external side effect;
run it only with explicit authorization.

## Safe change and cutover sequence

1. Back up the database and record `/api/status` counts.
2. Deploy with log delivery and verify `/healthz`, `/readyz`, and `/api/status`.
3. Run one complete cycle and compare source, job, identity, and outbox counts.
4. Confirm the intended schema, channel, and recipient. Preserve them during a
   code-only cutover to avoid a recovery flood or a new delivery namespace.
5. Enable Telegram only after explicit authorization, then watch failed,
   pending, and claimed delivery counts.
6. Keep the prior deployment stopped but recoverable until the new owner has
   completed a healthy cycle.

Never run two independently configured writers against the same recipient and
schema during cutover. The lease prevents overlapping cycles, but it does not
make inconsistent configuration safe.
