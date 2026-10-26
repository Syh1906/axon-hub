import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { createManifest, prepareRelease, validateTag } from './release.mjs';

const config = { repository: 'Syh1906/axon-hub', branch: 'release/v1.0.x',
  prerelease: false,
  upstreamTag: 'v1.0.0-beta10-fork.7', upstreamRepository: 'ChouUn/axon-hub',
  minimumReleaseNumber: 1, notesFile: 'notes.md',
  originalUpstream: { repository: 'looplj/axonhub', tag: 'v1.0.0-beta10', commit: 'b'.repeat(40) } };

test('发布使用独立 syh 编号，拒绝上游 tag、错误基线和非整数', () => {
  assert.equal(validateTag('v1.0.0-beta10-fork.7.syh.1', config), 1);
  for (const tag of ['v1.0.0-beta10', 'v1.0.0-beta10-fork.7', 'v1.0.0-beta10-fork.08',
    'v1.0.0-beta11-fork.8', 'v1.0.0-beta10-fork.8-rc.1', 'v1.0.0-beta10-fork.9007199254740992']) {
    assert.throws(() => validateTag(tag, config));
  }
  assert.throws(() => validateTag('v1.0.0-beta10-fork.8.syh.1', config));
  assert.throws(() => validateTag('v1.0.0-beta10-fork.7.syh.01', config));
});

test('清单固定源码、上游基线和多架构镜像摘要', () => {
  const prepared = { config: { ...config, upstreamCommit: 'b'.repeat(40) },
    version: 'v1.0.0-beta10-fork.7.syh.1', commit: 'a'.repeat(40), image: 'ghcr.io/syh1906/axon-hub' };
  const manifest = createManifest(prepared, `sha256:${'c'.repeat(64)}`, 'https://github.com/run');
  assert.equal(manifest.image, `ghcr.io/syh1906/axon-hub@sha256:${'c'.repeat(64)}`);
  assert.equal(manifest.commit, 'a'.repeat(40));
  assert.equal(manifest.upstream.commit, 'b'.repeat(40));
  assert.deepEqual(manifest.originalUpstream, config.originalUpstream);
  assert.deepEqual(manifest.platforms, ['linux/amd64', 'linux/arm64']);
  assert.throws(() => createManifest(prepared, 'latest', ''));
});

test('真实 Git 历史拒绝分支外发布、错误 tag 及倒退编号', () => {
  assert.ok(process.env.RELEASE_TEST_ROOT, '测试夹具必须写入显式指定的临时目录');
  mkdirSync(process.env.RELEASE_TEST_ROOT, { recursive: true });
  const root = mkdtempSync(join(process.env.RELEASE_TEST_ROOT, 'git-'));
  const git = (...args) => execFileSync('git', args, { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
  git('init', '-b', config.branch);
  writeFileSync(join(root, 'notes.md'), '本次升级说明');
  git('add', '.');
  git('-c', 'user.name=发布测试', '-c', 'user.email=test@example.invalid', 'commit', '-m', '初始基线');
  const upstreamCommit = git('rev-parse', 'HEAD');
  writeFileSync(join(root, 'release.config.json'), JSON.stringify({ ...config, upstreamCommit,
    originalUpstream: { ...config.originalUpstream, commit: upstreamCommit } }));
  git('add', '.');
  git('-c', 'user.name=发布测试', '-c', 'user.email=test@example.invalid', 'commit', '-m', '发布配置');
  git('update-ref', `refs/remotes/origin/${config.branch}`, 'HEAD');
  const tag = 'v1.0.0-beta10-fork.7.syh.1';
  git('tag', tag);
  assert.equal(prepareRelease(root, config.repository, tag).commit, git('rev-parse', 'HEAD'));
  for (const prerelease of [false, true]) {
    writeFileSync(join(root, 'release.config.json'), JSON.stringify({ ...config, upstreamCommit,
      prerelease, originalUpstream: { ...config.originalUpstream, commit: upstreamCommit } }));
    const output = join(root, `output-${prerelease}.txt`);
    execFileSync(process.execPath, [fileURLToPath(new URL('./release.mjs', import.meta.url)), 'prepare'], {
      cwd: root, env: { ...process.env, GITHUB_REPOSITORY: config.repository,
        GITHUB_REF_NAME: tag, GITHUB_OUTPUT: output },
    });
    assert.match(readFileSync(output, 'utf8'), new RegExp(`^prerelease=${prerelease}$`, 'm'));
  }
  assert.throws(() => prepareRelease(root, 'other/repo', tag));
  git('tag', 'v1.0.0-beta10-fork.7.syh.2');
  assert.throws(() => prepareRelease(root, config.repository, tag), /继续递增/);
  git('update-ref', `refs/remotes/origin/${config.branch}`, upstreamCommit);
  assert.throws(() => prepareRelease(root, config.repository, tag));
});
