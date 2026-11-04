import assert from 'node:assert/strict';
import test from 'node:test';
import { requestLogVariables, requestStatusCountVariables } from './request-log-variables.ts';

test('状态计数忽略已选状态与分页，保留其他条件和当前项目', () => {
  const where = {
    statusIn: ['failed'],
    offset: 100,
    projectID: 'old-project',
    sourceIn: ['api'],
    modelIDContainsFold: 'model',
    channelIDIn: ['channel'],
    apiKeyIDIn: ['key'],
    channelTagsIn: ['production'],
    apiKeyGroupRefsIn: [0, 7],
    timeRange: { period: 'TODAY' },
  };
  const result = requestStatusCountVariables(where, 'current-project');
  assert.deepEqual(result, {
    where: { projectID: 'current-project', sourceIn: ['api'], modelIDContainsFold: 'model', apiKeyIDIn: ['key'] },
    filter: { channelTagsIn: ['production'], channelIDsIn: ['channel'], apiKeyGroupRefsIn: [0, 7], timeRange: { period: 'TODAY' }, offset: undefined },
  });
  assert.deepEqual(requestStatusCountVariables({ ...where, statusIn: ['completed'], offset: 200 }, 'current-project'), result);
  assert.deepEqual(where.statusIn, ['failed']);
  assert.equal(where.offset, 100);
});

test('列表参数仍保留状态与分页，管理范围不自动加入项目', () => {
  assert.deepEqual(requestLogVariables({ statusIn: ['failed'], offset: 20 }), {
    where: { statusIn: ['failed'] },
    filter: { channelTagsIn: undefined, channelIDsIn: undefined, apiKeyGroupRefsIn: undefined, timeRange: undefined, offset: 20 },
  });
  assert.deepEqual(requestStatusCountVariables().where, {});
});
