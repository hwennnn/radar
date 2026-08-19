# Configuration

Radar reads environment variables at process startup. The `RADAR_LITE_` prefix
is intentionally retained for compatibility with existing databases and
deployments; it does not imply a dependency on another repository.

## Core settings

| Variable | Default | Notes |
| --- | --- | --- |
| `RADAR_LITE_DATABASE_URL` | fallback to `DATABASE_URL` | Required for every mode except `discover` and `audit` |
| `RADAR_LITE_SCHEMA` | `radar_lite` | Postgres identifier; routine modes create or migrate it |
| `RADAR_LITE_CATALOG` | `config/sources.json` | Verified source catalog |
| `RADAR_LITE_DISCOVERY_SEED` | `config/discovery-seed.json` | Discovery candidates and presentation metadata |
| `RADAR_LITE_HEALTH_ADDR` | `:8080` | HTTP bind address; `-` or empty disables HTTP where allowed |
| `RADAR_LITE_INTERVAL` | `15m` | Delay between routine cycles |
| `RADAR_LITE_CYCLE_TIMEOUT` | `20m` | Maximum crawl/discovery duration per cycle |

Durations use Go syntax such as `45s`, `15m`, or `6h` and must be positive.

## Discovery settings

| Variable | Default | Notes |
| --- | --- | --- |
| `TINYFISH_API_KEY` | none | Enables discovery; `RADAR_LITE_TINYFISH_API_KEY` takes precedence |
| `RADAR_LITE_TINYFISH_SEARCH_BASE_URL` | provider default | Override for tests or controlled environments |
| `RADAR_LITE_TINYFISH_FETCH_BASE_URL` | provider default | Override for tests or controlled environments |
| `RADAR_LITE_DISCOVERY_BATCH` | `16` | Candidates per cycle; valid range 1–100 |
| `RADAR_LITE_DISCOVERY_TIMEOUT` | `45s` | Per-candidate deadline |
| `RADAR_LITE_DISCOVERY_RETRY` | `6h` | Retry delay after a failed candidate |
| `RADAR_LITE_DISCOVERY_EMPTY_RETRY` | `1h` | Retry delay after a healthy empty candidate |

`reconcile` and `market-once` require a TinyFish key. Normal routine operation
still crawls verified sources without one, but autonomous discovery is disabled.

## Delivery settings

| Variable | Default | Notes |
| --- | --- | --- |
| `RADAR_LITE_DELIVERY_MODE` | `log` | Allowed values: `log`, `telegram` |
| `RADAR_LITE_RECIPIENT` | `local-preview` | Durable recipient identity for log mode |
| `RADAR_LITE_TELEGRAM_BOT_TOKEN` | none | Falls back to `RADAR_TELEGRAM_BOT_TOKEN` |
| `RADAR_LITE_TELEGRAM_CHAT_ID` | none | Falls back to `RADAR_TELEGRAM_CHAT_ID` |
| `RADAR_LITE_PUBLISHING_ENABLED` | false | Must be exactly `true` for Telegram mode |

Telegram startup fails unless mode, both credentials, and the publishing gate
agree. Changing the recipient changes the delivery uniqueness key and must be
treated as a cutover, not a cosmetic configuration edit.

## Compose-only settings

`RADAR_COMMAND` selects the process command. `POSTGRES_USER`,
`POSTGRES_PASSWORD`, and `POSTGRES_DB` configure the bundled database. Do not
commit a populated `.env`; `.env.example` contains non-secret defaults only.
