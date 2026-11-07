# Usage statistics and consumption leaderboard

Project **Usage statistics** provides key group, model, API key and user dimensions. The admin **Consumption leaderboard** also provides the channel dimension and cost/cache rankings. Keys refer to AxonHub caller API keys. Full key values are never loaded by these pages.

Fields and algorithms are ported from `claude-code-hub` commit `0c508eaa`: `src/repository/leaderboard.ts`, `src/repository/provider-cache-effectiveness.ts`, `src/lib/request-outcome.ts`, `src/lib/cache-effectiveness/{gate,service}.ts`, and `src/app/v1/_lib/proxy/affinity/fingerprint.ts`. The original MIT notice is retained in [LICENSE.cch](../../../internal/pkg/usageanalytics/LICENSE.cch). CCH has no separate key leaderboard; its user semantics are extended to API keys.

## Scope and permissions

| Route | Data scope | Permission |
| --- | --- | --- |
| `/project/usage-stats` | Current project, explicitly sent through `X-Project-ID` | Current-project `read_requests` or system owner |
| `/leaderboard` | All projects by default, with an optional project filter | System-level `read_leaderboard` or system owner |

There is no leaderboard management permission. Navigation, route guards and backend queries enforce read access. A project-level grant does not grant system-wide access. `consumptionProjects` provides project filter options without requiring project administration privileges. The leaderboard ignores the ambient project selected in the global header. Authorization runs before cache reads.

Project usage orders tabs as Key groups → Models → API keys → Users and defaults to key groups with expandable models. Project read access includes other members' personal-key requests, while write permissions and project isolation remain independent.

The consumption leaderboard orders tabs as Channels → Key groups → Models → API keys → Users and defaults to channels.

The standalone model dimension combines channels by the final upstream model name, using the requested model when no execution model was recorded. Live and archived records contribute to one row per model, with model-name search. Rows show requests, cost, tokens, success rate, average first-token latency, average output speed and overall cache-hit rate. The model cache ranking shows cache-triggered requests, hit rate, cache-read/input tokens, average first-token latency, average output speed and costs for those requests. Missing valid samples display an em dash. Model names align left and numeric cells align centrally.

## Dates and interaction

Both pages offer Today, Yesterday, This week, This month, All time and Custom dates. The backend uses the configured system timezone; weeks start on Monday. Custom `YYYY-MM-DD` dates include the entire last day, using an inclusive start and exclusive next-day boundary, including 23/25-hour DST days. Reversed ranges are rejected.

Ent persists timestamps in UTC. Calendar boundaries are calculated in the system timezone and converted to UTC before querying; the cache aggregation clock and windows also use UTC. This prevents SQLite's textual timestamp comparisons from assigning midnight requests to the wrong day or excluding cache samples when the server uses a different timezone.

All time includes retained request history, so log retention affects the available data. Dimension changes preserve the selected period. Column sorting preserves expansion by entity identity; model rows move with their parent. Wide tables scroll within their own container. Expanded rows use continuous muted backgrounds. Hover and keyboard focus apply a shared theme background to every cell without scaling or changing row height. Candidate lists, menus and ghost buttons share the feedback, including dark mode and reduced motion.

The table mounts only the visible rows and a small buffer, including expanded models, so large key lists do not create all cells at once. Measured row heights use normal table flow and stable column widths. See "Page reads and browser caching" below for frontend refresh intervals and memory-cache lifetime. The backend retains its one-minute result cache and loads only ID, name, user, project and group fields for key display labels.

Name search matches users' displayed full names, name fragments and email addresses case-insensitively. Key, channel and model searches use their names. Numeric cells are centered by column. Sort/expansion icons and model counts do not shift the text alignment.

## Attribution and amounts

Configure amount precision under **System Settings → General → Amount decimal places**. **Keep existing precision** preserves each page's current defaults. After an administrator saves 0–8 decimal places, costs and quotas on the consumption leaderboard, project usage statistics, key groups, and API keys are rounded and padded to that precision. Project users also read this display preference. Selecting the default restores the original display rules.

This affects frontend display only: stored amounts, cost/quota API values, calculations, sorting, quota inputs, and budget enforcement retain their existing precision. For example, `1.23456789` displays as `1.23` with two decimal places while calculations continue using the original amount.

Each terminal request counts once, assigned to its final channel. The final channel's latest execution supplies the actual model, status and timings. Usage is aggregated by request/channel/project before joining executions, preventing retries or multiple usage rows from multiplying counts. Actual execution models are used when present, otherwise the request model is used.

Costs sum stored historical `total_cost` values without repricing old requests or changing routing. The UI shows evidenced amounts without a partially-unpriced label, asterisk, or pricing-coverage count. When no cost evidence exists it shows a dash; an explicit zero stays zero. Cost/request uses only fully priced requests. Cost/million tokens uses fully priced requests with positive input/output tokens, with cost and tokens from the same sample. One request with missing evidence does not hide averages from other valid samples; a dash appears only without valid samples. Explicit zero costs participate; tokenless flat-fee requests do not distort token averages. Total tokens use AxonHub input plus output tokens. Input already includes cached portions, so cache-read and cache-write tokens must not be added again.

As in CCH, deleted users and channels are excluded from their respective rankings. The key extension also excludes deleted keys. A retained user's ranking can still include retained requests from that user's deleted keys. The leaderboard provides no deletion or historical cost editing operations.

Overall cache-hit rate in cost rankings and usage summaries divides all cache-read tokens by all input tokens, including cache misses; a zero denominator displays an em dash. The cache ranking retains its cache-triggered request denominator.

`total_cost` reflects the request's final rates, service tier and applied channel/key multiplier. Averages neither substitute standard cost nor apply that multiplier again. For example, a standard subtotal of 3, a final multiplier of 0.25 and 1.1 million eligible tokens produce actual cost 0.75 and cost/million tokens of approximately 0.681818. Late settlement evidence updates the previously unpriced usage record atomically with budget accounting, so the leaderboard does not remain unpriced after a successful settlement.

## Model details

| Ranking | Model fields |
| --- | --- |
| User/key/group cost | Model, request count, total tokens, overall cache-hit rate, cost, cost/million tokens |
| Channel cost | Model, requests, cost, total tokens, success rate, average first-token latency, average output speed, cost/request, cost/million tokens, cache coefficient, overall cache-hit rate |
| User/key cache | Model, cache-triggered requests, hit rate, cache-read tokens, total input tokens; model cost displays an em dash |
| Channel cache | Model, cache-triggered requests, hit rate, cache coefficient, cache-read tokens, total input tokens |

- Cache-triggered requests have a positive cache-read or cache-write count. Hit rate is total cache-read tokens divided by total input tokens of those requests, clamped to 0–100%. Requests without cache activity are excluded from the denominator; percentages are never averaged.
- Cost/request divides fully priced request costs by their request count. Cost/million tokens divides costs from fully priced requests with positive tokens by the same requests’ input/output tokens, multiplied by 1,000,000. Empty denominators return null. Archives use the same samples; legacy archives with unknown pricing completeness do not count as fully priced.
- Daily model details on the channel page include average first-token latency (ms), average output speed (tok/s), success rate and cost/million tokens, without pricing-coverage indicators. The three performance metrics use the eligibility rules below and only finished requests; missing samples appear as a dash. Channel-list costs, hover-card totals and per-model costs follow the system's decimal display setting, retaining the original six-digit display when unset.
- Details load on demand and refresh every 30 seconds while open; closing stops polling. Browser results remain cached for five minutes and are fresh for 20 seconds. The server retains at most 64 channel/day results for ten seconds and coalesces concurrent reads. Authorization runs on every call, transactional reads bypass shared caching, and different natural days use different keys. Closing or quickly reopening cancels only that caller's wait; shared computation remains available to other callers with a 30-second limit. A single database read snapshot and stable channel/day cursor bound each batch, joining final attempts and usage in one read and reusing historical price versions. Historical cost and standard-cost semantics remain unchanged.
- Success-rate classification maps CCH's success/failure/excluded rules onto AxonHub executions. HTTP 404, quota/rate-limit errors and client aborts after the first response byte are excluded. New requests canceled before a first byte count as failures. Legacy cancellations without observation evidence remain excluded. Local queue-full, queue-timeout and RPM-exhausted rejections can have execution records even though no request reached the provider. Their synthetic 429 status and corresponding persisted messages exclude them from outcome samples while retaining request counts and historical costs. An ordinary upstream 429 is not automatically excluded by status alone. No valid samples means null.
- First-token latency retains the existing first meaningful output metric. First-byte latency is observed separately when the first response body byte is read, before a complete SSE event; response headers do not count. Responses WebSocket observes the first message payload byte before all fragments arrive, and Vertex preserves the observer when transforming requests.
- Output speed is computed per request as `output tokens * 1000 / (duration ms - first-byte ms)`, then averaged. Only uniquely matched final text streams qualify. Non-stream responses, image/audio/video outputs, missing timings, no output, first byte at/after completion and intervals below 100ms are excluded. Saved image evidence also excludes Gemini/Responses image results; audio usage excludes audio output. The leaderboard reports valid sample counts. Request logs, model analytics, performance charts and channel probes use this formula: 100 and 500 tok/s average to 300, rather than dividing total tokens by total duration. Internal routing scores remain separate.
- Failed and canceled requests with measured partial output also qualify as speed samples. Model and channel rows expose valid sample counts. Model analytics and probes retain completed/failed attempt counts and success-rate denominators; first-token latency still uses completed executions only. Final attempts are selected by execution ID, including retries with identical timestamps.

Cost rankings default to descending cost. User/key cache rankings sort by hit rate and request count. Channel cache rankings sort by coefficient, then hit rate and request count. Missing values sort last, with stable ID/model-name tie breaking.

## Cache coefficient

Sampling supports Anthropic Messages, OpenAI Chat Completions/Responses and Gemini Contents. CCH normalization produces a digest and normalized prefix byte length; theoretical cache tokens are `floor(bytes / 4)`. The digest separates project, caller key, format and requested model. Prompt text is not stored by this collector. The digest is only a sampling marker and does not affect affinity or routing.

Current eligibility requires a successfully completed final execution and exactly one usage row for the same request, channel and project. Multiple rows cannot be attributed uniquely to the final execution: they remain observable samples but contribute neither eligible samples nor theoretical/observed tokens. Failed, canceled, truncated requests and requests without usage rows are also excluded. Billing and token totals continue to retain all recorded usage. Data is grouped by project, final channel, model and 5m/1h TTL bucket.

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

The coefficient compares observed cache utilization under similar workloads. It is neither a billing discount nor the cache-hit rate. With all samples eligible and observed reads equal to half the estimated cacheable tokens, 100 eligible samples yield about 0.50; 10 samples apply a 0.3 factor and yield about 0.15. A score of 0.50 does not mean half-price billing.

### Known limitations and deferred correction

The 2026-11-07 review confirmed that one usage row does not prove that the provider reported input usage. Tokenless flat-fee billing can create a zero-token row, and protocol conversion currently loses the distinction between a missing input field and an explicitly reported zero. This change only fixes ambiguous multi-row attribution; it does not fully correct the coefficient. Existing windows are not automatically recomputed and historical ranges can retain old results. The user explicitly deferred the complete correction:

1. Preserve missing, null and explicit-zero input evidence through Chat, Responses, Anthropic and Gemini parsing, including the relevant events during stream aggregation.
2. Carry this evidence through unified usage and persist it per execution, including budget recovery and late settlement. Leave historical evidence unknown; neither positive token counts nor enabled budgets substitute for evidence.
3. Define a window calculation version and the scope of reliable historical recomputation. Do not mix new/old evidence, change released migration behavior or infer missing values from stored zeros.
4. Cover missing/null/zero, split stream events, retries, disabled budgets and recovery, while preserving cost/token totals and checking upgrades and idempotent aggregation.

This local correction adds no schema/data migration and changes neither pricing nor budgets.

## Queries, storage and upgrades

`ConsumptionService` serves the [GraphQL schema](../../../internal/server/gql/consumption.graphql). Parent and model data are grouped in one query; the browser never downloads raw logs for calculation. Search runs on the server with a 300ms client debounce. Results are cached for one minute; keys distinguish view, project, dates, timezone, currency, dimension, metric and search.

Ent adds `cache_effectiveness_windows` and nullable first-byte latency, observation marker, cache digest, theoretical tokens and TTL fields to `request_executions`. Ent manages schema/index generation; no manual schema SQL or new data-migration version is needed. These names were checked against direct upstream `v1.0.0-beta10-fork.7` / `b205ae8e`, without a collision. Original upstream identity remains separately recorded in `release.config.json`.

`usage_archives` adds nullable `pricing_complete` and `speed_version` defaulting to 0. New archives retain pricing completeness and corrected speed version 1. Old archives lack sufficient per-request evidence and are excluded from corrected speed averages; unknown pricing completeness does not become confirmed zero cost. Ent manages these columns without changing released data migrations.

Missing legacy samples stay NULL; historical timing and cost data are unchanged. SQLite tests verify old-schema upgrade, repeated upgrade, legacy preservation and unique windows. Query expressions use shared SQL constructs and Ent generates dialect-specific upserts. The beta12 merge validated isolated SQLite/MySQL/PostgreSQL upgrades and repeated startup. The beta12-fork.1 rollout also checked schema fields, version and configuration ID sets on both production PostgreSQL hosts. See the [maintenance ledger](../../fork-maintenance.md#github-独立发布与两层上游追溯) for scope and receipts; this does not validate every production statistics workflow.

## Verification

```text
# Repository root
go test ./internal/pkg/usageanalytics ./internal/server/biz ./internal/server/gql -run 'TestCache|TestConsumption' -count=1

# llm/ directory
go test ./httpclient ./vertex ./transformer/openai/responses -run 'TestFirstByte|TestHTTPFirstByte|TestTransformRequestPreservesFirstByteObserver|TestWebSocketFirstByte' -count=1
```

Expected values in `internal/pkg/usageanalytics/testdata/cch-cache-fixtures.json` were generated by the pinned CCH implementation. Tests cover protocol normalization, integer thresholds, cache denominators, retry deduplication, model/TTL aggregation, permission isolation, calendar boundaries, first byte before a complete event, and upgrades.

Live local frontend/backend integration with an isolated SQLite database passed on 2026-10-29, covering all six date ranges, project isolation, read-only/denied access, model expansion, sorting, name search, automatic cache windows and desktop/mobile layouts. Relevant regressions and independent review passed. MySQL/PostgreSQL instance upgrades, real provider requests and production upgrades remain unverified; local checks do not replace acceptance in those environments.

## API key groups

Key and group dimensions can filter a specific group. Options come from groups with usage in the selected reporting scope. Ungrouped and deleted keys are excluded from group rankings. Identically named groups in different projects remain separate IDs. Global labels append the project name; a single-project scope keeps the original group name. Historical usage follows current membership; renames and reassignment change cache revisions immediately. Existing page permissions remain in force. See [API Key self usage](../guides/self-usage.md#managing-key-groups) for management, default filters and public group reporting.

## Page reads and browser caching

Ordinary admin data reads have a 35-second client deadline, including response-body consumption. Initialization and current-user checks have a 10-second deadline and offer explicit retry on failure. Mutations, channel tests and model streams retain their separate contracts. Queries wired to cancellation stop obsolete reads when filters change or pages unmount; timed-out reads are not repeatedly retried automatically.

Yesterday and custom-date consumption views refresh every five minutes; other ranges retain their one-minute interval. Manual refresh remains available. Inactive statistics and model lists remain in browser memory for up to five minutes, with freshness-based revalidation on return. Historical costs are not treated as immutable, and business data is not persisted for offline use. A bounded currency formatter cache preserves the existing numeric and precision semantics.

On PostgreSQL, rankings without search or a selected API key group select each request's final execution and metrics together, matching its project and channel. Search and selected-group queries retain indexed per-request lookups to avoid scanning the entire time range for a narrow result. Other dialects, archives and bounded detail reads retain their existing paths. Metric definitions and permissions are unchanged.

The model table renders visible rows with a small overscan. Sorting, developer grouping and select-all still use the complete loaded list; scrolling does not clear explicit selections or expanded rows. The API still loads the complete model list before display, so this rendering optimization is not server-side pagination.
