export type SelfUsageMeta = {
  apiKeyName: string;
  apiKeyType: string;
  currencyCode: string;
  timezone: string;
  maxRangeDays: number;
};
export type Usage = {
  successRequests: number;
  failedRequests: number;
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  reasoningTokens: number;
  totalTokens: number;
  cost: number | null;
  usageRecords: number;
  unpricedRecords: number;
};
export type SelfUsageStats = {
  start: string;
  end: string;
  totals: Usage;
  models: { model: string; usage: Usage }[];
  daily: { date: string; usage: Usage }[];
};
export type RequestStatus = 'SUCCESS' | 'FAILED' | 'PROCESSING';
export type SelfUsageCostItem = {
  image?: { width: number; height: number; tier: string; tierSource?: string; source: string } | null;
  imageRule?: string;
  unpriced?: boolean;
  itemCode: string;
  promptWriteCacheVariantCode: string | null;
  quantity: number;
  subtotal: number;
  tierBreakdown: { upTo: number | null; units: number; subtotal: number }[];
};
export type UsageRequest = Omit<Usage, 'successRequests' | 'failedRequests'> & {
  id: number;
  createdAt: string;
  model: string;
  status: RequestStatus;
  requestStatus: string;
  costItems: SelfUsageCostItem[];
  costMultiplier: number | null;
  costMultiplierMixed: boolean;
  stream: boolean;
  latencyMs: number | null;
  firstTokenLatencyMs: number | null;
};
export type RequestPage = { items: UsageRequest[]; page: number; pageSize: number; total: number };

export class SelfUsageError extends Error {
  constructor(
    message: string,
    public status?: number,
    public code?: string
  ) {
    super(message);
    this.name = 'SelfUsageError';
  }
}

const summaryFields = `successRequests failedRequests inputTokens outputTokens cacheReadTokens cacheWriteTokens reasoningTokens totalTokens cost usageRecords unpricedRecords`;
const metaQuery = `query SelfUsageMeta { selfUsageMeta { apiKeyName apiKeyType currencyCode timezone maxRangeDays } }`;
const statsQuery = `query SelfUsageStats($start: String!, $end: String!) {
  selfUsageStats(start: $start, end: $end) {
    start end totals { ${summaryFields} }
    models { model usage { ${summaryFields} } }
    daily { date usage { ${summaryFields} } }
  }
}`;
const requestsQuery = `query SelfUsageRequests($start: String!, $end: String!, $model: String, $status: SelfUsageRequestStatus, $page: Int, $pageSize: Int) {
  selfUsageRequests(start: $start, end: $end, model: $model, status: $status, page: $page, pageSize: $pageSize) {
    items { id createdAt model status requestStatus stream latencyMs firstTokenLatencyMs inputTokens outputTokens cacheReadTokens cacheWriteTokens reasoningTokens totalTokens cost usageRecords unpricedRecords costMultiplier costMultiplierMixed costItems { image { width height tier source tierSource } imageRule unpriced itemCode promptWriteCacheVariantCode quantity subtotal tierBreakdown { upTo units subtotal } } }
    page pageSize total
  }
}`;

async function execute<T>(key: string, query: string, variables?: Record<string, unknown>, signal?: AbortSignal): Promise<T> {
  const response = await fetch('/self-service/v1/graphql', {
    method: 'POST',
    headers: { Authorization: `Bearer ${key}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({ query, variables }),
    cache: 'no-store',
    signal,
  });
  if (!response.ok) throw new SelfUsageError('HTTP error', response.status);
  const payload = (await response.json()) as { data?: T; errors?: { message: string; extensions?: { code?: string } }[] };
  if (payload.errors?.length) throw new SelfUsageError(payload.errors[0].message, undefined, payload.errors[0].extensions?.code);
  if (!payload.data) throw new SelfUsageError('Empty response');
  return payload.data;
}

export async function fetchMeta(key: string, signal?: AbortSignal): Promise<SelfUsageMeta> {
  return (await execute<{ selfUsageMeta: SelfUsageMeta }>(key, metaQuery, undefined, signal)).selfUsageMeta;
}
export async function fetchStats(key: string, start: string, end: string, signal?: AbortSignal): Promise<SelfUsageStats> {
  return (await execute<{ selfUsageStats: SelfUsageStats }>(key, statsQuery, { start, end }, signal)).selfUsageStats;
}
export async function fetchRequests(
  key: string,
  start: string,
  end: string,
  model: string | null,
  status: RequestStatus | null,
  page: number,
  signal?: AbortSignal
): Promise<RequestPage> {
  return (
    await execute<{ selfUsageRequests: RequestPage }>(
      key,
      requestsQuery,
      {
        start,
        end,
        model,
        status,
        page,
        pageSize: 20,
      },
      signal
    )
  ).selfUsageRequests;
}
