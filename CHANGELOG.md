## [Unreleased]

### Added

- Added per-channel API key rules with status/keyword matching, configurable error thresholds, temporary auto-recovery, and permanent disable/delete actions.

## [v1.0.0-beta10-fork.7.syh.9] - 2026-11-01

### Added

- Storage retention controls, resumable cleanup with preserved usage summaries, capacity reporting, and controlled database transfer/export tools.
- Read-only historical fixed/image cost previews using frozen prices and retained evidence; missing usage remains unknown.
- Valid text-speed sample counts and consistent row, menu and candidate-list hover feedback.

### Fixed

- Image pricing can use explicit outgoing request dimensions when output dimensions are unavailable, with unconfirmed evidence shown separately.
- Flat fees work without token usage and preserve cache-duration-specific pricing; historical previews retain five-minute and one-hour cache quantities.
- Request logs, consumption rankings, model analytics and performance queries share the CCH per-request speed average, including measured failed/canceled streams and deterministic final-attempt selection.
- Incremental request/channel loading, refresh state, detailed daily channel usage and matched-model price inheritance.

### Release

- Cloud-only amd64/arm64 GHCR images and release.json; no cross-platform binary packages or image archives uploaded from local machines.
- Ent adds usage archives, pricing completeness/speed-version fields and a request-execution cleanup index. Existing saved retention policies remain unchanged; back up before upgrading from .syh.8.
- Historical production bills are not rewritten. Cleanup, database transfer and production deployment remain separately controlled operations.

## [v1.0.0-beta10-fork.7.syh.8] - 2026-10-31

### Added

- Standard/Fast/Ultrafast pricing with official references, per-item multiplier or unit-price overrides, long-context rules, frozen evidence and consistent budget settlement.
- Full Codex model catalogs, client-aware HTTP/WebSocket transport, and complete multi-turn Responses history recovery.
- Inline group/key editing, dashboard period costs, channel daily usage, project read visibility, incremental request/channel lists and refresh controls.
- New-input token details, mode/context cost badges, whole-row expansion, grouped leaderboard defaults and channel cache-ranking costs.

### Fixed

- Regex and prefix model-price matching, matched output limits for budget admission, zero-price preservation and stale price-editor responses.
- Truncated Responses history, unsafe replay after uncertain sends, terminal service-tier parsing and local budget-error attribution.
- Duplicate usage counts, cache-hit ratios, SQLite day boundaries, group request-log GraphQL evidence, date editing and interrupted operation recovery.

### Release

- GitHub Actions publishes amd64/arm64 GHCR images plus release.json by default; cross-platform binaries are optional via publishBinaries and omitted for this version.
- No new database migration; back up before upgrading from .syh.7. Older binaries do not understand the new pricing rules. See the release notes for verification limits.

## [v1.0.0-beta10-fork.7.syh.7] - 2026-10-30

### Added

- Request log calendar and second-precision filters, combined channel/tag/key/group filtering, and persisted URL selections.
- Per-attempt routing, credential hints, sanitized failure evidence, and actual retry/stop decisions; optional channel website links.
- Project API key groups with bulk operations, resumable receipts, direct group usage, and shared key/group monetary budgets.
- Per-image size-based pricing, official DeepSeek/Moonshot wallet queries, and saved default provider filtering.

### Fixed

- Reporting timezone access for project request readers, stale key hints across credential types, and missing stop reasons after streaming output.
- Account balance refresh coupling with key assignment lookups, multi-currency aggregation, and current-page refresh scope.

### Upgrade

- Ent manages new group, operation receipt and budget tables plus nullable request diagnostics and website fields; no new data migration version.
- Backup format 1.6 preserves budgets and group state; older versions reject the new format. Back up the database and configuration before upgrading.
- See [.syh.7 release notes](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-beta10-fork.7.syh.7) for migration, rollback and verification limits.


v0.4.0

- Introduced thread-aware tracing with zero-SDK integration and configurable trace headers
- Added trace visualization interface for following end-to-end conversations
- Added configurable data storage policies to keep or trim trace payloads based on compliance needs

v0.3.0

- Launched project workspace management with per-project API keys and dashboards
- Improved project-scoped permissions and usage insights

v0.2.0

- Added multimodal image generation via chat completions and image_generation tools
- Documented provider support and sample usage for image workflows

v0.1.1

- Add OpenRouter outbound transformer
- Support Google Gemini OpenAI compatible API
