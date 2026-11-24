# Independent releases and upstream maintenance

On November 24, 2026, both production sites upgraded to [fork.26](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.26). This version improves Responses WebSocket reuse and updates key-scoped channel rate displays and the Codex model catalog. Upgrading from fork.25 requires no database migration. Switch timings, in-flight request outcomes and deployment checks are recorded in the [maintenance ledger](../../fork-maintenance.md).

On 2026-11-24, both production sites upgraded from fork.24 to [fork.25](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.25). Each site verified the new channel rate tables, billing columns and indexes, and checked a pre-upgrade usage record. The applications took 6.855 and 42.433 seconds to rebuild and report healthy. The [maintenance ledger](../../fork-maintenance.md) records the outcomes of in-flight requests and the conditions for rolling back.

Both production sites upgraded to [fork.24](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.24) on 2026-11-24. The release aligns refresh behavior on the API key and group usage pages and requires no database migration. The [maintenance ledger](../../fork-maintenance.md) records the pinned image digest, health checks and request counts after each switch.

Both production sites upgraded to [fork.23](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.23) on 2026-11-23. The archive index on `request_id` was verified on each database. The [maintenance ledger](../../fork-maintenance.md) records how long each application took to rebuild and reach healthy status, and what happened to requests in flight during each switch.

Both production sites upgraded to [fork.22](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.22) on 2026-11-23. The receiving site was upgraded first. The sending site now negotiates dictionary compression on its direct HTTPS routes to that receiver. The [maintenance ledger](../../fork-maintenance.md) records the image digest, requests affected during each cutover and observations from production traffic.

Both production sites upgraded to [fork.21](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.21) on 2026-11-22. The admin console now controls adaptive protocol selection and request compression defaults while honoring existing pinned endpoints. The [maintenance ledger](../../fork-maintenance.md) records the shared image digest, health checks and requests affected during the cutovers.

Both production sites upgraded to [fork.20](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.20) on 2026-11-21. The request log and API key usage table show the sent and billed service tiers independently. This release required no database migration. The [maintenance ledger](../../fork-maintenance.md) records health checks, in-flight request outcomes and post-deployment verification.

On 2026-11-21, both production sites upgraded to [fork.19](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.19). Large, slow Responses uploads can now learn a transport preference from normal traffic. See the [maintenance ledger](../../fork-maintenance.md) for the image digest, health checks and in-flight outcomes.

Both production sites deployed [fork.18](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.18) on 2026-11-21. The session and reasoning-evidence migration completed. The verified Responses endpoint now uses HTTP/1.1 keep-alive and request compression. See the [deployment ledger](../../fork-maintenance.md) for cutover times, in-flight requests and production checks, and the [changelog](../../../CHANGELOG.md) for the fork.17 rollback condition.

On 2026-11-21 both production sites upgraded to the [fork.17 image-only release](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.17). The [deployment ledger](../../fork-maintenance.md) records the image digest, schema and model card checks.

On 2026-11-20, [rc1-fork.16](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.16) was published through the image-only workflow and deployed to both production sites. Local and public checks passed. The [deployment ledger](../../fork-maintenance.md) records request outcomes during the switch.

Every release uses the current maintenance branch and its latest published baseline in this repository. Increment the fork number on that baseline. A normal release verifies this repository’s branch, tags, manifest and recorded upstream provenance. Discovering or merging newer upstream releases is a separate operation performed only when explicitly requested.

On 2026-11-20, [rc1-fork.15](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.15) was released from the fork.14 baseline and deployed to both production sites. Both retained the official Codex repository as the active catalog. Version, health and asset checks passed. [Deployment ledger](../../fork-maintenance.md)

On 2026-11-19, [rc1-fork.14](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.14) was published as the latest release. Both production sites run the same image digest and passed internal and public checks. See the [fork maintenance ledger](../../fork-maintenance.md) for deployment timing and verification scope.

On 2026-11-19 the streamlined workflow published [rc1-fork.13](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.13). Both production hosts upgraded to the same pinned image and passed internal and public checks. See the [fork maintenance log](../../fork-maintenance.md) for cutover times, observed request failures and the limits of latency verification.

[rc1-fork.12](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.12) was released on 2026-11-19 and deployed to both Cloud and Quan. Both hosts passed public version, source commit and image digest checks. See the [fork maintenance log](../../fork-maintenance.md) for rollout times and verification results.

2026-11-18: [rc1-fork.11](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.11) is the latest stable release. Both production hosts upgraded from fork.9 and passed internal and public health and version checks. The streamlined workflow published Linux amd64/arm64 images and `release.json` from `0c87d028cceaa9d88fdf5e1a25dceb898544b9e2`. See the [fork maintenance log](../../fork-maintenance.md) for the pinned digest, switch times and verification scope. Earlier entries below are historical.

2026-11-17: [v1.0.0-rc1-fork.9](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.9), source `e5d3f1e84e70761a12bd4515d274fae3e345e8ce`, is the current stable release on Cloud and Quan. [Streamlined release 44063632868](https://github.com/Syh1906/axon-hub/actions/runs/44063632868) and source-matched Test, Build, Lint and Govulncheck passed. The release is stable/latest and contains only dual-architecture images and `release.json`. The translation refresh fix and browser language default are deployed; all five existing accounts now use `auto`, with permissions and update timestamps preserved. Subsequent manual language choices remain persistent.

Internal/public health, version/commit, page assets, language migration and configuration checks passed on both hosts; recreation plus health waits took 6.818/7.172 seconds. One Cloud stream forwarded to Quan failed during Quan's switch, with additional retries; the channel subsequently completed 16 executions successfully. This was not a zero-downtime deployment. The original user's production browser scenario and all translation extensions have not been retested. No database backup was created, temporary remote files were removed, and preceding fork.8 images remain. Switching back to the old image alone cannot restore previous language preferences. See the [fork ledger](../../fork-maintenance.md) and [local deployment record](../../../.agent/summary/release-rc1-fork9-20261117.md) for pinned artifacts and verification limits. The following paragraphs retain historical state and do not describe the current version.

2026-11-17: [v1.0.0-rc1-fork.8](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.8), source `100e0c3b75bfabeef92793eea1507bccf233c6a9`, is the current stable release on Cloud and Quan. [Streamlined release 44018089295](https://github.com/Syh1906/axon-hub/actions/runs/44018089295) and source-matched Test, Build, Lint and Govulncheck passed. Only dual-architecture images and release.json were published. This statistics performance release adds no schema or migration. Both deployments passed health, version, page asset, configuration and schema-preservation checks; recreation plus health waits took 6.577/7.345 seconds.

Three forwarded requests logged retries during Quan's switch; the channel subsequently completed requests successfully. A later upstream `invalid_encrypted_content` failure remains undiagnosed. The browser had no authenticated session, so production statistics page latency remains unverified; basic health is not performance acceptance. No database backup was created. Temporary configuration copies and deployment processes were removed; preceding fork.7 images remain temporarily. See the [fork ledger](../../fork-maintenance.md) and [local deployment record](../../../.agent/summary/release-rc1-fork8-20261117.md). The following paragraphs retain historical state.


2026-11-17: [v1.0.0-rc1-fork.7](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.7), source `6a2788f5ca84c8c9b15035dd169221808e71190f`, is the current stable release and runs on Cloud and Quan. [Streamlined release 43927183251](https://github.com/Syh1906/axon-hub/actions/runs/43927183251) and source-matched Test, Build, Lint and Govulncheck passed. Only dual-architecture images and release.json are published. Both statistics schema upgrades, internal/public health, page assets and configuration checks passed; recreation plus health waits took 6.690/10.264 seconds. Three Cloud requests forwarded to Quan failed during Quan's recreation window; the same route subsequently recovered. This was not a zero-downtime deployment.

Temporary configuration copies and deployment processes were removed, with no database backup created. The preceding fork.6 images remain temporarily. See the [fork ledger](../../fork-maintenance.md) and [local deployment record](../../../.agent/summary/release-rc1-fork7-20261117.md) for pinned artifacts, CI, schema and traffic evidence. The following paragraphs retain historical state and do not describe the current version.

2026-11-15: [v1.0.0-rc1-fork.6](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.6) is the current stable release and is running on both Cloud and Quan, pinned to `269412f52dd18ac31ef113f9018752c251316329`. [Streamlined release 43639889595](https://github.com/Syh1906/axon-hub/actions/runs/43639889595) and source-matched Test, Build, Lint and Govulncheck passed. `prerelease=false` and `publishBinaries=false`: only dual-architecture images and `release.json`, with no new migration or required configuration. Sequential deployment, basic checks and short natural-traffic observation passed; both applications have zero restarts, with databases and existing configuration preserved.

Recreation and health waits took 6.797/7.511 seconds. At approximately 477/264 seconds, Cloud/Quan had 37/52 completed natural requests and no failed requests, alongside canceled and in-progress work. Automatic trial expansion and unhelpful-capacity retirement were observed; sustained performance gains and complete resolution of the original slow requests are not established. Both local snapshot images and the three corresponding cloud package versions and three artifacts were [retired precisely](https://github.com/Syh1906/axon-hub/actions/runs/43641274709); stable fork.5 rollback images remain. Temporary configuration copies and deployment tools were removed. See the [fork ledger](../../fork-maintenance.md) and [local deployment record](../../../.agent/summary/release-rc1-fork6-20261115.md). The following paragraphs retain historical evidence and do not describe the current running version.

Historical temporary deployment, 2026-11-14 (retired after fork.6): connection-pool recovery source `641410e9` passed [snapshot workflow 43605316825](https://github.com/Syh1906/axon-hub/actions/runs/43605316825). After approximately 100 seconds of observation on Cloud, Quan was updated to the same pinned digest; both passed basic deployment checks. They then ran `v1.0.0-rc1-fork.5+snapshot.641410e9916d.43605316825.1`. No new Git tag or Release was created; the stable release at that time remained fork.5 as described below. See the [snapshot deployment record](../../../.agent/summary/snapshot-connection-20261114.md).

Historical stable version `v1.0.0-rc1-fork.5` (2026-11-14). Its large-request phase timeouts and lossless image optimization have been released through the streamlined workflow and deployed to Cloud and Quan. Only dual-architecture images and the manifest are published, with no new database migration or required configuration. Source checks, image startup and basic checks on both servers passed. One request forwarded from Cloud received 502 during Quan's recreation window; later requests on that path succeeded. This was not a zero-downtime deployment. See the [fork ledger](../../fork-maintenance.md) for source, digest and verification evidence.

Historical `v1.0.0-rc1-fork.4` added a rebuildable statistics table, and initial historical backfill still takes time. Its Quan switch was interrupted and subsequently recovered, with approximately six minutes and two seconds between the old container stopping and its replacement starting; that is not the result of this fork.5 deployment. The fork.2 paragraphs below likewise retain historical evidence and are not the current running version.

Historical release: [v1.0.0-rc1-fork.2](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.2) (2026-11-11), pinned to `1c0a65a093f9c356f0005e8b59e13f2c16ffd0ff`. Test, Build, Lint and Govulncheck passed for this source, followed by [streamlined release 42989955513](https://github.com/Syh1906/axon-hub/actions/runs/42989955513). It was published as stable/latest, with amd64/arm64 images and only `release.json`. The [release notes](../../../.github/release-notes.md) cover admin read optimization, failure diagnostics and the snapshot retirement validator fix. There are no backend runtime, schema/data migration or dependency changes relative to fork.1.

Image digest: `sha256:201ff56eb572b667d786825a3f240f05b2ccd730c4b1dc95e002c16e58cf217f`. Manifest SHA-256: `d1fb3fa182e875827747adeb0367b13716a5c2c9dc44846c64e8ad6fd8cf6f36`. Cloud and Quan passed basic deployment checks at 16:58:14/16:59:51 Singapore time. Recreation and health waits took 6.393/6.960 seconds, not measured per-request downtime. Both passed local/public health, version/commit, initialization, 34 homepage assets and anonymous authorization checks. Database schema digests and channel/model/project/key ID sets remained unchanged; the system version advanced to fork.2.

Only the application changed. Other containers, environment, resource limits, mounts, proxy and configuration digests were preserved, including Quan's current 8 GiB application and 2 GiB database limits. No database backup was created; temporary configuration copies and deployment tools were removed, while previous stable images remain. A short natural traffic sample showed Cloud: 6 completed, 1 processing; Quan: 4 completed, 1 canceled, 6 processing. No failures or body timeout log entries appeared in this sample; this does not prove sustained reliability. Authenticated pages, paid providers, WS end-to-end behavior and sustained load were not retested. The historical zero-body transport issue and provider compatibility failure remain unresolved.

This repository is Syh1906/axon-hub. Starting with beta12, releases merge directly from looplj/axonhub.
ChouUn/axon-hub remains a separately recorded inherited fork. GitHub Actions builds the application,
GHCR stores container images, and GitHub Releases stores binary packages and
deployment manifests. No Docker Hub account is required. Starting with .syh.8, releases default to dual-architecture images and the deployment manifest; binary archives are opt-in.

Set the explicit boolean `publishBinaries` in `release.config.json` before creating a new tag. `false` skips GoReleaser, its frontend installation, cross-platform archives, checksums and configuration-schema attachments; `true` preserves the full archive workflow. Image releases still run tests, native amd64/arm64 builds and the PostgreSQL startup check. All builds and pushes run in GitHub Actions; no local image tar is uploaded. Docker context excludes design/verification output and local demo data.

Image-only versions cannot be installed with the native binary download/upgrade scripts. Binary users must retain their existing version or wait for a new release with archives enabled. Do not rerun a published tag to change its artifact set. GitHub's automatic source archives remain available.

Independent production machines use PostgreSQL and Docker Compose, with their
own configuration and volumes. The active maintenance project manages staged
deployment. The private [axon-hub-ops](https://github.com/Syh1906/axon-hub-ops)
repository is an optional, separately authorized implementation, not a prerequisite.

The latest stable version, source commit, publishing workflow, and artifact checks
are recorded in the [release entry of the fork maintenance ledger](../../fork-maintenance.md#github-独立发布与两层上游追溯).
That entry confirms application publication; production upgrade status belongs
to the active maintenance records.

## Branches and sources

| Name | Purpose |
| --- | --- |
| origin | Your Syh1906/axon-hub repository |
| upstream | Inherited fork history and optional patches: ChouUn/axon-hub |
| original-upstream | Direct release baseline: looplj/axonhub |
| release/v1.0.x | Your maintained release branch |
| feature/* and fix/* | Local features and fixes |
| codex/sync-original-* | Temporary branch for an original-upstream release merge |

Create the synchronization branch from your maintained branch, then merge a
selected original-upstream release tag. Preserve published history; do not rebase,
force-push, or replace your branch with upstream.

Check git remote -v first. Add missing remotes only:

    git remote add upstream https://github.com/ChouUn/axon-hub.git
    git remote add original-upstream https://github.com/looplj/axonhub.git

GitHub CLI may select the parent repository by default in a fork. Pass
`--repo Syh1906/axon-hub` for your PR/release operations, or configure this checkout
with `gh repo set-default Syh1906/axon-hub`. Publishing explicitly targets this repository.

## Version identity and provenance

release.config.json records the publishing repository and branch, direct upstream
tag/commit, original upstream tag/commit, inherited fork provenance, and an explicit prerelease boolean.
The working branch baseline is v1.0.0-rc1 at d2935453 (rc1-fork.1 is not yet released). The inherited fork remains
ChouUn v1.0.0-beta10-fork.7 at b205ae8e, originally based on beta10 at 939b2bc0.

After beta12, your tags use <original-upstream-tag>-fork.N:

| Situation | Example |
| --- | --- |
| First release on beta12 | v1.0.0-beta12-fork.1 |
| Further changes on beta12 | v1.0.0-beta12-fork.2 |
| Selected patches from another fork | v1.0.0-beta12-fork.3 |
| A future beta13 release after merging it (example only) | v1.0.0-beta13-fork.1 |

Historical .syh.N releases remain readable, but new releases use fork.N. Restart at
fork.1 only after adopting a newer original-upstream baseline. Published tags are immutable.
Only -fork.N tags trigger this fork's publishing workflow; historical .syh.N tags remain readable.

Update checks and data migrations share xversion.Parse. Numeric ordering preserves:

    beta12 < beta12-fork.1 < beta12-fork.2 < beta13 < beta13-fork.1

The common v1.0.0- prefix is omitted. Do not invent a higher independent major
version that causes future upstream migrations to be skipped. New local migrations
use the complete fork.N version of their first release and must be newer than deployed
versions. Existing published migration identifiers and behavior stay unchanged.

The release.json attachment contains schemaVersion, version, repository, commit,
upstream, originalUpstream, inheritedFork, image, platforms and runURL. Deploy the immutable
multi-platform image reference in image, such as ghcr.io/syh1906/axon-hub@sha256:….

## Merging original upstream

Verify the target release exists first. The following beta13 tag is an example,
not a claim that the release already exists:

    git switch release/v1.0.x
    git fetch original-upstream tag v1.0.0-beta13
    git switch -c codex/sync-original-beta13
    git merge --no-ff --no-commit v1.0.0-beta13

Keep both parents. The beta12 merge is `50bc66fa`; compare subsequent original
release tags instead of replaying all fork changes from beta10.

1. Review every relevant topic in [Fork maintenance](../../fork-maintenance.md);
   retain local behavior while resolving conflicts.
2. Compare schema/data migrations, configuration and cache changes from both
   upstream levels. Local release numbers must not suppress new upstream migrations.
3. Update upstream and originalUpstream in release.config.json while retaining
   inheritedFork provenance. Resolve commits with git rev-parse '<tag>^{commit}'.
4. Update .github/release-notes.md with functionality, database/config changes,
   supported upgrade paths, actual verification and rollback conditions; update the ledger.
5. Run relevant checks through PR and branch CI. Publication reuses the successful
   maintenance-branch Test run for the exact source commit instead of rerunning it.
6. Push the synchronization branch, review its PR and merge it into your maintained
   branch. Create your own fork.N release tag separately.

Merging upstream does not publish or deploy. Do not mirror-push or push every
historical tag into origin.

## GitHub setup

Enable Actions for this fork and allow GITHUB_TOKEN to write repository contents
and packages. Personal plaintext tokens are not required for publishing.
After the first image push, verify its linked repository and package visibility.
Public images require no server login; private images need read-only pull access
configured on each server.

The original Helm workflow remains restricted to the original repository.
The old daily unstable image workflow is removed; scheduled model-catalog updates
are restricted to original upstream. Adopt catalog changes through normal review.

Binary packaging retains GoReleaser. The upstream Homebrew publishing section is
removed. Install/upgrade scripts and application update checks point to this fork.

Existing CI branch filters include release/**. The repository restriction on
unrequested local lint/build commands remains applicable.

## Publishing

Update the release notes, push the release commit to the maintenance branch and wait
for Test to succeed before tagging. Publication checks the latest push-triggered Test
run for that exact commit and branch; a missing, pending or failed result stops publication
without rerunning tests. The release summary links the reused run.
These commands illustrate the first release; documentation does not execute them:

    git tag -a v1.0.0-beta12-fork.1 -m "release: v1.0.0-beta12-fork.1"
    git push origin v1.0.0-beta12-fork.1

The workflow validates version/repository/branch ancestry, reuses the branch's backend,
llm, frontend and release-script test result, builds native amd64/arm64 images from the
same commit, and creates their GHCR manifest. A digest-pinned startup and version
check against temporary PostgreSQL 17 precedes binary packaging. This is not a production-data migration rehearsal.

When `publishBinaries=true`, GoReleaser uploads binaries, checksums and the configuration schema to a draft. Otherwise the workflow creates a draft directly after image verification.
The final step attaches release.json and the reviewed notes, then publishes with the complete version as its title and the configured
prerelease state. There is no version-file commit back to the default branch.
Set prerelease: false for an official release; it becomes the latest GitHub release.
Explicitly use true for a test release, which must not replace the latest official release.
Upstream beta/rc provenance does not determine your release level. Update checks follow
GitHub metadata; enabling pre-releases opts into test releases. The application-side
recognition fix takes effect in the next binary release. Changing release metadata never
replaces existing images or tags. Production continues to deploy immutable digests.

Failures leave the release unpublished. Inspect logs before retrying failed jobs;
inspect existing draft assets before rerunning binary publication. Published
versions must not be overwritten; issue a new fork.N number. Partial image tags do
not establish a successful release: the complete published manifest does.

## On-demand snapshot images

Run `.github/workflows/snapshot.yml` manually from the maintenance branch, supplying
a full 40-character `commit` and an already published official `base_version`, such
as `v1.0.0-beta10-fork.7.syh.10`. This creates neither Git tags nor Releases, consumes
no new release number, and does not deploy production. The workflow must exist on
the GitHub default branch, currently `release/v1.0.x`.

Preparation verifies repository, maintenance-branch ancestry, both upstream sources,
and the base tag. It rejects an older base when a later numbered tag is already in
the source history, and rejects changes under `internal/ent/schema/` or
`internal/ent/migrate/` since the base. Review configuration, persistent writes and
rollback compatibility separately; incompatible changes require a dedicated upgrade
plan rather than this ordinary snapshot path.

The minimum release number applies to the selected base, not to older tags already
in its history. Historical tags are still checked for a version newer than the base.

Tests and both native architecture builds use the exact source commit. After startup,
version and revision-label checks against temporary PostgreSQL, the workflow uploads
`snapshot.json`. The application version is
`<base>+snapshot.<12-character-commit>.<run-id>.<attempt>`; the image tag is
`snapshot-<12-character-commit>-<run-id>-<attempt>`. Build metadata does not advance
version precedence. Retries have distinct identities; official image tags are never
replaced. Partial images without a successful manifest are not deployable evidence.
After a failure, rerun the entire workflow to prepare a new identity. Rerunning only
failed jobs is rejected because it would reuse the previous snapshot identity.

The manifest records `kind: snapshot`, `snapshotId`, base version and commit, source
commit, application version, both upstream sources, immutable image digest, all three
image tags, platforms and workflow attempt URL. Verify the successful run and retain
the manifest in maintenance records before Actions artifacts expire. Deploy the same
digest serially, retaining separate host receipts and the existing no-database-backup
and application-only recreation rules. Startup checks do not verify real providers
or billing.

Snapshots are temporary, not long-term archives. Retirement is initiated manually by
the user; do not add schedules, post-release detection or automatic deletion hooks.
A published Release alone is insufficient: verify that target hosts have switched
to and accepted the replacement official image or snapshot and that no runtime or rollback references remain.
List precise local images, GHCR multi-platform and architecture child images, and
Actions artifacts before deleting within the applicable authorization. Check all
container references and shared digests; do not delete official images or sweep by
prefix or age. Keep referenced or uncertain snapshots with a reason. Retain non-secret
source, digest, run, deployment and cleanup receipts, including outstanding items.

When a formal release or new snapshot replaces an old snapshot, manually run `retire-snapshot.yml` with
`old_run` and the already accepted `replacement_run`. A formal replacement requires a successful release
workflow and a published non-prerelease version; its `release.json` version, commit, platforms and run URL
must match the build. It defaults to a precise preview;
set `execute=true` only within the deletion authorization after checking every target
host and rollback reference. The workflow does not connect to servers or remove local
images. It uses the publishing repository's existing `GITHUB_TOKEN`, not expanded
personal-token scopes. It deletes only package versions for the old manifest's three
exact tags and that run's manifest and `.dockerbuild` artifacts. Shared tags or a
replacement digest block deletion. Preserve workflow history and retirement receipts;
record completed operations and stop on partial failure without automatic retries.
The replacement release validator accepts current `-fork.N` and historical `.syh.N` versions.

Snapshots use the same version format and provenance rules as releases: the base tag must include both the original upstream and inherited fork. A commit containing a later fork.N tag cannot produce a snapshot from an older base. Historical ChouUn configurations retain their .syh.N snapshot validation.

## Independent production machines

Use the active maintenance project for inventory, dedicated SSH credentials and
verified host keys. A separate private ops repository is optional, not a prerequisite
for official or snapshot deployment. Never commit production credentials to the
application repository. Accept the first host before updating the others with the
same manifest and digest; retain individual host receipts. When using the private
ops implementation, select production with the successful canary run number and
verify the same version, digest, ops commit and inventory.

Each host pulls the immutable image, checks migrations and rollback compatibility,
and temporarily saves configuration while the old application remains online. Change
only AXONHUB_IMAGE and recreate the application after preparation. Axon maintenance
updates do not create database backups, including full or selected-table exports.
Preserve database images and volumes. After deployment verification, delete this
upgrade's temporary configuration copies and retain only non-secret version, hash
and verification receipts. Historical copies require separate deletion authorization.

Migration or verification failure stops further hosts and preserves the current state.
Do not restore the database automatically or treat switching to an older image as a
complete rollback. Record the actual scope of business, billing and network checks;
this procedure establishes no database recovery point. The active maintenance
repository and host records define paths, execution state and rollback limitations.
Any private ops automation that still requires a database export is outside this
procedure; these documentation changes do not establish that it was modified.
