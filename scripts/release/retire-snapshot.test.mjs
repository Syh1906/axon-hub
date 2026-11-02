import assert from 'node:assert/strict';
import test from 'node:test';
import { planRetirement } from './retire-snapshot.mjs';

function fixture() {
  const manifest = (commit, run, digest) => {
    const snapshotId = `snapshot-${commit}-${run}-1`;
    return { kind: 'snapshot', repository: 'Syh1906/axon-hub', snapshotId,
      image: `ghcr.io/syh1906/axon-hub@sha256:${digest.repeat(64)}`,
      imageTags: [snapshotId, `${snapshotId}-amd64`, `${snapshotId}-arm64`] };
  };
  const previous = manifest('a'.repeat(12), '1', 'a');
  const replacement = manifest('b'.repeat(12), '2', 'b');
  const versions = previous.imageTags.map((tag, index) => ({ id: index + 1,
    name: `sha256:${String.fromCharCode(97 + index).repeat(64)}`,
    metadata: { container: { tags: [tag] } } }));
  versions[1].name = `sha256:${'c'.repeat(64)}`;
  versions[2].name = `sha256:${'d'.repeat(64)}`;
  versions.push({ id: 100, name: replacement.image.split('@')[1], metadata: { container: { tags: replacement.imageTags } } });
  const artifacts = [
    { id: 10, name: previous.snapshotId, size_in_bytes: 650 },
    { id: 11, name: 'build.dockerbuild', size_in_bytes: 1000 },
    { id: 12, name: 'unrelated-audit', size_in_bytes: 20 },
  ];
  return { previous, replacement, versions, artifacts };
}

test('只计划指定旧快照的三种镜像与构建附件，保留新快照和无关附件', () => {
  const f = fixture();
  const before = JSON.stringify(f);
  const plan = planRetirement(f.previous, f.replacement, f.versions, f.artifacts);
  assert.deepEqual(plan.packageVersions.map((item) => item.id), [1, 2, 3]);
  assert.deepEqual(plan.artifacts.map((item) => item.id), [10, 11]);
  assert.equal(plan.replacementImage, f.replacement.image);
  assert.equal(JSON.stringify(f), before);
});

test('拒绝与正式版或其他快照共享的包版本', () => {
  for (const tag of ['v1.0.0-beta10-fork.7.syh.10', 'snapshot-other']) {
    const f = fixture();
    f.versions[0].metadata.container.tags.push(tag);
    assert.throws(() => planRetirement(f.previous, f.replacement, f.versions, f.artifacts), /共享/);
  }
});

test('拒绝相同替代镜像、标签缺失、主摘要不匹配或清单附件缺失', () => {
  const f = fixture();
  assert.throws(() => planRetirement(f.previous, f.previous, f.versions, f.artifacts), /不同/);
  assert.throws(() => planRetirement(f.previous, f.replacement, f.versions.slice(1), f.artifacts), /缺失/);
  const changed = structuredClone(f.versions);
  changed[0].name = `sha256:${'e'.repeat(64)}`;
  assert.throws(() => planRetirement(f.previous, f.replacement, changed, f.artifacts), /主摘要/);
  assert.throws(() => planRetirement(f.previous, f.replacement, f.versions, []), /附件缺失/);
});

test('拒绝将正式清单、其他仓库或不完整快照作为退役目标', () => {
  const f = fixture();
  for (const changes of [{ kind: 'release' }, { repository: 'other/repo' }, { imageTags: [] }]) {
    assert.throws(() => planRetirement({ ...f.previous, ...changes }, f.replacement, f.versions, f.artifacts));
  }
});

test('正式发布可以替代旧快照，但不删除正式镜像或无关附件', () => {
  const f = fixture();
  const release = { schemaVersion: 1, repository: 'Syh1906/axon-hub',
    version: 'v1.0.0-beta10-fork.7.syh.11', commit: 'b'.repeat(40),
    image: f.replacement.image, platforms: ['linux/amd64', 'linux/arm64'] };
  const plan = planRetirement(f.previous, release, f.versions, f.artifacts);
  assert.deepEqual(plan.packageVersions.map((item) => item.id), [1, 2, 3]);
  assert.deepEqual(plan.artifacts.map((item) => item.id), [10, 11]);
  assert.equal(plan.replacementVersion, release.version);
  assert.equal(plan.replacementSnapshotId, null);
  assert.throws(() => planRetirement(f.previous, { ...release, commit: 'invalid' }, f.versions, f.artifacts), /正式发布清单/);
  assert.throws(() => planRetirement(f.previous, { ...release, image: f.previous.image }, f.versions, f.artifacts), /不同/);
});
