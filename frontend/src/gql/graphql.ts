import { toast } from 'sonner';
import { getTokenFromStorage, removeTokenFromStorage } from '@/stores/authStore';
import { ensureFreshAccessToken } from '@/lib/auth-session';
import i18n from '@/lib/i18n';
import { withReadDeadline } from '@/lib/read-deadline';

export type ReadDiagnostics = {
  traceId: string;
  operationName?: string;
  operationType: 'query' | 'other';
  responseReceived: boolean;
  requestId?: string;
  elapsedMs: number;
};

export class GraphQLRequestError extends Error {
  status?: number;
  isAuthError: boolean;
  extensions?: Record<string, any>;
  requestId?: string;
  diagnostics?: ReadDiagnostics;

  constructor(message: string, options?: { status?: number; isAuthError?: boolean; extensions?: Record<string, any>; requestId?: string }) {
    super(message);
    this.name = 'GraphQLRequestError';
    this.status = options?.status;
    this.isAuthError = Boolean(options?.isAuthError);
    this.extensions = options?.extensions;
    this.requestId = options?.requestId;
  }
}

export function isAuthError(error: unknown): error is GraphQLRequestError {
  return error instanceof GraphQLRequestError && error.isAuthError;
}

// Utility function to extract the operation name from a GraphQL query string
export function extractOperationName(query: string): string | undefined {
  // Remove leading whitespace and match the operation name from GraphQL query/mutation/subscription
  // Pattern: (query|mutation|subscription)\s+Name
  const trimmedQuery = query.trim();
  const operationMatch = trimmedQuery.match(/^(query|mutation|subscription)\s+(\w+)/i);
  if (operationMatch) {
    return operationMatch[2]; // Return the operation name
  }
  return undefined;
}

export const GRAPHQL_ENDPOINT = '/admin/graphql';

function createTraceId() {
  // getRandomValues 同样适用于受支持的普通 HTTP 自托管后台。
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 15) | 64;
  bytes[8] = (bytes[8] & 63) | 128;
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
  return `at-${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

function isForbiddenGraphQLError(error: any): boolean {
  return error?.extensions?.code === 'FORBIDDEN' || error?.message?.toLowerCase().includes('permission denied');
}

export function isUnauthorizedGraphQLError(error: any): boolean {
  return error?.extensions?.code === 'UNAUTHENTICATED';
}

function redirectToSignIn() {
  const { pathname, search, hash } = window.location;
  if (pathname.startsWith('/sign-in')) {
    window.location.href = '/sign-in';
    return;
  }
  window.location.href = `/sign-in?redirect=${encodeURIComponent(pathname + search + hash)}`;
}

// GraphQL client function with token support
async function performGraphQLRequest<T>(
  query: string,
  variables: Record<string, any> | undefined,
  customHeaders: Record<string, string> | undefined,
  init: { signal?: AbortSignal; timeoutMs?: number } | undefined,
  diagnostics: ReadDiagnostics
): Promise<T> {
  // Get token from localStorage
  const token = await ensureFreshAccessToken();

  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    'AH-Trace-Id': diagnostics.traceId,
  };

  // Add Authorization header if token exists
  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }

  // Merge custom headers
  if (customHeaders) {
    Object.assign(headers, customHeaders);
  }
  diagnostics.traceId = new Headers(headers).get('AH-Trace-Id') ?? diagnostics.traceId;

  // Extract operation name from the query for tracing
  const operationName = extractOperationName(query);

  let response: Response;

  try {
    response = await fetch(GRAPHQL_ENDPOINT, {
      method: 'POST',
      headers,
      signal: init?.signal,
      body: JSON.stringify({
        query,
        variables,
        operationName, // Add operation name for tracing
      }),
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') {
      throw error;
    }
    throw new GraphQLRequestError('Network error', { status: undefined, isAuthError: false });
  }

  const requestId = response.headers.get('AH-Request-Id') ?? undefined;
  diagnostics.requestId = requestId;
  diagnostics.responseReceived = true;

  // Handle explicit auth failures (401 only — 403 is a permission denial, not a session issue)
  if (response.status === 401) {
    // Clear token and redirect to login
    if (getTokenFromStorage() === token) {
      removeTokenFromStorage();
      toast.error(i18n.t('common.errors.sessionExpiredSignIn'));
      redirectToSignIn();
    }
    throw new GraphQLRequestError('Unauthorized', { status: response.status, isAuthError: true, requestId });
  }

  // Handle permission denial — do NOT clear token or redirect
  if (response.status === 403) {
    throw new GraphQLRequestError('Forbidden', { status: 403, isAuthError: false, requestId });
  }

  // Check content type before parsing JSON
  const contentType = response.headers.get('content-type');
  if (!contentType || !contentType.includes('application/json')) {
    throw new GraphQLRequestError('Server returned non-JSON response. Please check the API endpoint.', {
      status: response.status,
      requestId,
    });
  }

  let result: { data: T; errors?: Array<{ message?: string; extensions?: Record<string, unknown> }> };
  try {
    result = await response.json();
  } catch (_error) {
    throw new GraphQLRequestError('Failed to parse server response as JSON', {
      status: response.status,
      requestId,
    });
  }

  if (!response.ok) {
    throw new GraphQLRequestError(result?.errors?.[0]?.message || 'Request failed', {
      extensions: result?.errors?.[0]?.extensions,
      status: response.status,
      requestId,
    });
  }

  if (result.errors) {
    const firstError = result.errors[0];

    // Check for authentication errors
    const authError = result.errors.find(isUnauthorizedGraphQLError);

    if (authError) {
      // Clear token and redirect to login
      if (getTokenFromStorage() === token) {
        removeTokenFromStorage();
        toast.error(i18n.t('common.errors.sessionExpiredSignIn'));
        redirectToSignIn();
      }
      throw new GraphQLRequestError('Unauthorized', { status: 401, isAuthError: true, requestId });
    }

    throw new GraphQLRequestError(firstError?.message || 'GraphQL Error', {
      status: isForbiddenGraphQLError(firstError) ? 403 : undefined,
      extensions: firstError?.extensions ?? {},
      requestId,
    });
  }

  return result.data;
}

export async function graphqlRequest<T>(
  query: string,
  variables?: Record<string, any>,
  customHeaders?: Record<string, string>,
  init?: { signal?: AbortSignal; timeoutMs?: number }
): Promise<T> {
  // 只限制明确的读取；写操作、订阅和 fragment 前缀不猜测为读取。
  const isRead = /^\s*(?:#[^\r\n]*[\r\n]\s*)*(?:query\b|\{)/.test(query);
  const startedAt = performance.now();
  const diagnostics: ReadDiagnostics = {
    traceId: createTraceId(),
    operationName: extractOperationName(query),
    operationType: isRead ? 'query' : 'other',
    responseReceived: false,
    elapsedMs: 0,
  };
  try {
    if (!isRead) return await performGraphQLRequest(query, variables, customHeaders, init, diagnostics);
    return await withReadDeadline(
      (signal) => performGraphQLRequest<T>(query, variables, customHeaders, { signal }, diagnostics),
      init?.signal,
      init?.timeoutMs
    );
  } catch (error) {
    if (error instanceof DOMException && error.name === 'TimeoutError') {
      error = new GraphQLRequestError(i18n.t('common.errors.readTimeout'), {
        status: 408,
        extensions: { code: 'CLIENT_READ_TIMEOUT' },
        requestId: diagnostics.requestId,
      });
    }
    if (error instanceof GraphQLRequestError) error.diagnostics = diagnostics;
    throw error;
  } finally {
    diagnostics.elapsedMs = Math.round(performance.now() - startedAt);
  }
}
