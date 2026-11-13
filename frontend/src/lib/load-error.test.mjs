import assert from 'node:assert/strict';
import test from 'node:test';
import { loadErrorKey, loadErrorRequestId, readFailureToastId } from './load-error.ts';

test('入口区分网络、接收超时、处理超时和权限失败', () => {
  const cases = [
    [{ extensions: { code: 'STATISTICS_PENDING' } }, 'statisticsPending'],
    [{ extensions: { code: 'STATISTICS_RANGE_CHANGED' } }, 'statisticsChanged'],
    [{ status: 0 }, 'network'],
    [new TypeError('private message'), 'network'],
    [{ status: 408 }, 'timeout'],
    [{ status: 504 }, 'timeout'],
    [{ extensions: { code: 'REQUEST_TIMEOUT' } }, 'executionTimeout'],
    [{ status: 408, extensions: { code: 'REQUEST_BODY_TIMEOUT' } }, 'bodyTimeout'],
    [{ status: 408, extensions: { code: 'CLIENT_READ_TIMEOUT' } }, 'timeout'],
    [{ status: 408, extensions: { code: 'CLIENT_READ_TIMEOUT' }, diagnostics: { responseReceived: true } }, 'responseTimeout'],
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

test('客户端关联编号可用于尚未收到响应的失败，服务端编号优先', () => {
  const traceId = 'at-12345678-1234-1234-1234-123456789abc';
  const requestId = 'ar-12345678-1234-1234-1234-123456789abc';
  assert.equal(loadErrorRequestId({ diagnostics: { traceId } }), traceId);
  assert.equal(loadErrorRequestId({ requestId, diagnostics: { traceId } }), requestId);
  assert.equal(loadErrorRequestId({ diagnostics: { traceId: 'private text' } }), undefined);
});

test('同页读取连接错误合并通知，不合并写入、权限或业务错误', () => {
  const read = { diagnostics: { operationType: 'query' } };
  const a = readFailureToastId({ ...read, status: 408 }, '/channels');
  assert.equal(a, readFailureToastId({ ...read, status: 0 }, '/channels'));
  assert.notEqual(a, readFailureToastId({ ...read, status: 408 }, '/project/requests'));
  assert.equal(readFailureToastId({ ...read, status: 403 }, '/channels'), undefined);
  assert.equal(readFailureToastId({ ...read, extensions: { code: 'INVALID_INPUT' } }, '/channels'), undefined);
  assert.equal(readFailureToastId({ status: 408, diagnostics: { operationType: 'other' } }, '/channels'), undefined);
});

test('只显示格式正确的服务端请求编号，不显示错误正文', () => {
  const id = 'ar-12345678-1234-1234-1234-123456789abc';
  assert.equal(loadErrorRequestId({ requestId: id }), id);
  assert.equal(loadErrorRequestId({ requestId: 'private diagnostic payload' }), undefined);
  assert.equal(loadErrorRequestId(null), undefined);
});
