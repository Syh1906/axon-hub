import { execFileSync } from 'node:child_process';
import { appendFileSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createManifest, validateTag } from './release.mjs';

export function prepareSnapshot(root, repository, commit, baseVersion, runID, runAttempt) {
  const config = JSON.parse(readFileSync(resolve(root, 'release.config.json'), 'utf8'));
  if (repository !== config.repository) throw new Error('快照仓库与 release.config.json 不一致');
  if (!/^[a-f0-9]{40}$/.test(commit)) throw new Error('快照必须指定完整的 40 位源码提交号');
  for (const value of [runID, runAttempt]) {
    if (!/^[1-9]\d*$/.test(value)) throw new Error('构建运行编号及重跑次数必须为正整数');
  }
  const baseNumber = validateTag(baseVersion, config);
  const git = (...args) => execFileSync('git', args, {
    cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
  const baseCommit = git('rev-parse', `${baseVersion}^{commit}`);
  git('merge-base', '--is-ancestor', baseCommit, commit);
  git('merge-base', '--is-ancestor', commit, `origin/${config.branch}`);
  git('merge-base', '--is-ancestor', config.upstreamCommit, baseCommit);
  git('merge-base', '--is-ancestor', config.originalUpstream.commit, baseCommit);
  if (config.inheritedFork) git('merge-base', '--is-ancestor', config.inheritedFork.commit, baseCommit);

  // 已包含更晚正式编号的源码不能再标为旧基线的快照。
  const prefix = config.upstreamRepository === 'ChouUn/axon-hub' ? `${config.upstreamTag}.syh.` : `${config.upstreamTag}-fork.`;
  // 历史编号可以早于当前发布下限；下限已在上面对所选基线检查。
  const historyConfig = { ...config, minimumReleaseNumber: 1 };
  for (const tag of git('tag', '--merged', commit, '--list', `${prefix}*`).split('\n').filter(Boolean)) {
    if (validateTag(tag, historyConfig) > baseNumber) throw new Error(`源码已包含 ${tag}，请使用对应的新基线`);
  }
  const migrationChanges = git('diff', '--name-only', baseCommit, commit, '--',
    'internal/ent/schema', 'internal/ent/migrate');
  if (migrationChanges) throw new Error(`普通快照不支持 schema 或迁移代码变化：\n${migrationChanges}`);

  const shortCommit = commit.slice(0, 12);
  const imageTag = `snapshot-${shortCommit}-${runID}-${runAttempt}`;
  return {
    config, commit, baseVersion, baseCommit, imageTag, runAttempt,
    version: `${baseVersion}+snapshot.${shortCommit}.${runID}.${runAttempt}`,
    image: `ghcr.io/${repository.toLowerCase()}`,
    runURL: `https://github.com/${repository}/actions/runs/${runID}/attempts/${runAttempt}`,
  };
}

export function createSnapshotManifest(prepared, digest) {
  return {
    ...createManifest(prepared, digest, prepared.runURL),
    kind: 'snapshot', snapshotId: prepared.imageTag,
    baseVersion: prepared.baseVersion, baseCommit: prepared.baseCommit,
    imageTags: [prepared.imageTag, `${prepared.imageTag}-amd64`, `${prepared.imageTag}-arm64`],
  };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  if (process.argv[2] === 'prepare') {
    const prepared = prepareSnapshot(process.cwd(), process.env.GITHUB_REPOSITORY,
      process.env.SNAPSHOT_COMMIT, process.env.BASE_VERSION,
      process.env.GITHUB_RUN_ID, process.env.GITHUB_RUN_ATTEMPT);
    appendFileSync(process.env.GITHUB_OUTPUT,
      `prepared=${JSON.stringify(prepared)}\nversion=${prepared.version}\ncommit=${prepared.commit}\nimage=${prepared.image}\nimage_tag=${prepared.imageTag}\nrun_attempt=${prepared.runAttempt}\n`);
  } else if (process.argv[2] === 'manifest') {
    const manifest = createSnapshotManifest(JSON.parse(process.env.SNAPSHOT_PREPARED), process.env.IMAGE_DIGEST);
    writeFileSync(process.argv[3], `${JSON.stringify(manifest, null, 2)}\n`);
  } else {
    throw new Error('用法：node scripts/release/snapshot.mjs prepare | manifest <输出文件>');
  }
}
