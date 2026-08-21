# Source lifecycle

Radar separates research evidence from production job ingestion.

1. `config/discovery-seed.json` identifies companies worth researching.
2. Discovery resolves candidate career routes with bounded retries and durable
   backoff.
3. The production extractor verifies ownership, provider shape, completeness,
   and technical relevance.
4. A healthy non-empty company-owned board may become a promoted source.
5. Routine cycles ingest complete snapshots, attach identities and provenance,
   and evaluate early-career eligibility.
6. Repeatedly unhealthy discovered routes are demoted and returned to discovery.

Routine scheduling is least-recently-attempted and bounded. New routes cannot
be starved by a slow provider near the front of the catalog, and failed routes
use durable exponential backoff. A global cycle timeout preserves the route's
last real health outcome because an interrupted worker is not evidence that the
upstream source failed.

Empty, ambiguous, mismatched, incomplete, or nontechnical results do not
promote. A source failure is isolated: healthy sources continue and previously
healthy state is retained until a complete replacement snapshot exists.

Broad market-search results cannot promote a generic website fallback. They
must resolve to a recognized, candidate-matching ATS or explicitly supported
company route. This prevents job boards and editorial networks from presenting
copied listings under their own brand. Curated research candidates may still
use the bounded same-site fallback after production extraction succeeds.

## Static and discovered sources

`config/sources.json` is the trusted source floor. Prefer a static source when
the provider and company-owned identifier are known. Discovery supplements this
floor and must not create duplicate monitored routes for an existing source.

When changing either config file:

```sh
make audit
go test ./internal/pipeline ./internal/source/... -count=1
```

Live verification should report candidates attempted, routes probed, healthy,
empty, rejected, promoted, demoted, and failed. Do not promote from search
snippets or copied job listings alone.
