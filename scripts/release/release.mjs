import { execFileSync } from 'node:child_process';
import { appendFileSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export function validateTag(tag, config) {
  const prefix = `${config.upstreamTag}.syh.`;
  const suffix = tag.startsWith(prefix) ? tag.slice(prefix.length) : '';
  if (!/^[1-9]\d*$/.test(suffix) || !Number.isSafeInteger(Number(suffix))) {
    throw new Error(`版本必须采用 ${prefix}N，N 为正整数`);
  }
  if (Number(suffix) < config.minimumReleaseNumber) {
    throw new Error(`发布编号必须不小于 ${config.minimumReleaseNumber}，必须使用独立的 syh 发布编号`);
  }
  return Number(suffix);
}

export function prepareRelease(root, repository, tag) {
  const config = JSON.parse(readFileSync(resolve(root, 'release.config.json'), 'utf8'));
  if (repository !== config.repository) throw new Error('发布仓库与 release.config.json 不一致');
  const number = validateTag(tag, config);
  const git = (...args) => execFileSync('git', args, { cwd: root, encoding: 'utf8' }).trim();
  const commit = git('rev-parse', 'HEAD');
  if (git('rev-parse', `${tag}^{commit}`) !== commit) throw new Error('当前提交与发布 tag 不一致');
  git('merge-base', '--is-ancestor', commit, `origin/${config.branch}`);
  git('merge-base', '--is-ancestor', config.upstreamCommit, commit);
  git('merge-base', '--is-ancestor', config.originalUpstream.commit, config.upstreamCommit);
  const tags = git('tag', '--list', `${config.upstreamTag}.syh.*`).split('\n').filter(Boolean);
  for (const previous of tags) {
    if (previous === tag) continue;
    const suffix = previous.slice(`${config.upstreamTag}.syh.`.length);
    if (/^[1-9]\d*$/.test(suffix) && Number(suffix) >= number) {
      throw new Error(`已有 ${previous}；新发布编号必须继续递增`);
    }
  }
  const notes = readFileSync(resolve(root, config.notesFile), 'utf8');
  if (!notes.trim() || /\bTODO\b/.test(notes)) throw new Error('请完成本次发布说明');
  return { config, version: tag, commit, image: `ghcr.io/${repository.toLowerCase()}` };
}

export function createManifest(prepared, digest, runURL) {
  if (!/^sha256:[a-f0-9]{64}$/.test(digest)) throw new Error('镜像摘要格式无效');
  return {
    schemaVersion: 1, version: prepared.version,
    repository: prepared.config.repository, commit: prepared.commit,
    upstream: { repository: prepared.config.upstreamRepository,
      tag: prepared.config.upstreamTag, commit: prepared.config.upstreamCommit },
    image: `${prepared.image}@${digest}`,
    originalUpstream: prepared.config.originalUpstream,
    platforms: ['linux/amd64', 'linux/arm64'], runURL,
  };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const prepared = prepareRelease(process.cwd(), process.env.GITHUB_REPOSITORY, process.env.GITHUB_REF_NAME);
  if (process.argv[2] === 'prepare') {
    appendFileSync(process.env.GITHUB_OUTPUT, `version=${prepared.version}\ncommit=${prepared.commit}\nimage=${prepared.image}\n`);
  } else if (process.argv[2] === 'manifest') {
    const manifest = createManifest(prepared, process.env.IMAGE_DIGEST,
      `https://github.com/${prepared.config.repository}/actions/runs/${process.env.GITHUB_RUN_ID}`);
    writeFileSync(process.argv[3], `${JSON.stringify(manifest, null, 2)}\n`);
  } else {
    throw new Error('用法：node scripts/release/release.mjs prepare | manifest <输出文件>');
  }
}
