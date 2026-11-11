type LoadError = {
  status?: number;
  extensions?: Record<string, unknown>;
  requestId?: string;
  diagnostics?: { traceId: string; operationType: string; responseReceived: boolean };
};

export function loadErrorKey(error: unknown): string {
  const failure = error && typeof error === 'object' ? (error as LoadError) : undefined;
  const code = failure?.extensions?.code;
  if (code === 'REQUEST_BODY_TIMEOUT') return 'common.loadFailure.bodyTimeout';
  if (code === 'REQUEST_TIMEOUT') return 'common.loadFailure.executionTimeout';
  if (code === 'CLIENT_READ_TIMEOUT' && failure?.diagnostics?.responseReceived) return 'common.loadFailure.responseTimeout';
  if (failure?.status === 408 || failure?.status === 504 || code === 'CLIENT_READ_TIMEOUT') {
    return 'common.loadFailure.timeout';
  }
  if (failure?.status === 401 || code === 'UNAUTHENTICATED') return 'common.loadFailure.session';
  if (failure?.status === 403 || code === 'FORBIDDEN') return 'common.loadFailure.forbidden';
  if (code === 'REQUEST_BODY_INCOMPLETE' || code === 'REQUEST_BODY_READ_FAILED') return 'common.loadFailure.interrupted';
  if (failure?.status === 0 || (failure?.status === undefined && !failure?.extensions)) return 'common.loadFailure.network';
  if (code === 'INTERNAL_SERVER_ERROR') return 'common.loadFailure.server';
  if (failure?.status === 400 || failure?.status === 422) return 'common.loadFailure.invalid';
  return 'common.loadFailure.server';
}

export function loadErrorRequestId(error: unknown): string | undefined {
  const failure = error && typeof error === 'object' ? (error as LoadError) : undefined;
  const id = failure?.requestId;
  if (typeof id === 'string' && /^ar-[a-f0-9-]{36}$/.test(id)) return id;
  const traceId = failure?.diagnostics?.traceId;
  return typeof traceId === 'string' && /^at-[a-f0-9-]{36}$/.test(traceId) ? traceId : undefined;
}

// 只合并读取时的连接故障；写入、权限和业务错误保留各自的反馈。
export function readFailureToastId(error: unknown, pathname: string): string | undefined {
  const failure = error && typeof error === 'object' ? (error as LoadError) : undefined;
  if (failure?.diagnostics?.operationType !== 'query') return undefined;
  const key = loadErrorKey(error);
  return ['timeout', 'bodyTimeout', 'executionTimeout', 'responseTimeout', 'network', 'interrupted']
    .some((kind) => key === `common.loadFailure.${kind}`) ? `read-failure:${pathname}` : undefined;
}
