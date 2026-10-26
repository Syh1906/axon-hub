# Independent releases and upstream maintenance

This repository is Syh1906/axon-hub. Its direct upstream is ChouUn/axon-hub;
the original upstream is looplj/axonhub. GitHub Actions builds the application,
GHCR stores container images, and GitHub Releases stores binary packages and
deployment manifests. No Docker Hub account is required.

Independent production machines use PostgreSQL and Docker Compose, with their
own configuration and volumes. The private
[axon-hub-ops](https://github.com/Syh1906/axon-hub-ops) repository manages staged
deployment. Access to that repository requires authorization.

## Branches and sources

| Name | Purpose |
| --- | --- |
| origin | Your Syh1906/axon-hub repository |
| upstream | Direct upstream: ChouUn/axon-hub |
| original-upstream | Original upstream: looplj/axonhub |
| release/v1.0.x | Your maintained release branch |
| feature/* and fix/* | Local features and fixes |
| sync/chouun-* | Temporary branch for an upstream release merge |

Create the synchronization branch from your maintained branch, then merge a
selected direct-upstream release tag. Preserve published history; do not rebase,
force-push, or replace your branch with upstream.

Check git remote -v first. Add missing remotes only:

    git remote add upstream https://github.com/ChouUn/axon-hub.git
    git remote add original-upstream https://github.com/looplj/axonhub.git

GitHub CLI may select the parent repository by default in a fork. Pass
`--repo Syh1906/axon-hub` for your PR/release operations, or configure this checkout
with `gh repo set-default Syh1906/axon-hub`. Publishing explicitly targets this repository.

## Version identity and provenance

release.config.json records the publishing repository and branch, direct upstream
tag/commit, and original upstream tag/commit. The current direct baseline is
v1.0.0-beta10-fork.7 at b205ae8e; its original baseline is v1.0.0-beta10 at 939b2bc0.

Your tags use <direct-upstream-tag>.syh.N:

| Situation | Example |
| --- | --- |
| First independent release from this checkout | v1.0.0-beta10-fork.7.syh.1 |
| Another local release on the same baseline | v1.0.0-beta10-fork.7.syh.2 |
| After merging ChouUn fork.8 | v1.0.0-beta10-fork.8.syh.1 |
| After a direct-upstream release based on beta11 | v1.0.0-beta11-fork.1.syh.1 |

The last two rows are examples, not assertions that those releases exist.
Never consume ChouUn's fork.N sequence. Increment syh.N within one baseline;
restart at syh.1 after adopting a newer direct baseline. Published tags are immutable.
Only .syh.N tags trigger this fork's publishing workflow.

Update checks and data migrations share xversion.Parse. Numeric ordering preserves:

    beta10-fork.7 < beta10-fork.7.syh.9 < beta10-fork.7.syh.10
    < beta10-fork.8 < beta10-fork.8.syh.1 < beta11-fork.1.syh.1

The common v1.0.0- prefix is omitted. Do not invent a higher independent major
version that causes future upstream migrations to be skipped. New local migrations
use the complete syh version of their first release and must be newer than deployed
versions. Existing published migration identifiers and behavior stay unchanged.

The release.json attachment contains schemaVersion, version, repository, commit,
upstream, originalUpstream, image, platforms and runURL. Deploy the immutable
multi-platform image reference in image, such as ghcr.io/syh1906/axon-hub@sha256:….

## Merging direct upstream

For an illustrative v1.0.0-beta10-fork.8 target, first verify it actually exists:

    git switch release/v1.0.x
    git pull --ff-only origin release/v1.0.x
    git fetch upstream
    git fetch original-upstream
    git switch -c sync/chouun-v1.0.0-beta10-fork.8
    git merge --no-ff v1.0.0-beta10-fork.8

1. Review every relevant topic in [Fork maintenance](../../fork-maintenance.md);
   retain local behavior while resolving conflicts.
2. Compare schema/data migrations, configuration and cache changes from both
   upstream levels. Local release numbers must not suppress new upstream migrations.
3. Update the direct baseline in release.config.json; update originalUpstream only
   when that baseline actually changes. Resolve commits with git rev-parse '<tag>^{commit}'.
4. Update .github/release-notes.md with functionality, database/config changes,
   supported upgrade paths, actual verification and rollback conditions; update the ledger.
5. Run relevant checks. Existing PR CI covers changes; release tests run again on
   the exact publishing tag.
6. Push the synchronization branch, review its PR and merge it into your maintained
   branch. Create your own syh release tag separately.

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

Update the release notes and ensure the maintained commit passes its checks.
These commands illustrate the first release; documentation does not execute them:

    git tag -a v1.0.0-beta10-fork.7.syh.1 -m "release: v1.0.0-beta10-fork.7.syh.1"
    git push origin v1.0.0-beta10-fork.7.syh.1

The workflow validates version/repository/branch ancestry, calls existing backend,
llm, frontend and release-script tests, builds native amd64/arm64 images from the
same commit, and creates their GHCR manifest. A digest-pinned startup and version
check against temporary PostgreSQL 17 precedes binary packaging. This is not a production-data migration rehearsal.

GoReleaser uploads binaries, checksums and the configuration schema to a draft.
The final step attaches release.json and the reviewed notes, then publishes as a
prerelease. There is no version-file commit back to the default branch.
Enable Beta updates in the application to see releases retaining a beta baseline.
Production never follows a mutable latest tag.

Failures leave the release unpublished. Inspect logs before retrying failed jobs;
inspect existing draft assets before rerunning binary publication. Published
versions must not be overwritten; issue a new syh number. Partial image tags do
not establish a successful release: the complete published manifest does.

## Independent production machines

Configure actual inventory, dedicated SSH credentials and verified host keys in
the private ops repository. Never commit production credentials to the application
repository. First deploy canary, perform business acceptance, then select
production with that successful run number. The workflow verifies the same version,
image digest, ops commit and inventory before updating hosts serially.

Each host pre-pulls the application image, preserves configuration, stops the app,
backs up PostgreSQL and updates only AXONHUB_IMAGE and the application service.
Database images and data volumes are preserved. An unchanged host is verified
without repeating maintenance. Backup/verification failure stops further hosts.
There is no unconditional database rollback.

The private ops README documents secrets, paths, backup states and recovery.
Actual business behavior, backup restoration and network access remain acceptance
requirements when real hosts are connected.
