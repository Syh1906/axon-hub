import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { createSnapshotManifest, prepareSnapshot } from './snapshot.mjs';

const repository = 'Syh1906/axon-hub';
const branch = 'release/v1.0.x';
const baseVersion = 'v1.0.0-beta12-fork.1';

function fixture() {
  assert.ok(process.env.RELEASE_TEST_ROOT, '测试夹具必须写入显式指定的临时目录');
  mkdirSync(process.env.RELEASE_TEST_ROOT, { recursive: true });
  const root = mkdtempSync(join(process.env.RELEASE_TEST_ROOT, 'snapshot-'));
  const git = (...args) => execFileSync('git', args, {
    cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
  const commitFile = (path, text) => {
    mkdirSync(dirname(join(root, path)), { recursive: true });
    writeFileSync(join(root, path), text);
    git('add', '.');
    git('-c', 'user.name=快照测试', '-c', 'user.email=test@example.invalid', 'commit', '-m', path);
    return git('rev-parse', 'HEAD');
  };
  git('init', '-b', branch);
  commitFile('README.md', '共同历史');
  git('branch', 'inherited');
  const upstreamCommit = commitFile('upstream.txt', '原始 beta12');
  git('checkout', 'inherited');
  const inheritedCommit = commitFile('fork.txt', '继承 fork 功能');
  git('checkout', branch);
  git('-c', 'user.name=快照测试', '-c', 'user.email=test@example.invalid', 'merge', '--no-ff', 'inherited', '-m', '合并两条来源');
  const config = {
    repository, branch, minimumReleaseNumber: 1,
    upstreamTag: 'v1.0.0-beta12', upstreamRepository: 'looplj/axonhub', upstreamCommit,
    originalUpstream: { repository: 'looplj/axonhub', tag: 'v1.0.0-beta12', commit: upstreamCommit },
    inheritedFork: { repository: 'ChouUn/axon-hub', tag: 'v1.0.0-beta10-fork.7', commit: inheritedCommit },
  };
  const baseCommit = commitFile('release.config.json', JSON.stringify(config));
  git('tag', baseVersion);
  const commit = commitFile('source.txt', '提前使用的功能');
  git('update-ref', `refs/remotes/origin/${branch}`, commit);
  const prepare = (changes = {}) => {
    const args = { repository, commit, baseVersion, runID: '123', runAttempt: '1', ...changes };
    return prepareSnapshot(root, args.repository, args.commit, args.baseVersion, args.runID, args.runAttempt);
  };
  return { root, git, commitFile, baseCommit, commit, prepare };
}

test('快照固定源码与基线，程序版本和镜像标签分别表示构建身份', () => {
  const f = fixture();
  const prepared = f.prepare();
  const suffix = `${f.commit.slice(0, 12)}-123-1`;
  assert.equal(prepared.commit, f.commit);
  assert.equal(prepared.baseCommit, f.baseCommit);
  assert.equal(prepared.version, `${baseVersion}+snapshot.${f.commit.slice(0, 12)}.123.1`);
  assert.equal(prepared.imageTag, `snapshot-${suffix}`);
  assert.match(prepared.imageTag, /^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$/);
  assert.notEqual(f.prepare({ runAttempt: '2' }).imageTag, prepared.imageTag);
  assert.deepEqual(f.git('tag', '--list').split('\n'), [baseVersion]);
  assert.equal(f.git('status', '--porcelain'), '');
  const manifest = createSnapshotManifest(prepared, `sha256:${'a'.repeat(64)}`);
  assert.equal(manifest.kind, 'snapshot');
  assert.deepEqual(manifest.inheritedFork, prepared.config.inheritedFork);
  assert.deepEqual(manifest.originalUpstream, prepared.config.originalUpstream);
  assert.equal(manifest.image, `ghcr.io/syh1906/axon-hub@sha256:${'a'.repeat(64)}`);
  assert.equal(manifest.baseVersion, baseVersion);
  assert.equal(manifest.baseCommit, f.baseCommit);
  assert.equal(manifest.commit, f.commit);
  assert.deepEqual(manifest.imageTags, [prepared.imageTag, `${prepared.imageTag}-amd64`, `${prepared.imageTag}-arm64`]);
  assert.deepEqual(manifest.platforms, ['linux/amd64', 'linux/arm64']);
  assert.equal(manifest.runURL, `https://github.com/${repository}/actions/runs/123/attempts/1`);
  assert.throws(() => createSnapshotManifest(prepared, 'latest'), /摘要/);
});

test('拒绝错误仓库、非固定提交、非法基线和输出注入', () => {
  const f = fixture();
  assert.throws(() => f.prepare({ repository: 'other/repo' }), /仓库/);
  for (const commit of ['HEAD', f.commit.slice(0, 12), `${f.commit}\nimage=other`]) {
    assert.throws(() => f.prepare({ commit }), /40 位/);
  }
  for (const changes of [{ baseVersion: 'other' }, { runID: '1\nimage=other' }, { runAttempt: '0' }]) {
    assert.throws(() => f.prepare(changes));
  }
});

test('真实 Git 历史拒绝分支外提交、非祖先基线与落后基线', () => {
  const f = fixture();
  f.git('update-ref', `refs/remotes/origin/${branch}`, f.baseCommit);
  assert.throws(() => f.prepare());
  f.git('update-ref', `refs/remotes/origin/${branch}`, f.commit);
  assert.throws(() => f.prepare({ commit: f.baseCommit, baseVersion: 'v1.0.0-beta12-fork.2' }));
  f.git('tag', 'v1.0.0-beta12-fork.2');
  assert.throws(() => f.prepare(), /新基线/);
  assert.equal(f.prepare({ baseVersion: 'v1.0.0-beta12-fork.2' }).baseCommit, f.commit);
  assert.throws(() => f.prepare({ commit: f.baseCommit, baseVersion: 'v1.0.0-beta12-fork.2' }));
});

test('真实源码差异拒绝 schema、schema migration 和 data migration，允许普通代码更新', () => {
  for (const path of ['internal/ent/schema/model.go', 'internal/ent/migrate/schema.go',
    'internal/ent/migrate/datamigrate/new.go']) {
    const f = fixture();
    assert.doesNotThrow(() => f.prepare());
    const commit = f.commitFile(path, '迁移变化');
    f.git('update-ref', `refs/remotes/origin/${branch}`, commit);
    assert.throws(() => f.prepare({ commit }), /普通快照不支持/);
  }
});

test('提高发布下限后仍允许历史旧 tag，同时拒绝低于下限或落后的基线', () => {
  const f = fixture();
  const config = JSON.parse(readFileSync(join(f.root, 'release.config.json'), 'utf8'));
  config.minimumReleaseNumber = 5;
  const commit = f.commitFile('release.config.json', JSON.stringify(config));
  f.git('tag', 'v1.0.0-beta12-fork.5', commit);
  f.git('update-ref', `refs/remotes/origin/${branch}`, commit);
  assert.doesNotThrow(() => f.prepare({ commit, baseVersion: 'v1.0.0-beta12-fork.5' }));
  assert.throws(() => f.prepare({ commit }), /发布编号必须不小于 5/);
  f.git('tag', 'v1.0.0-beta12-fork.6', commit);
  assert.throws(() => f.prepare({ commit, baseVersion: 'v1.0.0-beta12-fork.5' }), /新基线/);
});

test('命令行向 Actions 输出固定参数，并从同一准备结果生成部署清单', () => {
  const f = fixture();
  const output = join(f.root, 'outputs.txt');
  const script = fileURLToPath(new URL('./snapshot.mjs', import.meta.url));
  const env = { ...process.env, GITHUB_REPOSITORY: repository, SNAPSHOT_COMMIT: f.commit,
    BASE_VERSION: baseVersion, GITHUB_RUN_ID: '123', GITHUB_RUN_ATTEMPT: '1', GITHUB_OUTPUT: output };
  execFileSync(process.execPath, [script, 'prepare'], { cwd: f.root, env });
  const values = Object.fromEntries(readFileSync(output, 'utf8').trim().split('\n').map((line) => {
    const index = line.indexOf('=');
    return [line.slice(0, index), line.slice(index + 1)];
  }));
  assert.equal(values.commit, f.commit);
  assert.equal(values.run_attempt, '1');
  const prepared = JSON.parse(values.prepared);
  assert.equal(values.image_tag, prepared.imageTag);
  assert.equal(values.version, prepared.version);
  const manifestPath = join(f.root, 'snapshot.json');
  const digest = `sha256:${'b'.repeat(64)}`;
  execFileSync(process.execPath, [script, 'manifest', manifestPath], {
    cwd: f.root, env: { ...env, SNAPSHOT_PREPARED: values.prepared, IMAGE_DIGEST: digest },
  });
  assert.deepEqual(JSON.parse(readFileSync(manifestPath, 'utf8')), createSnapshotManifest(prepared, digest));
});


test('快照基线必须同时包含原始上游和继承 fork 的历史', () => {
  for (const field of ['originalUpstream', 'inheritedFork']) {
    const f = fixture();
    const unrelated = f.git('-c', 'user.name=快照测试', '-c', 'user.email=test@example.invalid',
      'commit-tree', 'HEAD^{tree}', '-m', '不相关历史');
    const configPath = join(f.root, 'release.config.json');
    const config = JSON.parse(readFileSync(configPath, 'utf8'));
    config[field].commit = unrelated;
    writeFileSync(configPath, JSON.stringify(config));
    assert.throws(() => f.prepare());
  }
});

test('历史 syh 配置仍拒绝落后的快照基线', () => {
  const f = fixture();
  const configPath = join(f.root, 'release.config.json');
  const config = JSON.parse(readFileSync(configPath, 'utf8'));
  config.upstreamRepository = 'ChouUn/axon-hub';
  config.upstreamTag = 'v1.0.0-beta10-fork.7';
  writeFileSync(configPath, JSON.stringify(config));
  const baseVersion = `${config.upstreamTag}.syh.19`;
  f.git('tag', baseVersion, f.baseCommit);
  assert.doesNotThrow(() => f.prepare({ baseVersion }));
  f.git('tag', `${config.upstreamTag}.syh.20`, f.commit);
  assert.throws(() => f.prepare({ baseVersion }), /新基线/);
});
