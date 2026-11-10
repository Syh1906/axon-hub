import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

function validateManifest(manifest) {
  const id = manifest.snapshotId;
  if (manifest.kind !== 'snapshot' || !/^snapshot-[a-f0-9]{12}-[1-9]\d*-[1-9]\d*$/.test(id)) {
    throw new Error('只允许退役有明确身份的快照');
  }
  if (manifest.repository !== 'Syh1906/axon-hub' ||
    !/^ghcr\.io\/syh1906\/axon-hub@sha256:[a-f0-9]{64}$/.test(manifest.image)) {
    throw new Error('快照来源仓库或镜像无效');
  }
  const expected = [id, `${id}-amd64`, `${id}-arm64`];
  if (JSON.stringify(manifest.imageTags) !== JSON.stringify(expected)) throw new Error('快照镜像标签不完整');
}

function validateReplacement(manifest) {
  if (manifest.kind === 'snapshot') return validateManifest(manifest);
  if (manifest.schemaVersion !== 1 || manifest.repository !== 'Syh1906/axon-hub' ||
    !/^v\d+\.\d+\.\d+-[^/]+(?:-fork\.|\.syh\.)[1-9]\d*$/.test(manifest.version) ||
    !/^[a-f0-9]{40}$/.test(manifest.commit) ||
    !/^ghcr\.io\/syh1906\/axon-hub@sha256:[a-f0-9]{64}$/.test(manifest.image) ||
    JSON.stringify(manifest.platforms) !== JSON.stringify(['linux/amd64', 'linux/arm64'])) {
    throw new Error('替代正式发布清单无效');
  }
}

export function planRetirement(previous, replacement, versions, artifacts) {
  validateManifest(previous);
  validateReplacement(replacement);
  if (previous.snapshotId === replacement.snapshotId || previous.image === replacement.image) {
    throw new Error('替代产物必须使用不同身份与镜像摘要');
  }
  const tags = new Set(previous.imageTags);
  const candidates = versions.filter((item) => item.metadata.container.tags.some((tag) => tags.has(tag)));
  const found = new Set(candidates.flatMap((item) => item.metadata.container.tags));
  if (previous.imageTags.some((tag) => !found.has(tag))) throw new Error('旧快照镜像标签缺失，需先核对现场');
  if (!candidates.some((item) => previous.image.endsWith(`@${item.name}`))) {
    throw new Error('旧快照主摘要与包版本不一致');
  }
  for (const item of candidates) {
    if (replacement.image.endsWith(`@${item.name}`) || item.metadata.container.tags.some((tag) => !tags.has(tag))) {
      throw new Error('旧镜像与其他标签共享版本，拒绝删除');
    }
  }
  const selectedArtifacts = artifacts.filter((item) => item.name === previous.snapshotId || item.name.endsWith('.dockerbuild'));
  if (!selectedArtifacts.some((item) => item.name === previous.snapshotId)) throw new Error('旧快照清单附件缺失');
  return {
    snapshotId: previous.snapshotId, oldImage: previous.image, oldCommit: previous.commit,
    replacementSnapshotId: replacement.snapshotId ?? null, replacementVersion: replacement.version,
    replacementImage: replacement.image, replacementCommit: replacement.commit,
    packageVersions: candidates.map((item) => ({ id: item.id, digest: item.name, tags: item.metadata.container.tags })),
    artifacts: selectedArtifacts.map((item) => ({ id: item.id, name: item.name, bytes: item.size_in_bytes })),
  };
}

function gh(...args) {
  return execFileSync('gh', args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const [oldRun, replacementRun, directory, execute] = process.argv.slice(2);
  if (![oldRun, replacementRun].every((id) => /^[1-9]\d*$/.test(id)) || oldRun === replacementRun) {
    throw new Error('必须指定不同的旧快照与替代构建运行编号');
  }
  const repository = 'Syh1906/axon-hub';
  if (process.env.GITHUB_REPOSITORY !== repository) throw new Error('退役入口只能在本仓库运行');
  mkdirSync(directory, { recursive: true });
  const fetchManifest = (runID, subdirectory) => {
    const run = JSON.parse(gh('api', `repos/${repository}/actions/runs/${runID}`));
    if (run.path !== '.github/workflows/snapshot.yml' || run.conclusion !== 'success') {
      throw new Error('只能使用成功的快照构建任务');
    }
    const artifacts = JSON.parse(gh('api', `repos/${repository}/actions/runs/${runID}/artifacts?per_page=100`)).artifacts;
    const manifests = artifacts.filter((item) => /^snapshot-[a-f0-9]{12}-\d+-\d+$/.test(item.name) && !item.expired);
    if (manifests.length !== 1) throw new Error('快照任务必须具有唯一可下载清单');
    gh('run', 'download', runID, '--repo', repository, '--name', manifests[0].name, '--dir', subdirectory);
    const manifest = JSON.parse(readFileSync(join(subdirectory, 'snapshot.json'), 'utf8'));
    validateManifest(manifest);
    if (manifest.snapshotId !== manifests[0].name ||
      !manifest.runURL.startsWith(`https://github.com/${repository}/actions/runs/${runID}/attempts/`)) {
      throw new Error('清单与构建任务身份不一致');
    }
    return { manifest, artifacts };
  };
  const previous = fetchManifest(oldRun, join(directory, 'old'));
  const replacementDirectory = join(directory, 'replacement');
  const replacementBuild = JSON.parse(gh('api', `repos/${repository}/actions/runs/${replacementRun}`));
  let replacement;
  if (replacementBuild.path === '.github/workflows/release.yml' && replacementBuild.conclusion === 'success') {
    const release = JSON.parse(gh('api', `repos/${repository}/releases/tags/${replacementBuild.head_branch}`));
    if (release.draft || release.prerelease) throw new Error('替代正式版本必须已公开发布');
    gh('release', 'download', release.tag_name, '--repo', repository, '--pattern', 'release.json', '--dir', replacementDirectory);
    const manifest = JSON.parse(readFileSync(join(replacementDirectory, 'release.json'), 'utf8'));
    validateReplacement(manifest);
    if (manifest.version !== release.tag_name || manifest.commit !== replacementBuild.head_sha ||
      manifest.runURL !== `https://github.com/${repository}/actions/runs/${replacementRun}`) {
      throw new Error('正式发布清单与构建任务身份不一致');
    }
    replacement = { manifest };
  } else {
    replacement = fetchManifest(replacementRun, replacementDirectory);
  }
  const endpoint = 'users/Syh1906/packages/container/axon-hub/versions';
  const versions = JSON.parse(gh('api', '--paginate', '--slurp', `${endpoint}?per_page=100`)).flat();
  const receipt = {
    ...planRetirement(previous.manifest, replacement.manifest, versions, previous.artifacts),
    oldRun, replacementRun, status: 'planned', createdAt: new Date().toISOString(),
    deletedPackageVersions: [], deletedArtifacts: [],
  };
  const receiptPath = join(directory, 'retirement.json');
  const save = () => writeFileSync(receiptPath, `${JSON.stringify(receipt, null, 2)}\n`);
  save();
  console.log(JSON.stringify(receipt));
  if (execute === '--execute') {
    try {
      for (const version of receipt.packageVersions) {
        gh('api', '--method', 'DELETE', `${endpoint}/${version.id}`);
        receipt.deletedPackageVersions.push(version.id);
        save();
      }
      for (const artifact of receipt.artifacts) {
        gh('api', '--method', 'DELETE', `repos/${repository}/actions/artifacts/${artifact.id}`);
        receipt.deletedArtifacts.push(artifact.id);
        save();
      }
      const remainingVersions = JSON.parse(gh('api', '--paginate', '--slurp', `${endpoint}?per_page=100`)).flat();
      const remainingArtifacts = JSON.parse(gh('api', `repos/${repository}/actions/runs/${oldRun}/artifacts?per_page=100`)).artifacts;
      if (remainingVersions.some((item) => receipt.deletedPackageVersions.includes(item.id)) ||
        remainingArtifacts.some((item) => receipt.deletedArtifacts.includes(item.id))) {
        throw new Error('删除后仍存在指定目标，保留收据并核对现场');
      }
      receipt.status = 'retired';
      receipt.completedAt = new Date().toISOString();
      save();
    } catch (error) {
      receipt.status = 'incomplete';
      save();
      throw error;
    }
  }
}
