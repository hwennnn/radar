# HTTP API

The API is read-only, returns JSON, and sends `Cache-Control: no-store` for
pipeline data. Unknown UI paths return 404.

## `GET /api/jobs`

Returns eligible active openings grouped by normalized company, title,
location, and track. Exact canonical apply URLs are shown once while distinct
requisition URLs remain separate openings.

Query parameters:

| Parameter | Values | Behavior |
| --- | --- | --- |
| `q` | text | Case-insensitive company, title, and location search |
| `location` | `all`, `singapore`, `us` | Derived region filter |
| `track` | `all`, `internship`, `new_grad` | Early-career track filter |
| `role` | `all`, `software`, `ai_ml`, `data`, `infra_security`, `quant` | Role category filter |
| `sort` | `recent`, `company` | Defaults to newest first |
| `limit` | positive integer | Defaults to 50 and is capped at 500 |

The response contains `jobs`, an untruncated `total`, `showing`, `limit`, and a
summary with eligible openings, grouped roles, companies, recency, and source
health. A database failure returns an `{ "error": "..." }` body with status 500.

## `GET /api/status`

Returns one consistent operational snapshot:

- `runtime` — mode, readiness, cycle ownership, last result, and cycle counts;
- `sources` — configured, observed, healthy, empty, failed, pending, failures,
  and the monitored company roster;
- `discovery` — candidate and promoted-source counts;
- `dedupe` — canonical jobs, aliases, observations, and multi-source jobs;
- `deliveries` — counts by durable delivery state;
- `telegram` — credentials, authorization gate, and external-publishing state.

Provider diagnostics are sanitized before reaching this endpoint. Consult
structured service logs for private failure detail.

## Health and UI

`GET /healthz` reports liveness. `GET /readyz` reports readiness and last-cycle
summary. The embedded UI is served at `/jobs`, `/companies`, `/system`, and
`/docs`; `/` serves the jobs application shell.
