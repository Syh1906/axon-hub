# Usage statistics and consumption leaderboard

Project **Usage statistics** provides user and API key dimensions. The admin **Consumption leaderboard** adds user, API key and channel dimensions, cost/cache rankings and expandable model rows. Keys refer to AxonHub caller API keys, not upstream channel credentials. Full key values are never loaded by these pages.

Fields and algorithms are ported from `claude-code-hub` commit `0c508eaa`: `src/repository/leaderboard.ts`, `src/repository/provider-cache-effectiveness.ts`, `src/lib/request-outcome.ts`, `src/lib/cache-effectiveness/{gate,service}.ts`, and `src/app/v1/_lib/proxy/affinity/fingerprint.ts`. The original MIT notice is retained in [LICENSE.cch](../../../internal/pkg/usageanalytics/LICENSE.cch). CCH has no separate key leaderboard; its user semantics are extended to API keys.

## Scope and permissions

| Route | Data scope | Permission |
| --- | --- | --- |
| `/project/usage-stats` | Current project, explicitly sent through `X-Project-ID` | Existing project-owner or system-owner access |
| `/leaderboard` | All projects by default, with an optional project filter | System-level `read_leaderboard` or system owner |

There is no leaderboard management permission. Navigation, route guards and backend queries enforce read access. A project-level grant does not grant system-wide access. `consumptionProjects` provides project filter options without requiring project administration privileges. The leaderboard ignores the ambient project selected in the global header. Authorization runs before cache reads.

## Dates and interaction

Both pages offer Today, Yesterday, This week, This month, All time and Custom dates. The backend uses the configured system timezone; weeks start on Monday. Custom `YYYY-MM-DD` dates include the entire last day, using an inclusive start and exclusive next-day boundary, including 23/25-hour DST days. Reversed ranges are rejected.

Ent persists timestamps in UTC. Calendar boundaries are calculated in the system timezone and converted to UTC before querying; the cache aggregation clock and windows also use UTC. This prevents SQLite's textual timestamp comparisons from assigning midnight requests to the wrong day or excluding cache samples when the server uses a different timezone.

All time includes retained request history, so log retention affects the available data. Dimension changes preserve the selected period. Column sorting preserves expansion by entity identity; model rows move with their parent. Wide tables scroll within their own container. Expanded rows use continuous muted backgrounds, alternating shades and two-pixel separators.

Name search matches users' displayed full names, name fragments and email addresses case-insensitively; key and channel searches use their names. Headers, parent rows and model rows are centered by column. Sort/expansion icons and model counts do not shift the text alignment.

## Attribution and amounts

Each terminal request counts once, assigned to its final channel. The final channel's latest execution supplies the actual model, status and timings. Usage is aggregated by request/channel/project before joining executions, preventing retries or multiple usage rows from multiplying counts. Actual execution models are used when present, otherwise the request model is used.

Costs sum stored historical `total_cost` values without repricing old requests or changing routing. As in CCH, missing cost aggregates become zero; this means no recorded amount and does not prove that upstream service was free. Total tokens use AxonHub input plus output tokens. Input already includes cached portions, so cache-read and cache-write tokens must not be added again.

As in CCH, deleted users and channels are excluded from their respective rankings. The key extension also excludes deleted keys. A retained user's ranking can still include retained requests from that user's deleted keys. The leaderboard provides no deletion or historical cost editing operations.

## Model details

| Ranking | Model fields |
| --- | --- |
| User/key cost | Model, request count, total tokens, cost |
| Channel cost | Model, requests, cost, total tokens, success rate, average first-token latency, average output speed, cost/request, cost/million tokens, cache coefficient |
| User/key cache | Model, cache-triggered requests, hit rate, cache-read tokens, total input tokens; model cost displays an em dash |
| Channel cache | Model, cache-triggered requests, hit rate, cache coefficient, cache-read tokens, total input tokens |

- Cache-triggered requests have a positive cache-read or cache-write count. Hit rate is total cache-read tokens divided by total input tokens of those requests, clamped to 0–100%. Requests without cache activity are excluded from the denominator; percentages are never averaged.
- Average cost is historical cost divided by request count, or cost multiplied by 1,000,000 and divided by total tokens. A zero denominator produces null.
- Success-rate classification maps CCH's success/failure/excluded rules onto AxonHub executions. HTTP 404, quota/rate-limit errors and client aborts after the first response byte are excluded. New requests canceled before a first byte count as failures. Legacy cancellations without observation evidence remain excluded. Local queue-full, queue-timeout and RPM-exhausted rejections can have execution records even though no request reached the provider. Their synthetic 429 status and corresponding persisted messages exclude them from outcome samples while retaining request counts and historical costs. An ordinary upstream 429 is not automatically excluded by status alone. No valid samples means null.
- First-token latency retains the existing first meaningful output metric. First-byte latency is observed separately when the first response body byte is read, before a complete SSE event; response headers do not count. Responses WebSocket observes the first message payload byte before all fragments arrive, and Vertex preserves the observer when transforming requests.
- Output speed is computed per request as `output tokens * 1000 / (duration ms - first-byte ms)`, then averaged. Missing timings, no output, first byte at/after completion, and intervals below 100ms are excluded.

Cost rankings default to descending cost. User/key cache rankings sort by hit rate and request count. Channel cache rankings sort by coefficient, then hit rate and request count. Missing values sort last, with stable ID/model-name tie breaking.

## Cache coefficient

Sampling supports Anthropic Messages, OpenAI Chat Completions/Responses and Gemini Contents. CCH normalization produces a digest and normalized prefix byte length; theoretical cache tokens are `floor(bytes / 4)`. The digest separates project, caller key, format and requested model. Prompt text is not stored by this collector. The digest is only a sampling marker and does not affect affinity or routing.

Only successfully completed final executions with observable upstream usage are eligible. Failed, canceled, truncated and unobservable attempts remain in the sample count but do not contribute theoretical or observed tokens. Data is grouped by project, final channel, model and 5m/1h TTL bucket.

The existing scheduler runs every five minutes with a 15-minute terminal-state lag. The first run looks back one hour; later runs resume at the latest window. Fixed five-minute windows, a unique key and Ent upserts prevent duplicate accumulation across instances. Empty periods are skipped through an indexed lookup.

Counters are summed across models, TTL buckets and windows before recomputing the score. Existing scores must not be averaged. Integer basis-point arithmetic is used:

```text
raw = clamp(observed * 10000 / theoretical, 0, 10000)
sample factor = eligible >=100:10000; >=30:6000; >=5:3000; otherwise:1000
observable = eligible * 10000 / samples
confidence = observable * sample factor / 10000
coefficient = raw * confidence / 10000
```

Each division truncates. The UI divides the result by 10000 to display 0–1. No window samples produces an em dash. The score normally lags by 15–20 minutes. Requests settling after the buffer may be excluded from the window's eligible samples, matching CCH's display-only limitation. Cost and cache-hit statistics update independently.

## Queries, storage and upgrades

`ConsumptionService` serves the [GraphQL schema](../../../internal/server/gql/consumption.graphql). Parent and model data are grouped in one query; the browser never downloads raw logs for calculation. Search runs on the server with a 300ms client debounce. Results are cached for one minute; keys distinguish view, project, dates, timezone, currency, dimension, metric and search.

Ent adds `cache_effectiveness_windows` and nullable first-byte latency, observation marker, cache digest, theoretical tokens and TTL fields to `request_executions`. Ent manages schema/index generation; no manual schema SQL or new data-migration version is needed. These names were checked against direct upstream `v1.0.0-beta10-fork.7` / `b205ae8e`, without a collision. Original upstream identity remains separately recorded in `release.config.json`.

Missing legacy samples stay NULL; historical timing and cost data are unchanged. SQLite tests verify old-schema upgrade, repeated upgrade, legacy preservation and unique windows. Query expressions use shared SQL constructs and Ent generates dialect-specific upserts. Live MySQL/PostgreSQL upgrades remain unverified.

## Verification

```text
# Repository root
go test ./internal/pkg/usageanalytics ./internal/server/biz ./internal/server/gql -run 'TestCache|TestConsumption' -count=1

# llm/ directory
go test ./httpclient ./vertex ./transformer/openai/responses -run 'TestFirstByte|TestHTTPFirstByte|TestTransformRequestPreservesFirstByteObserver|TestWebSocketFirstByte' -count=1
```

Expected values in `internal/pkg/usageanalytics/testdata/cch-cache-fixtures.json` were generated by the pinned CCH implementation. Tests cover protocol normalization, integer thresholds, cache denominators, retry deduplication, model/TTL aggregation, permission isolation, calendar boundaries, first byte before a complete event, and upgrades.

Live local frontend/backend integration with an isolated SQLite database passed on 2026-10-29, covering all six date ranges, project isolation, read-only/denied access, model expansion, sorting, name search, automatic cache windows and desktop/mobile layouts. Relevant regressions and independent review passed. MySQL/PostgreSQL instance upgrades, real provider requests and production upgrades remain unverified; local checks do not replace acceptance in those environments.
