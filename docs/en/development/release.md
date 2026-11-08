# Independent releases and upstream maintenance

The current stable release is [`v1.0.0-beta12-fork.2`](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-beta12-fork.2), published on 2026-11-08 from `5fb33840`. Both production hosts are updated, with authenticated request diagnostics, trace details, and database-transfer readiness rechecked.

The current candidate is `v1.0.0-beta12-fork.3`, covering statistics reuse, average output speed over total execution time, and client-cancellation continuations. It keeps `prerelease=false` and `publishBinaries=false` for the image-only workflow. Two fields, a request lookup index and an identity backfill migration are added; see the [release notes](../../../.github/release-notes.md). Publishing and host deployment results are recorded only after they complete.

The request-validation, deep-trace parsing, and retention-marker defects found after fork.1 are fixed. Acceptance must distinguish health, authenticated queries, nonempty diagnostics/details, and actual request outcomes. Database transfer uses a dedicated writable mount while keeping the container root read-only; both hosts now have that mount, with no backup or import performed. Short-lived payload caching and existing disk policies are preserved. Deferred cache evidence, real provider billing, and long-term load remain explicit limitations.

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
The current baseline is v1.0.0-beta12 at 2b1a0f93. The inherited fork remains
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
