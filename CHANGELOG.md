## [Unreleased]

### Added

- Added per-channel API key rules with status/keyword matching, configurable error thresholds, temporary auto-recovery, and permanent disable/delete actions.

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
