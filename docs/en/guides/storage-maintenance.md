# Storage cleanup, deployment migration, and ongoing maintenance

Use **System Settings → Storage** for application retention and background cleanup. Host maintenance separately owns migration files, WAL, logs, and image capacity. Deleting rows usually makes database pages reusable; it does not immediately shrink database files or replace backups.

This guide describes the new maintenance implementation. Upgrade older deployments through the [release workflow](../development/release.md), validate in isolation, then enable production policies. All paths and accounts below are examples. Keep real deployment inventories, addresses, measurements, backup destinations, and credentials in ignored `.local/` material or protected machine configuration, never in the public repository.

## Short-lived payload storage

New request/response debug bodies use local gzip caches instead of database JSON or long-lived object storage. Database rows keep only a status/reference. Tokens, costs, and outcomes are independent of payload retention. This follows CCH’s short-lived debugging approach without requiring Redis.

Request lists load summaries only. Full details and drawers request payloads for the active request/response tab, and execution records load when their tab opens. Audio downloads start on the response tab; leaving cancels pending downloads and releases page resources. Video budget recovery selects and reads back only required fields, so status updates do not reload large legacy bodies.

| Data | Maximum age | Uncompressed entry | Compressed total | Entry count |
| --- | --- | --- | --- | --- |
| Request/response/stream debug copies | 5 minutes | 5 MiB | 256 MiB | 4096 |
| Required Responses continuation input/output | 2 hours | 32 MiB | 512 MiB | 2048 |

`store_request_body`, `store_response_body`, and `store_chunks` independently control request bodies, response bodies, and raw stream chunks. Capturing raw chunks requires `store_chunks`, including chunks received before a request fails or is canceled. All three debug categories share the 256 MiB and 4096-entry limits above. Keeping a switch enabled still allows content to expire on the short-cache schedule. The day-based body cleanup settings below apply to legacy payloads and database reference markers.

The per-instance total is 768 MiB of compressed content, excluding filesystem metadata. Capacity/count pressure evicts oldest entries earlier. A minute task expires files; reads also enforce expiry. Oversized debug copies are skipped without truncating forwarded requests. Identical content is deduplicated without extending its original expiry. Detail views distinguish disabled, expired, evicted, and oversized payloads.

`request_payload.directory` defaults to `data/request-payloads` and must be private to each application instance. These disposable files are excluded from database backups. Existing request/response storage switches remain authoritative. Disabling either switch also disables new disk continuation copies; the existing bounded memory cache remains independent and cannot guarantee `previous_response_id` restoration after expiry or restart. Disk continuation, when enabled, stores required input/output scoped by project and API key and can expire or be evicted.

Upgrades do not remove old database/object payloads or shrink database files. Legacy payloads remain readable and follow the cleanup policies below. Binary audio resources and video files retain their separate storage and are outside these JSON cache limits. Forwarding still reads full live requests, so transient memory for very large requests is not eliminated.

### Numeric image request summaries

With `store_request_body` enabled, Responses and Chat Completions payload markers also retain a fixed set of integers: `images.count` (image occurrences), `inline_count`, `inline_encoded_bytes` (total inline encoded data bytes), and `largest_inline_encoded_bytes`. URL and file-ID images count as occurrences only; repeated images count separately. No image contents, URLs, or file IDs are retained in the summary. Images are neither fetched nor decoded, and image-like text or tool arguments are ignored. Encoded bytes are not decoded image size or provider relay capacity.

Request rows summarize the inbound body actually retained in the request log; execution rows summarize the channel request's logged body. Each marker's `size` records that full body's length. Restored HTTP Responses continuation includes historical images, while WebSocket retains its existing display body. These figures therefore do not always describe the client's current upload, nor do they measure bytes on the wire. When a body exceeds 5 MiB, expires, or is evicted, the existing body-reading API returns an `_axon_payload` marker that retains the numeric summary. Available cached bodies are returned unchanged. No new frontend summary display is added.

The summary does not expire with the five-minute file: it remains in the database payload marker until body cleanup (seven days by default on new installations) or request deletion. It remains retained if the relevant cleanup is disabled. Disabling request body storage prevents new summaries. Legacy markers without `images` remain readable without migration. Cache capacity is unchanged, but scanning large requests adds transient CPU and memory work proportional to body size.

## Retention and cleanup

| Resource | New installation default | Effect |
| --- | --- | --- |
| Legacy bodies and payload markers | 7 days | Removes the corresponding content; request cleanup also removes request headers |
| Legacy streaming chunks and markers | 3 days | Removes frame replay; persistence is disabled by default |
| Requests and executions | 60 days | Removes detailed views after preserving compact statistics |
| Usage details | 60 days | Removes detailed prices after preserving compact statistics |
| Cache effectiveness windows | 60 days | Shortens available window history |
| Compact statistics | 365 days | Removes expired historical statistics |
| Channel probes | 3 days | Removes expired probe records |

Existing explicit settings, including disabled policies and previous retention periods, are preserved. New installations enable these cleanup categories. Review and save the policy after upgrading. Retention uses record timestamps.

Archives contain compact per-request/per-usage numerical records, not fixed-size monthly totals. They preserve attribution, models, status, timestamps, tokens, and costs, without bodies, headers, or full price JSON. Leaderboards, dashboard costs, and self-usage combine live data with archives. Detail pages only show retained details. Active requests, unsettled budget charges, and retained traces/threads are protected. Request statistics markers still referenced by retained details may exceed 365 days to prevent double counting.

Budget balances, lifetime accounting, unsettled charges, and duplicate-charge protection are not removed by log retention. Generated videos, uploaded files, and backups require separate lifecycle policies.

Open **Manual cleanup**, select categories, set retention, and inspect the impact. Preview reads at most 10,000 IDs; `≥` means a lower bound. Payload preview counts candidates, while execution skips already empty payloads. Categories overlap and cannot be summed as distinct requests. Table sizes include entire tables and indexes, not bytes this operation will release. External object sizes are unknown; SQLite falls back to total database size rather than scanning payloads for estimates.

The server persists the latest task. Active status polls about every 1.5 seconds while the page is visible; closing the page does not stop execution. Pause takes effect after the current batch; resume follows committed checkpoints. Ordinary timeouts or database contention wait about 30 seconds before continuing. Other failures require investigation and explicit resume.

Automatic and manual submissions share one database task slot. Active tasks, including paused tasks, are reused; retrying the last submission key returns its existing result. Processes must share the primary database for mutual exclusion. Batch changes and checkpoints commit together, and an expired 30-second owner lease allows restart recovery. Only the latest task is retained, not a full task history. External deletion cannot be atomic with the database; a retry may delete an already absent object again.

## Protecting request traffic

Defaults are 100 records per batch, 250 ms between batches, 3 seconds per batch, and a 300-second execution slice. Cleanup yields when primary connection use reaches 60% of the configured pool limit. PostgreSQL lock waits are limited to 250 ms per batch; a lock timeout rolls back the current batch, preserves progress, and waits before continuing automatically. Other database errors remain failed and require investigation before resuming. Save limits through **Manual configuration**, then save page settings; a running task keeps its starting settings.

These are contention controls, not a guarantee of zero latency impact or a hard CPU/memory quota. Large individual payloads, storage, and database I/O still matter. For a historical backlog, start with 20–50 records and 500–1000 ms intervals. Watch success rate, P95/P99 latency, connection waits, disk latency, and WAL growth; pause cleanup if request performance degrades.

`gc.cron` defaults to `0 2 * * *`, explicitly **02:00 UTC**, independently of UI timezone. The runner checks available tasks every 2 seconds. The new online path does not invoke full database compaction, even if legacy `gc.vacuum_enabled` / `gc.vacuum_full` settings remain.

## Capacity and routine operations

The page caches measurements for 30 seconds and refreshes about every 60 seconds while mounted. PostgreSQL/MySQL expose database/table metadata; SQLite uses page count × page size. Automatic disk detection describes the application directory; configure a mounted path when needed. A container filesystem does not establish host or remote database capacity.

Defaults are 70% warning, 85% critical, and a reserve of the greater of 15% of the disk or 2 GiB. Free space below the reserve also warns; below half the reserve is critical. Thresholds and reserve are configurable. Host status also reports inode use.

| Frequency | Inspect | Response |
| --- | --- | --- |
| Every 5 minutes | Database, payload and export volumes; inodes; WAL errors | Connect existing alerting, notify on meaningful failures and recovery |
| Daily | Cleanup/export completion, expiration exit status, net growth | Investigate failures the same day, not just whether a schedule fired |
| Weekly | Tables/indexes, dead tuples, autovacuum, migration leftovers, capacity trend | Tune retention and batch limits; estimate days to reserve threshold |
| Monthly and before upgrades | Isolated restore, configuration, permissions, statistics | Record actual recovery point and elapsed recovery time |

Compare measurements taken at consistent times over 7–14 days. Divide remaining headroom by positive daily net growth to estimate time to the reserve. Zero net growth does not eliminate peaks. New images, migrations, dumps, and rollback need additional room; 15% is not a universal upgrade budget.

Keep PostgreSQL autovacuum/analyze enabled. PostgreSQL `VACUUM FULL`, MySQL `OPTIMIZE TABLE`, and SQLite `VACUUM` may lock tables or require substantial extra space; evaluate them in a maintenance window. Do not repeatedly delete because files did not shrink. Remove only explicitly identified unused images, retaining current and rollback images. Do not schedule `docker system prune --volumes`.

## Exporting and importing a deployment

This phase supports migration, without requiring an offsite repository, continuous WAL archiving, or a full disaster-recovery system. Existing application backup controls remain available; cleanup and migration are independent.

| Method | Purpose | Scope |
| --- | --- | --- |
| Application JSON | Move selected small business configuration | Only supported, selected content; not all users, archives, database objects, or external files |
| Full UI transfer / PostgreSQL CLI export | Move the deployment's complete business database to another PostgreSQL deployment | Users, permissions, settings, channels, keys, budgets, retained details, and compact statistics |
| External storage transfer | Preserve required bodies, videos, and attachments | Copy actual filesystem/object-store objects separately; database exports contain their references |

For a whole deployment, prefer database export. Application JSON alone may restore channels while omitting users or archived statistics. Dumps contain sensitive configuration: transfer privately, never through Git, public download links, or chat. Cluster roles, other databases, environment settings, and external objects are outside a single-database export.

### Full transfer in the UI (recommended)

Open **System settings → Backup → Full deployment transfer**. Only the system owner can use it;
requests do not inherit the selected project. It requires a PostgreSQL URL connection and matching
server/client major versions for `pg_dump` and `pg_restore`. The image includes PostgreSQL 17 clients;
custom images can select a matching `POSTGRES_CLIENT_PACKAGE`. Source application and PostgreSQL
versions cannot exceed the destination. Normal schema/data migrations still run after switching;
cross-dialect or arbitrary historical-version compatibility is not promised.

1. On the source, create a full transfer file. The server exports a consistent snapshot in the background. Download the completed `.axon.tar` using the browser’s native download flow; the whole database is never buffered in memory.
2. On a new destination without business data, select the file and upload it for validation. Keep the page open during upload. The server checks the fixed package layout, byte count, SHA-256 and versions, then shows source version, size and export time.
3. Review the import, enter a **nonexistent** database name and confirm. A temporary restricted login restores into a separate new database without overwriting the initialization database. Only one transfer runs at a time; repeating the same submission does not restore twice.
4. When the new database is ready, change the destination database name in its connection settings and restart. Sign in using a source account, verify users, channels, keys, ledger, archives and external storage, then switch traffic. Imported cleanup/transfer jobs are removed, login signatures reset and imported automatic backups disabled; business API keys remain. The application connection is not switched automatically.

Import only files from your own trusted source. Checksums verify integrity, not provenance; native
archives can contain database code. External payloads, videos, object storage, environment variables
and machine configuration are not in the package. For final cutover, stop source writes and export a
final snapshot. Later requests are not merged. The tool does not stop request services or change routing.

Export shows actual written bytes; upload shows transferred percentage; restore shows the phase and
last update while data, indexes and the transaction complete. Reading all bytes is not reported as
successful restore. Export/restore continue after leaving the page; leaving during upload interrupts it.
Interrupted/timed-out imports are **never replayed automatically**. The new database remains for inspection;
removing temporary files does not delete it. Background maintenance retries temporary-role cleanup.
Read the existing task after an unknown response instead of submitting another import.

Generic configuration (adapt paths privately; never commit machine details):

```yaml
database_transfer:
  work_dir: data/migration
  max_file_gib: 20
  timeout_seconds: 3600
  retention_hours: 24
  database_disk_path: /mnt/database-volume
  restore_reserve_gib: 2
```

Environment variables use `AXONHUB_DATABASE_TRANSFER_`, e.g. `AXONHUB_DATABASE_TRANSFER_WORK_DIR`.
The work directory must be writable and support atomic replacement and hard links. Multiple instances
need a shared directory or sticky routing for admin requests. A dedicated primary-database session owns
the lock; files are not replicated automatically. Default timeout is one hour and file retention one day;
locked recovery/expiry checks run each minute. A new task removes the previous terminal task’s temporary
files, so save needed downloads first.

Export preflights the work volume against source database size. Restore reserves the greater of twice
source database size and `restore_reserve_gib`, plus the maintenance emergency reserve. The database disk
path defaults to empty and must point to an accessible location on the **actual database volume**;
imports are disabled if that capacity cannot be verified. App-container free space is not a substitute
for a remote database volume. Both volumes are checked during execution; crossing the reserve stops the
task. Cancelled/timed-out uploads interrupt network reads.

Native tools use no parallel restore, lower CPU priority and bounded session working memory. These
limits reduce competition but do not guarantee zero production I/O impact; use a low-traffic window
for large databases and observe server load. Configure reverse-proxy upload sizes, timeouts and request
streaming to avoid proxy-level whole-file buffering. Checks do not reserve disk quota or replace database
host monitoring.

### Optional command-line transfer

The CLI below uses separate `.dump` / `.json` files, not the UI’s `.axon.tar` package. Its lock is independent
of the UI lock: **do not run both migration entry points concurrently**.

### Four migration steps

Use [`scripts/maintenance/maintenance.py`](../../../scripts/maintenance/maintenance.py), Python 3.10+, and compatible native `psql`, `pg_dump`, and `pg_restore`. This tool implements **PostgreSQL to PostgreSQL** migration, not SQLite/MySQL or cross-dialect conversion. Test against the target version in isolation first.

**1. Configure the source and export.** Copy `config.example.json` to protected machine configuration and set actual volumes, export/state directories, and database name. Directories must exist. Use protected libpq services/environment configuration; do not put passwords in public JSON or command arguments.

```bash
python3 scripts/maintenance/maintenance.py --config /etc/axonhub-maintenance/config.json status
python3 scripts/maintenance/maintenance.py --config /etc/axonhub-maintenance/config.json export
```

Export streams a consistent snapshot to a unique temporary file, then produces `.dump` / `.json` pairs with SHA-256, bytes, source database size, and time. It does not modify the source database or load the whole dump into memory. Requests can continue, but writes after the snapshot are not included.

**2. Copy both files to the target.** Also transfer deployment configuration and required external bodies/videos, preserving object keys/path relationships. Set target-private configuration to the actual destination volumes. Do not point the target application at the import database yet.

**3. Verify and import a new database.** Replace `EXAMPLE` with the returned receipt name:

```bash
python3 scripts/maintenance/maintenance.py --config /etc/axonhub-maintenance/config.json verify --receipt /var/backups/axonhub/exports/axonhub-export-EXAMPLE.json
python3 scripts/maintenance/maintenance.py --config /etc/axonhub-maintenance/config.json restore --receipt /var/backups/axonhub/exports/axonhub-export-EXAMPLE.json --database axonhub_import_test
```

Verification proves integrity/header format, not successful import. Restore creates only a valid nonexistent database, rejecting the configured online database and any existing destination. It never automatically drops or overwrites a database. The execution account needs creation/import privileges; prefer the target application database owner, or review ownership/access before switching if an administrator imports. Single-transaction failure leaves the isolated target for investigation.

Set `postgres.restore_monitor_path` to the actual target database volume. Preflight requires twice the uncompressed source size plus reserve; raise `restore_required_gib` for larger index/temp requirements. Old receipts without source size require an explicit budget. Compressed bytes cannot predict restored size, and preflight does not reserve a filesystem quota: monitor concurrent writes during import.

**4. Validate and switch.** With a compatible application version, check login, channel/model/key counts, budget balances, recent/historical statistics, and external objects. Review restored schedules to prevent unintended old tasks. Only then switch application database configuration and traffic. Keep the source available for rollback.

An online dump is not lossless zero-downtime cutover. Agree on a final snapshot/traffic-switch window; continued source writes after export are not automatically merged. If continuous writes and zero tail loss are required, use a separately designed replication/incremental migration. Re-importing over an online database is not a solution. This tool does not stop request traffic or modify routing.

### Concurrency, limits, and export expiration

Mutation operations share an OS lock in `state_directory`; duplicate work fails while the original continues. Default timeout is one hour, killing the local POSIX process group on timeout. Mutation operations reject Docker/SSH `exec_prefix`: a killed client cannot reliably cancel remote work or impose its resource limits. Run the script in the actual constrained environment, using native clients to connect. A remote target requires measurement of its real database volume, not the client's disk.

`status` stays lock-free and reports visible filesystems, inodes, database/table occupancy, and configured WAL archiving failures. Exit 0 is healthy, 2 indicates capacity/WAL alert conditions, and 1 is command failure. Existing monitoring or a reviewed systemd `OnFailure` integration is needed for notifications. No new notification channel is required for this phase, and the tool sends no messages or changes to request serving.

Managed exports default to 7 days and at least the newest 2 pairs, configurable through `export_retention_days` / `export_min_keep`. Move migration files still in use to a separate archive directory or adjust retention. Preview before applying:

```bash
python3 scripts/maintenance/maintenance.py --config /etc/axonhub-maintenance/config.json expire-exports
python3 scripts/maintenance/maintenance.py --config /etc/axonhub-maintenance/config.json expire-exports --apply
```

Only matching ordinary dump/receipt pairs inside the configured directory are eligible; no recursion or links. Crash `.partial` files and unpaired outputs remain for inspection. Check weekly and reclaim only when no writer owns them.

Capacity and expiration systemd examples are supplied, without installation. Review account privileges/paths; timers use host timezone. The generic service uses low I/O priority, 20% CPU quota, 256 MiB soft and 512 MiB hard memory limits. These constrain its process group, not another database service. Windows validation does not establish Linux systemd success. Trial export/import before adjusting production limits. Bound journal and Docker logs independently.

Existing application JSON defaults remain daily/30-day retention, preserving stored settings. Database payload storage is rejected as an automatic backup target because it writes no backup file. JSON expiration runs after successful backups and only logs failures; asynchronous trigger success is not file completion, and repeated triggers do not share the new cleanup task lock. Use that UI for small configuration transfers and verify results; use the streaming database tool for large migrations.

## Capacity emergencies and migration acceptance

- At 70% or below reserve: inspect growth, failed/paused cleanup, old exports and WAL; reclaim verified expired payloads/logs/artifacts or expand storage.
- At 85% or below half reserve: pause additional imports/exports/upgrades, expand or move export files, and prioritize request traffic. Do not force-delete retained or unsettled records.
- Near exhaustion: never remove volumes, database files, or `pg_wal`. Operators decide expansion/failover and persistence tradeoffs; the tool does not change routing or stop serving.
- Before upgrades: prove an isolated import, trace source/image versions, and allow migration/rollback headroom. Roll out deployments sequentially. Initial index creation on large tables may require a maintenance window.
- Older application versions do not understand new archives after detail deletion; replacing the image cannot restore complete historical queries. Use schema-compatible versions for migration.
- Record export/verify/import results, data reconciliation, actual elapsed time, omitted external resources, cutover point, and rollback. Tool delivery is not production migration acceptance.
