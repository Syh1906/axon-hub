type LoadError = {
  status?: number;
  extensions?: Record<string, unknown>;
  requestId?: string;
};

export function loadErrorKey(error: unknown): string {
  const failure = error && typeof error === 'object' ? (error as LoadError) : undefined;
  const code = failure?.extensions?.code;
  if (failure?.status === 408 || failure?.status === 504 || code === 'REQUEST_TIMEOUT' || code === 'REQUEST_BODY_TIMEOUT') {
    return 'common.loadFailure.timeout';
  }
  if (failure?.status === 401 || code === 'UNAUTHENTICATED') return 'common.loadFailure.session';
  if (failure?.status === 403 || code === 'FORBIDDEN') return 'common.loadFailure.forbidden';
  if (code === 'REQUEST_BODY_INCOMPLETE' || code === 'REQUEST_BODY_READ_FAILED') return 'common.loadFailure.interrupted';
  if (failure?.status === 0 || (failure?.status === undefined && !failure?.extensions)) return 'common.loadFailure.network';
  if (failure?.status === 400 || failure?.status === 422) return 'common.loadFailure.invalid';
  return 'common.loadFailure.server';
}

export function loadErrorRequestId(error: unknown): string | undefined {
  const id = error && typeof error === 'object' ? (error as LoadError).requestId : undefined;
  return typeof id === 'string' && /^ar-[a-f0-9-]{36}$/.test(id) ? id : undefined;
}
