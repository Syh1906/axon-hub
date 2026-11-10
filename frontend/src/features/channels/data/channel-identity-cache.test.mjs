import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';
import { QueryClient } from '@tanstack/react-query';

const source = ts.transpileModule(readFileSync(new URL('./channel-identity-cache.ts', import.meta.url), 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2023 },
}).outputText;
const { invalidateChannelIdentityQueries } = await import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`);

test('渠道标签变化只让渠道排行榜重新读取，其他统计缓存保持有效', async () => {
  const client = new QueryClient();
  const key = (view, dimension) => ['consumption', view, null, 'viewer', { dimension }];
  const channels = key('leaderboard', 'CHANNEL');
  const models = key('leaderboard', 'MODEL');
  const usage = key('usage', 'API_KEY_GROUP');
  for (const k of [channels, models, usage]) client.setQueryData(k, { entries: [] });
  await invalidateChannelIdentityQueries(client);
  assert.equal(client.getQueryState(channels).isInvalidated, true);
  assert.equal(client.getQueryState(models).isInvalidated, false);
  assert.equal(client.getQueryState(usage).isInvalidated, false);
  client.clear();
});
