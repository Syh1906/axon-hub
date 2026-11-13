import type { QueryClient } from '@tanstack/react-query';
import type { ConsumptionFilter, ConsumptionResult, ConsumptionMetrics, ConsumptionEntry } from './consumption';

export type SlicePage = {
  days: { start: string; revision: string }[];
  pending: boolean;
  resolvedAt: string;
  slices: ConsumptionResult[];
  next: string | null;
  rangeStart: string | null;
  rangeEnd: string;
  timezone: string;
  currencyCode: string;
};

type DayCache = {
  timezone: string;
  currencyCode: string;
  days: Record<string, ConsumptionResult>;
  checkedAt?: Record<string, number>;
};

const SUM_FIELDS = [
  'requestCount',
  'totalTokens',
  'totalCost',
  'unpricedRequestCount',
  'pricedRequestCount',
  'tokenPricedRequestCount',
  'speedSampleCount',
  'cacheRequests',
  'cacheReadTokens',
  'cacheInputTokens',
  'cacheCreationCost',
  'pricedCost',
  'tokenPricedCost',
  'pricedTokens',
  'successCount',
  'outcomeCount',
  'firstTokenSum',
  'firstTokenCount',
  'speedSum',
  'coefficientSamples',
  'coefficientEligible',
  'coefficientTheoretical',
  'coefficientObserved',
] as const;

function addMetrics(a: ConsumptionMetrics, b: ConsumptionMetrics) {
  for (const key of SUM_FIELDS) a[key] += b[key];
}

function finishMetrics(m: ConsumptionMetrics) {
  const ratio = (a: number, b: number) => (b > 0 ? a / b : null);
  m.avgCostPerRequest = ratio(m.pricedCost, m.pricedRequestCount);
  m.avgCostPerMillionTokens = ratio(m.tokenPricedCost * 1_000_000, m.pricedTokens);
  m.successRate = ratio(m.successCount, m.outcomeCount);
  m.avgFirstTokenMs = ratio(m.firstTokenSum, m.firstTokenCount);
  m.avgTokensPerSecond = ratio(m.speedSum, m.speedSampleCount);
  const hit = ratio(m.cacheReadTokens, m.cacheInputTokens);
  m.cacheHitRate = hit === null ? (m.cacheRequests > 0 ? 0 : null) : Math.max(0, Math.min(1, hit));
  m.cacheCoefficient = null;
  if (m.coefficientSamples > 0) {
    // 与服务端整数计算一致，乘法使用 BigInt，避免中间值越过安全整数范围。
    const scaled = (a: number, b: number) => (b <= 0 ? 0n : (BigInt(Math.max(0, Math.trunc(a))) * 10000n) / BigInt(Math.trunc(b)));
    const clamp = (n: bigint) => (n > 10000n ? 10000n : n);
    const factor = m.coefficientEligible >= 100 ? 10000n : m.coefficientEligible >= 30 ? 6000n : m.coefficientEligible >= 5 ? 3000n : 1000n;
    const confidence = (clamp(scaled(m.coefficientEligible, m.coefficientSamples)) * factor) / 10000n;
    m.cacheCoefficient = Number((clamp(scaled(m.coefficientObserved, m.coefficientTheoretical)) * confidence) / 10000n);
  }
}

export function combineConsumptionSlices(
  days: ConsumptionResult[],
  metadata: Pick<SlicePage, 'rangeStart' | 'rangeEnd' | 'timezone' | 'currencyCode'>,
  filter: ConsumptionFilter
): ConsumptionResult {
  const entries = new Map<string, ConsumptionEntry>();
  const models = new Map<string, Map<string, ConsumptionEntry['models'][number]>>();
  const cacheSamples = new Map<string, ConsumptionResult['cacheSamples'][number]>();
  const performanceSamples = new Map<string, ConsumptionResult['performanceSamples'][number]>();
  for (const day of days) {
    for (const sample of day.cacheSamples ?? []) {
      const key = JSON.stringify([sample.channelID, sample.model]);
      const previous = cacheSamples.get(key);
      if (previous) {
        previous.samples += sample.samples;
        previous.eligible += sample.eligible;
        previous.theoretical += sample.theoretical;
        previous.observed += sample.observed;
      } else cacheSamples.set(key, { ...sample });
    }
    for (const sample of day.performanceSamples ?? []) {
      const key = JSON.stringify([sample.channelID, sample.model]);
      const previous = performanceSamples.get(key);
      if (previous) addMetrics(previous.metrics, sample.metrics);
      else performanceSamples.set(key, { ...sample, metrics: { ...sample.metrics } });
    }
    for (const entry of day.entries) {
      const existing = entries.get(entry.id);
      if (existing) addMetrics(existing.metrics, entry.metrics);
      else entries.set(entry.id, { ...entry, metrics: { ...entry.metrics }, models: [] });
      if (!models.has(entry.id)) models.set(entry.id, new Map());
      const byModel = models.get(entry.id)!;
      for (const model of entry.models) {
        const existing = byModel.get(model.model);
        if (existing) addMetrics(existing.metrics, model.metrics);
        else byModel.set(model.model, { ...model, metrics: { ...model.metrics } });
      }
    }
  }
  const compareNullable = (a: number | null, b: number | null) => (a === b ? 0 : a === null ? 1 : b === null ? -1 : b - a);
  const compare = (a: ConsumptionMetrics, b: ConsumptionMetrics) =>
    filter.metric === 'COST'
      ? b.totalCost - a.totalCost
      : (filter.dimension === 'CHANNEL' ? compareNullable(a.cacheCoefficient, b.cacheCoefficient) : 0) ||
        compareNullable(a.cacheHitRate, b.cacheHitRate) ||
        b.requestCount - a.requestCount;
  for (const entry of entries.values()) {
    entry.models = [...models.get(entry.id)!.values()];
    if (filter.dimension === 'CHANNEL') {
      const performanceKeys = ['successCount', 'outcomeCount', 'firstTokenSum', 'firstTokenCount', 'speedSum', 'speedSampleCount'] as const;
      const reset = (m: ConsumptionMetrics) => {
        for (const key of performanceKeys) m[key] = 0;
        m.coefficientSamples = m.coefficientEligible = m.coefficientTheoretical = m.coefficientObserved = 0;
      };
      reset(entry.metrics);
      for (const model of entry.models) reset(model.metrics);
      for (const sample of performanceSamples.values())
        if (sample.channelID === entry.id) {
          const model = entry.models.find((m) => m.model === sample.model);
          // 缓存榜仅向整个范围中有缓存账目的模型附加性能；消耗榜保留实际尝试。
          if (!model) continue;
          for (const key of performanceKeys) {
            model.metrics[key] += sample.metrics[key];
            entry.metrics[key] += sample.metrics[key];
          }
        }
      for (const sample of cacheSamples.values())
        if (sample.channelID === entry.id) {
          const apply = (m: ConsumptionMetrics) => {
            m.coefficientSamples += sample.samples;
            m.coefficientEligible += sample.eligible;
            m.coefficientTheoretical += sample.theoretical;
            m.coefficientObserved += sample.observed;
          };
          apply(entry.metrics);
          const model = entry.models.find((m) => m.model === sample.model);
          if (model) apply(model.metrics);
        }
    }
    finishMetrics(entry.metrics);
    for (const model of entry.models) finishMetrics(model.metrics);
    entry.models.sort(
      (a, b) =>
        (filter.metric === 'CACHE'
          ? compareNullable(a.metrics.cacheHitRate, b.metrics.cacheHitRate) || b.metrics.requestCount - a.metrics.requestCount
          : b.metrics.totalCost - a.metrics.totalCost) || a.model.localeCompare(b.model)
    );
  }
  return {
    revision: '',
    cacheSamples: [...cacheSamples.values()],
    performanceSamples: [...performanceSamples.values()],
    rangeStart: metadata.rangeStart,
    rangeEnd: metadata.rangeEnd,
    timezone: metadata.timezone,
    currencyCode: metadata.currencyCode,
    entries: [...entries.values()].sort((a, b) => compare(a.metrics, b.metrics) || a.id.localeCompare(b.id, 'en', { numeric: true })),
    generatedAt: days.length
      ? days.reduce((oldest, day) => (Date.parse(day.generatedAt) < Date.parse(oldest) ? day.generatedAt : oldest), days[0].generatedAt)
      : new Date().toISOString(),
    resolvedAt: days.length ? (days[0].resolvedAt ?? days[0].generatedAt) : new Date().toISOString(),
  };
}

export function reportingDate(at: string | number | Date, timezone: string) {
  return new Intl.DateTimeFormat('en-CA', { timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(at));
}

const addDays = (day: string, count: number) => new Date(Date.parse(`${day}T00:00:00Z`) + count * 86400000).toISOString().slice(0, 10);

export function reportingMidnight(day: string, timezone: string) {
  const target = Date.parse(`${day}T00:00:00Z`);
  let guess = target;
  const format = new Intl.DateTimeFormat('en-CA', {
    timeZone: timezone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23',
  });
  for (let i = 0; i < 4; i++) {
    const p = Object.fromEntries(format.formatToParts(guess).map((part) => [part.type, part.value]));
    const shown = Date.parse(`${p.year}-${p.month}-${p.day}T${p.hour}:${p.minute}:${p.second}Z`);
    const next = guess + target - shown;
    if (next === guess) break;
    guess = next;
  }
  return new Date(guess).toISOString();
}

function requestedDates(input: ConsumptionFilter, timezone: string, now: number) {
  const today = reportingDate(now, timezone);
  let start = today,
    end = addDays(today, 1);
  switch (input.period) {
    case 'ALL':
      return null; // 未取得完整最早日期时不猜测全历史覆盖。
    case 'YESTERDAY':
      start = addDays(today, -1);
      end = today;
      break;
    case 'WEEK': {
      const weekday = new Date(`${today}T00:00:00Z`).getUTCDay();
      start = addDays(today, -(weekday + 6) % 7);
      end = addDays(start, 7);
      break;
    }
    case 'MONTH':
      start = `${today.slice(0, 7)}-01`;
      end = new Date(Date.UTC(Number(today.slice(0, 4)), Number(today.slice(5, 7)), 1)).toISOString().slice(0, 10);
      break;
    case 'CUSTOM':
      if (!input.startDate || !input.endDate) return null;
      start = input.startDate;
      end = addDays(input.endDate, 1);
      break;
  }
  return { start, end, today };
}

function cachedRange(input: ConsumptionFilter, cache: DayCache, now: number) {
  const range = requestedDates(input, cache.timezone, now);
  if (!range) return null;
  const { start, end, today } = range;
  const coveredEnd = end < addDays(today, 1) ? end : addDays(today, 1);
  if ((Date.parse(coveredEnd) - Date.parse(start)) / 86400000 > Object.keys(cache.days).length) return null;
  const days: ConsumptionResult[] = [];
  for (let date = start; date < coveredEnd; date = addDays(date, 1)) {
    const day = cache.days[date];
    if (!day || now - (cache.checkedAt?.[date] ?? Date.parse(day.generatedAt)) >= (date === today ? 60000 : 300000)) return null;
    days.push(day);
  }
  return {
    days,
    metadata: {
      timezone: cache.timezone,
      currencyCode: cache.currencyCode,
      rangeStart: reportingMidnight(start, cache.timezone),
      rangeEnd: reportingMidnight(end, cache.timezone),
    },
  };
}

export type KnownSlice = { start: string; revision: string };
type FetchSlices = (after?: string, known?: KnownSlice[], asOf?: string, verify?: boolean) => Promise<SlicePage>;

function waitForSummary(signal: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    signal.throwIfAborted();
    const cancel = () => {
      clearTimeout(timer);
      reject(signal.reason);
    };
    const timer = setTimeout(() => {
      signal.removeEventListener('abort', cancel);
      resolve();
    }, 300);
    signal.addEventListener('abort', cancel, { once: true });
  });
}

function trimDays(cache: DayCache) {
  let rows = 0;
  for (const [index, date] of Object.keys(cache.days)
    .sort(
      (a, b) =>
        Date.parse(cache.days[b].resolvedAt ?? cache.days[b].generatedAt) -
        Date.parse(cache.days[a].resolvedAt ?? cache.days[a].generatedAt)
    )
    .entries()) {
    rows +=
      cache.days[date].entries.reduce((n, entry) => n + 1 + entry.models.length, 0) +
      cache.days[date].cacheSamples.length +
      cache.days[date].performanceSamples.length;
    if (index >= 90 || rows > 10000) {
      delete cache.days[date];
      if (cache.checkedAt) delete cache.checkedAt[date];
    }
  }
}

export async function loadConsumptionSlices(
  client: QueryClient,
  key: readonly unknown[],
  input: ConsumptionFilter,
  fetchPage: FetchSlices,
  signal: AbortSignal,
  reuse = true
) {
  signal.throwIfAborted();
  // 固定前缀只占一个默认项，任意搜索词和权限范围不会积累全局 defaults。
  client.setQueryDefaults(['consumptionDays'], { gcTime: 30 * 60000 });
  let cache = client.getQueryData<DayCache>(key);
  const covered = reuse && cache && cachedRange(input, cache, Date.now());
  if (covered) return { ...combineConsumptionSlices(covered.days, covered.metadata, input), resolvedAt: new Date().toISOString() };
  const started = Date.now();
  for (let attempt = 0; attempt < 3; attempt++) {
    let combined: ConsumptionResult | undefined;
    let after: string | undefined;
    let first: SlicePage | undefined;
    const used: KnownSlice[] = [];
    do {
      const pageDays = new Map<string, ConsumptionResult>();
      if (cache) for (const value of Object.values(cache.days)) pageDays.set(value.rangeStart!, value);
      let page: SlicePage;
      do {
        signal.throwIfAborted();
        const dates = cache && requestedDates(input, cache.timezone, Date.parse(first?.resolvedAt ?? new Date().toISOString()));
        const from = after ?? first?.rangeStart ?? (dates && cache ? reportingMidnight(dates.start, cache.timezone) : undefined);
        const known = [...pageDays.values()]
          .filter(
            (day) =>
              !!day.revision &&
              (!from || day.rangeStart! >= from) &&
              (!dates || reportingDate(day.rangeStart!, cache!.timezone) < dates.end)
          )
          .sort((a, b) => a.rangeStart!.localeCompare(b.rangeStart!))
          .slice(0, 31)
          .map((day) => ({ start: day.rangeStart!, revision: day.revision }));
        page = await fetchPage(after, known, first?.resolvedAt);
        signal.throwIfAborted();
        first ??= page;
        if (
          page.timezone !== first.timezone ||
          page.currencyCode !== first.currencyCode ||
          page.rangeStart !== first.rangeStart ||
          page.rangeEnd !== first.rangeEnd
        ) {
          throw Object.assign(new Error('统计范围已变化，请重试'), { extensions: { code: 'STATISTICS_RANGE_CHANGED' } });
        }
        for (const day of page.slices) pageDays.set(day.rangeStart!, day);
        if (page.pending) {
          if (Date.now() - started > 180000)
            throw Object.assign(new Error('统计汇总仍在处理，请稍后重试'), { extensions: { code: 'STATISTICS_PENDING' } });
          await waitForSummary(signal);
        }
      } while (page.pending);
      const current: ConsumptionResult[] = [];
      for (const state of page.days) {
        const day = pageDays.get(state.start);
        if (!day || day.revision !== state.revision)
          throw Object.assign(new Error('统计日期数据不完整，请重试'), { extensions: { code: 'INTERNAL_SERVER_ERROR' } });
        current.push(day);
        used.push(state);
      }
      combined = combineConsumptionSlices(combined ? [combined, ...current] : current, first, input);
      if (!cache || cache.timezone !== page.timezone || cache.currencyCode !== page.currencyCode)
        cache = { timezone: page.timezone, currencyCode: page.currencyCode, days: {} };
      cache = { ...cache, days: { ...cache.days }, checkedAt: { ...cache.checkedAt } };
      for (const day of current) {
        const date = reportingDate(day.rangeStart!, page.timezone);
        cache.days[date] = day;
        cache.checkedAt![date] = Date.now();
      }
      trimDays(cache);
      if (page.next && after && page.next <= after)
        throw Object.assign(new Error('统计日期未前进，请重试'), { extensions: { code: 'INTERNAL_SERVER_ERROR' } });
      after = page.next ?? undefined;
    } while (after);
    // 在同一数据库读取快照内复核所有已使用日期，避免分页时移组/补价混合前后归属。
    const checked = await fetchPage(undefined, used, first!.resolvedAt, true);
    signal.throwIfAborted();
    if (!checked.pending) {
      client.setQueryData(key, cache);
      return { ...combined!, resolvedAt: first!.resolvedAt };
    }
  }
  throw Object.assign(new Error('统计数据正在更新，请重试'), { extensions: { code: 'STATISTICS_RANGE_CHANGED' } });
}
