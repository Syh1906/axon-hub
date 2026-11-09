import assert from 'node:assert/strict';
import test from 'node:test';
import { loadErrorKey, loadErrorRequestId } from './load-error.ts';

test('入口区分网络、接收超时、处理超时和权限失败', () => {
  const cases = [
    [{ status: 0 }, 'network'],
    [new TypeError('private message'), 'network'],
    [{ status: 408 }, 'timeout'],
    [{ status: 504 }, 'timeout'],
    [{ extensions: { code: 'REQUEST_TIMEOUT' } }, 'timeout'],
    [{ status: 400, extensions: { code: 'REQUEST_BODY_INCOMPLETE' } }, 'interrupted'],
    [{ status: 401 }, 'session'],
    [{ status: 403 }, 'forbidden'],
    [{ status: 400 }, 'invalid'],
    [{ status: 500 }, 'server'],
    [{ status: 422, extensions: { code: 'INTERNAL_SERVER_ERROR' } }, 'server'],
    [{ extensions: { code: 'INTERNAL_SERVER_ERROR' } }, 'server'],
  ];
  for (const [error, kind] of cases) assert.equal(loadErrorKey(error), `common.loadFailure.${kind}`);
});

test('只显示格式正确的服务端请求编号，不显示错误正文', () => {
  const id = 'ar-12345678-1234-1234-1234-123456789abc';
  assert.equal(loadErrorRequestId({ requestId: id }), id);
  assert.equal(loadErrorRequestId({ requestId: 'private diagnostic payload' }), undefined);
  assert.equal(loadErrorRequestId(null), undefined);
});
