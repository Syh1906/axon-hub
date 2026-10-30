import { useCallback, useEffect, useState, type FormEvent } from 'react';
import { QueryClientProvider, useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Area, AreaChart, CartesianGrid, Legend, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { RequestIdCell, TokensCell, ReadCacheCell, WriteCacheCell, CostCell, DurationCell } from '@/components/request-usage-cells';
import { ThemeSwitch } from '@/components/theme-switch';
import { fetchMeta, fetchRequests, fetchStats, SelfUsageError, type RequestStatus, type SelfUsageMeta, type Usage } from './api';
import type { SelfUsageIdentity } from './api';
import { SelfUsageBudgets } from './components/budgets';
import { Cost } from './components/cost';
import { CacheHitCard } from './components/cache-hit-card';
import { PageError } from './components/page-error';
import { displayCost } from './cost/format';
import { selfUsageQueryClient } from './query-client';
import { sameUsageScope } from './scope';
import { getPageKey, setPageKey } from './session';
import { chartDay, presetRange, validRange, type DatePreset, type DateRange } from './utils/date-range';

function usePageLabels(meta: SelfUsageMeta) {
  const { t, i18n } = useTranslation();
  const locale = i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US';
  const number = new Intl.NumberFormat(locale);
  const money = (cost: number | null, unpricedRecords: number) =>
    displayCost(
      cost,
      unpricedRecords,
      (val) => t('currencies.format', { val, currency: meta.currencyCode, locale, minimumFractionDigits: 6 }),
      (count) => t('selfUsage.cost.unpriced', { count })
    );
  return { t, i18n, locale, number, money };
}

function Login({
  onLogin,
  error,
  onError,
}: {
  onLogin: (key: string, meta: SelfUsageMeta) => void;
  error: Error | null;
  onError: (error: Error | null) => void;
}) {
  const { t } = useTranslation();
  const [input, setInput] = useState('');
  const [pending, setPending] = useState(false);
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (pending) return;
    const key = input.trim();
    if (!key) {
      onError(new Error(t('selfUsage.login.required')));
      return;
    }
    setPending(true);
    onError(null);
    try {
      const meta = await fetchMeta(key);
      onLogin(key, meta);
      setInput('');
    } catch (caught) {
      if (caught instanceof SelfUsageError && caught.status === 401) onError(new Error(t('selfUsage.login.invalid')));
      else onError(caught instanceof Error ? caught : new Error(t('selfUsage.error.generic')));
    } finally {
      setPending(false);
    }
  }
  return (
    <Card className='mx-auto mt-12 w-full max-w-md'>
      <CardHeader>
        <CardTitle>{t('selfUsage.login.title')}</CardTitle>
        <p className='text-muted-foreground text-sm'>{t('selfUsage.login.description')}</p>
      </CardHeader>
      <CardContent>
        <form onSubmit={submit} className='space-y-4'>
          <Label htmlFor='self-usage-key'>{t('selfUsage.login.placeholder')}</Label>
          <Input id='self-usage-key' type='password' autoComplete='off' value={input} onChange={(event) => setInput(event.target.value)} />
          {error &&
            (error.message === t('selfUsage.login.invalid') || error.message === t('selfUsage.login.required') ? (
              <p role='alert' className='text-destructive text-sm'>
                {error.message}
              </p>
            ) : (
              <PageError error={error} />
            ))}
          <Button type='submit' disabled={pending} className='w-full'>
            {pending ? t('selfUsage.loading') : t('selfUsage.login.submit')}
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}

export function DatePicker({ meta, onChange }: { meta: SelfUsageMeta; onChange: (range: DateRange) => void }) {
  const { t } = useTranslation();
  const [preset, setPreset] = useState<DatePreset | 'custom'>('today');
  const [draft, setDraft] = useState<DateRange>(() => presetRange('today', meta.timezone));
  const [invalid, setInvalid] = useState(false);
  function applyPreset(value: DatePreset | 'custom') {
    setPreset(value);
    setInvalid(false);
    if (value !== 'custom') {
      const next = presetRange(value, meta.timezone);
      setDraft(next);
      onChange(next);
    }
  }
  return (
    <div className='space-y-3'>
      <div className='flex flex-wrap gap-2'>
        {(['today', 'yesterday', 'last7', 'last30', 'thisMonth', 'custom'] as const).map((value) => (
          <Button key={value} size='sm' variant={preset === value ? 'default' : 'outline'} onClick={() => applyPreset(value)}>
            {t(`selfUsage.range.${value}`)}
          </Button>
        ))}
      </div>
      {preset === 'custom' && (
        <div className='flex flex-wrap items-end gap-3'>
          <div className='space-y-1'>
            <Label htmlFor='usage-start'>{t('selfUsage.range.start')}</Label>
            <Input
              id='usage-start'
              type='date'
              value={draft.start}
              onChange={(event) => setDraft({ ...draft, start: event.target.value })}
            />
          </div>
          <div className='space-y-1'>
            <Label htmlFor='usage-end'>{t('selfUsage.range.end')}</Label>
            <Input id='usage-end' type='date' value={draft.end} onChange={(event) => setDraft({ ...draft, end: event.target.value })} />
          </div>
          <Button
            onClick={() => {
              if (validRange(draft, meta.maxRangeDays)) {
                onChange(draft);
                setInvalid(false);
              } else setInvalid(true);
            }}
          >
            {t('selfUsage.range.apply')}
          </Button>
          {invalid && (
            <p role='alert' className='text-destructive text-sm'>
              {t('selfUsage.error.invalidRange', { count: Math.min(meta.maxRangeDays, 90) })}
            </p>
          )}
        </div>
      )}
    </div>
  );
}

export function Summary({ usage, meta, models }: { usage: Usage; meta: SelfUsageMeta; models: { model: string; usage: Usage }[] }) {
  const { t, number, money } = usePageLabels(meta);
  const fields = [
    ['success', usage.successRequests],
    ['failed', usage.failedRequests],
    ['input', usage.inputTokens],
    ['output', usage.outputTokens],
    ['cacheRead', usage.cacheReadTokens],
    ['cacheWrite', usage.cacheWriteTokens],
    ['reasoning', usage.reasoningTokens],
    ['total', usage.totalTokens],
  ] as const;
  return (
    <section className='space-y-3'>
      <h2 className='text-xl font-semibold'>{t('selfUsage.summary.title')}</h2>
      <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5'>
        {fields.map(([label, value]) => (
          <Card key={label}>
            <CardHeader className='pb-1'>
              <CardTitle className='text-muted-foreground text-sm font-medium'>{t(`selfUsage.summary.${label}`)}</CardTitle>
            </CardHeader>
            <CardContent className='text-2xl font-semibold tabular-nums'>{number.format(value)}</CardContent>
          </Card>
        ))}
        <Card>
          <CardHeader className='pb-1'>
            <CardTitle className='text-muted-foreground text-sm font-medium'>{t('selfUsage.summary.cost')}</CardTitle>
          </CardHeader>
          <CardContent className='text-lg font-semibold'>
            <Cost value={usage.cost} unpriced={usage.unpricedRecords} money={money} />
          </CardContent>
        </Card>
        <CacheHitCard usage={usage} models={models} />
      </div>
    </section>
  );
}

export function Trend({ daily, meta }: { daily: { date: string; usage: Usage }[]; meta: SelfUsageMeta }) {
  const { t, locale, money, number } = usePageLabels(meta);
  const [metric, setMetric] = useState<'requests' | 'tokens' | 'cost'>('requests');
  const points = daily.map(({ date, usage }) => ({
    date: chartDay(date, locale),
    success: usage.successRequests,
    failed: usage.failedRequests,
    value: metric === 'tokens' ? usage.totalTokens : usage.cost,
    unpriced: usage.unpricedRecords,
  }));
  return (
    <Card>
      <CardHeader className='flex flex-row flex-wrap items-center justify-between gap-3'>
        <CardTitle>{t('selfUsage.trend.title')}</CardTitle>
        <div className='flex gap-2'>
          {(['requests', 'tokens', 'cost'] as const).map((value) => (
            <Button key={value} size='sm' variant={metric === value ? 'default' : 'outline'} onClick={() => setMetric(value)}>
              {t(`selfUsage.trend.${value}`)}
            </Button>
          ))}
        </div>
      </CardHeader>
      <CardContent>
        <div className='h-72 w-full'>
          <ResponsiveContainer width='100%' height='100%'>
            <AreaChart data={points} margin={{ top: 8, right: 12, left: 8, bottom: 0 }}>
              <CartesianGrid strokeDasharray='3 3' stroke='var(--border)' vertical={false} />
              <XAxis dataKey='date' tick={{ fontSize: 11 }} />
              <YAxis tick={{ fontSize: 11 }} width={60} />
              <Tooltip
                content={({ active, payload, label }) =>
                  active && payload?.length ? (
                    <div className='bg-background rounded border p-2 text-sm shadow'>
                      <p>{label}</p>
                      {metric === 'requests' ? (
                        <>
                          <p>
                            {t('selfUsage.summary.success')}: {number.format(payload[0].payload.success)}
                          </p>
                          <p>
                            {t('selfUsage.summary.failed')}: {number.format(payload[0].payload.failed)}
                          </p>
                        </>
                      ) : (
                        <p>
                          {metric === 'cost'
                            ? money(payload[0].payload.value as number | null, payload[0].payload.unpriced).amount
                            : number.format(Number(payload[0].value))}
                        </p>
                      )}
                      {metric === 'cost' && payload[0].payload.unpriced > 0 && (
                        <p className='text-muted-foreground'>{t('selfUsage.cost.unpriced', { count: payload[0].payload.unpriced })}</p>
                      )}
                    </div>
                  ) : null
                }
              />
              {metric === 'requests' ? (
                <>
                  <Legend />
                  <Area
                    type='monotone'
                    dot={points.length === 1}
                    dataKey='success'
                    name={t('selfUsage.summary.success')}
                    stroke='var(--chart-1)'
                    fill='var(--chart-1)'
                    fillOpacity={0.14}
                  />
                  <Area
                    type='monotone'
                    dot={points.length === 1}
                    dataKey='failed'
                    name={t('selfUsage.summary.failed')}
                    stroke='var(--chart-2)'
                    fill='var(--chart-2)'
                    fillOpacity={0.14}
                  />
                </>
              ) : (
                <Area
                  type='monotone'
                  dot={points.length === 1}
                  dataKey='value'
                  stroke='var(--primary)'
                  fill='var(--primary)'
                  fillOpacity={0.18}
                  connectNulls={false}
                />
              )}
            </AreaChart>
          </ResponsiveContainer>
        </div>
        {metric === 'cost' && (
          <div className='mt-3 grid max-h-40 grid-cols-2 gap-2 overflow-y-auto text-xs sm:grid-cols-4'>
            {daily.map(({ date, usage }) => (
              <div key={date} className='rounded border p-2'>
                <span className='text-muted-foreground block'>{chartDay(date, locale)}</span>
                <Cost value={usage.cost} unpriced={usage.unpricedRecords} money={money} />
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

export function ModelTable({ models, meta }: { models: { model: string; usage: Usage }[]; meta: SelfUsageMeta }) {
  const { t, number, money } = usePageLabels(meta);
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('selfUsage.models.title')}</CardTitle>
      </CardHeader>
      <CardContent className='overflow-x-auto'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('selfUsage.requests.model')}</TableHead>
              <TableHead>{t('selfUsage.summary.success')}</TableHead>
              <TableHead>{t('selfUsage.summary.failed')}</TableHead>
              <TableHead>{t('selfUsage.summary.total')}</TableHead>
              <TableHead>{t('selfUsage.summary.cost')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {models.map(({ model, usage }) => (
              <TableRow key={model}>
                <TableCell>{model}</TableCell>
                <TableCell>{number.format(usage.successRequests)}</TableCell>
                <TableCell>{number.format(usage.failedRequests)}</TableCell>
                <TableCell>{number.format(usage.totalTokens)}</TableCell>
                <TableCell>
                  <Cost value={usage.cost} unpriced={usage.unpricedRecords} money={money} />
                </TableCell>
              </TableRow>
            ))}
            {!models.length && (
              <TableRow>
                <TableCell colSpan={5}>{t('selfUsage.requests.empty')}</TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

export function RequestLog({
  group = false,
  keyValue,
  range,
  meta,
  models,
  onUnauthorized,
  expectedScope,
  scopeRevision,
  onScopeMismatch,
  loadRequests,
  queryScope,
}: {
  loadRequests?: (
    model: RequestStatus | string | null,
    status: RequestStatus | null,
    page: number,
    signal?: AbortSignal
  ) => ReturnType<typeof fetchRequests>;
  queryScope?: readonly unknown[];
  group?: boolean;
  keyValue: string;
  range: DateRange;
  meta: SelfUsageMeta;
  models: string[];
  onUnauthorized: () => void;
  expectedScope?: SelfUsageIdentity;
  scopeRevision: number;
  onScopeMismatch: () => void;
}) {
  const { t, locale } = usePageLabels(meta);
  const [model, setModel] = useState<string | null>(null);
  const [status, setStatus] = useState<RequestStatus | null>(null);
  const [page, setPage] = useState(1);
  const result = useQuery({
    queryKey: [...(queryScope ?? ['selfUsage']), 'requests', scopeRevision, group, range.start, range.end, model, status, page],
    queryFn: ({ signal }) =>
      loadRequests
        ? loadRequests(model, status, page, signal)
        : fetchRequests(keyValue, range.start, range.end, model, status, page, signal, group),
    staleTime: 0,
    gcTime: 0,
  });
  useEffect(() => {
    if (result.error instanceof SelfUsageError && result.error.status === 401) onUnauthorized();
  }, [result.error, onUnauthorized]);
  const scopeMismatch = !!result.data && !!expectedScope && !sameUsageScope(result.data, expectedScope);
  useEffect(() => {
    if (scopeMismatch) onScopeMismatch();
  }, [scopeMismatch, onScopeMismatch]);
  const data = scopeMismatch || !expectedScope ? undefined : result.data;
  const pages = Math.max(1, Math.ceil((data?.total ?? 0) / (data?.pageSize ?? 20)));
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('selfUsage.requests.title')}</CardTitle>
        <div className='flex flex-wrap gap-2 pt-2'>
          <Select
            value={model ?? '__all__'}
            onValueChange={(value) => {
              setModel(value === '__all__' ? null : value);
              setPage(1);
            }}
          >
            <SelectTrigger className='w-48' aria-label={t('selfUsage.requests.model')}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='__all__'>{t('selfUsage.requests.allModels')}</SelectItem>
              {models.map((value) => (
                <SelectItem key={value} value={value}>
                  {value}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select
            value={status ?? '__all__'}
            onValueChange={(value) => {
              setStatus(value === '__all__' ? null : (value as RequestStatus));
              setPage(1);
            }}
          >
            <SelectTrigger className='w-44' aria-label={t('selfUsage.requests.status')}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='__all__'>{t('selfUsage.requests.allStatuses')}</SelectItem>
              {(['SUCCESS', 'FAILED', 'PROCESSING'] as const).map((value) => (
                <SelectItem key={value} value={value}>
                  {t(`selfUsage.requests.${value.toLowerCase()}`)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </CardHeader>
      <CardContent className='space-y-4'>
        {result.error && (
          <PageError
            error={result.error}
            retry={() => {
              void result.refetch();
            }}
          />
        )}
        {(result.isPending || scopeMismatch || !expectedScope) && <p role='status'>{t('selfUsage.loading')}</p>}
        {data && (
          <>
            <div className='overflow-x-auto'>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t('common.columns.id')}</TableHead>
                    <TableHead>{t('requests.columns.caller')}</TableHead>
                    <TableHead>{t('requests.columns.model')}</TableHead>
                    <TableHead>{t('requests.columns.tokens')}</TableHead>
                    <TableHead>{t('requests.columns.readCache')}</TableHead>
                    <TableHead>{t('requests.columns.writeCache')}</TableHead>
                    <TableHead>{t('requests.columns.cost')}</TableHead>
                    <TableHead>{t('requests.columns.duration')}</TableHead>
                    <TableHead>{t('selfUsage.requests.createdAt', { timezone: meta.timezone })}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.items.map((item) => (
                    <TableRow key={item.id}>
                      <TableCell>
                        <RequestIdCell id={item.id} status={item.requestStatus} stream={item.stream} />
                      </TableCell>
                      <TableCell>{item.apiKeyName}</TableCell>
                      <TableCell>{item.model}</TableCell>
                      {item.usageRecords === 0 ? (
                        <>
                          {[0, 1, 2, 3].map((index) => (
                            <TableCell key={index}>
                              <div className='text-muted-foreground text-xs'>-</div>
                            </TableCell>
                          ))}
                        </>
                      ) : (
                        <>
                          <TableCell>
                            <TokensCell prompt={item.inputTokens} completion={item.outputTokens} reasoning={item.reasoningTokens} />
                          </TableCell>
                          <TableCell>
                            <ReadCacheCell cached={item.cacheReadTokens} prompt={item.inputTokens} />
                          </TableCell>
                          <TableCell>
                            <WriteCacheCell writeCached={item.cacheWriteTokens} prompt={item.inputTokens} />
                          </TableCell>
                          <TableCell>
                            <CostCell
                              total={item.cost}
                              currencyCode={meta.currencyCode}
                              emptyLabel='-'
                              breakdown={{
                                items: item.costItems.map((costItem) => ({
                                  image: costItem.image,
                                  imageRule: costItem.imageRule,
                                  unpriced: costItem.unpriced,
                                  itemCode: costItem.itemCode,
                                  variant: costItem.promptWriteCacheVariantCode,
                                  quantity: costItem.quantity,
                                  subtotal: costItem.subtotal,
                                  tiers: costItem.tierBreakdown,
                                })),
                                multiplier: item.costMultiplierMixed ? 'mixed' : (item.costMultiplier ?? 1),
                                unpricedRecords: item.unpricedRecords,
                              }}
                            />
                          </TableCell>
                        </>
                      )}
                      <TableCell>
                        <DurationCell
                          status={item.requestStatus}
                          stream={item.stream}
                          latencyMs={item.latencyMs}
                          firstTokenLatencyMs={item.firstTokenLatencyMs}
                        />
                      </TableCell>
                      <TableCell className='whitespace-nowrap'>
                        {new Intl.DateTimeFormat(locale, {
                          timeZone: meta.timezone,
                          year: 'numeric',
                          month: '2-digit',
                          day: '2-digit',
                          hour: '2-digit',
                          minute: '2-digit',
                          second: '2-digit',
                          hourCycle: 'h23',
                        }).format(new Date(item.createdAt))}
                      </TableCell>
                    </TableRow>
                  ))}
                  {!data.items.length && (
                    <TableRow>
                      <TableCell colSpan={9}>{t('selfUsage.requests.empty')}</TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </div>
            <div className='flex items-center justify-between gap-2 text-sm'>
              <Button variant='outline' size='sm' disabled={page <= 1} onClick={() => setPage(page - 1)}>
                {t('selfUsage.requests.previous')}
              </Button>
              <span>{t('selfUsage.requests.page', { page: data.page, pages, total: data.total })}</span>
              <Button variant='outline' size='sm' disabled={page >= pages} onClick={() => setPage(page + 1)}>
                {t('selfUsage.requests.next')}
              </Button>
            </div>
          </>
        )}
      </CardContent>
    </Card>
  );
}

function Dashboard({
  keyValue,
  meta,
  onUnauthorized,
  onIdentity,
}: {
  keyValue: string;
  meta: SelfUsageMeta;
  onUnauthorized: () => void;
  onIdentity: (identity: Pick<SelfUsageMeta, 'apiKeyName' | 'groupName'>) => void;
}) {
  const { t } = useTranslation();
  const [group, setGroup] = useState(false);
  // A log-only interaction can discover reassignment before the summary does.
  // Advance both queries together so old totals and a new group log cannot mix.
  const [scopeRevision, setScopeRevision] = useState(0);
  const syncScope = useCallback(() => setScopeRevision((value) => value + 1), []);
  const [range, setRange] = useState<DateRange>(() => presetRange('today', meta.timezone));
  const stats = useQuery({
    queryKey: ['selfUsage', 'stats', scopeRevision, group, range.start, range.end],
    queryFn: ({ signal }) => fetchStats(keyValue, range.start, range.end, signal, group),
    // Membership can change while this page is open; do not reuse a previous scope after toggling.
    staleTime: 0,
    gcTime: 0,
  });
  useEffect(() => {
    if (stats.data) onIdentity(stats.data);
  }, [stats.data, onIdentity]);
  useEffect(() => {
    if (stats.error instanceof SelfUsageError && stats.error.status === 401) onUnauthorized();
  }, [stats.error, onUnauthorized]);
  return (
    <div className='space-y-6'>
      <SelfUsageBudgets keyValue={keyValue} meta={meta} onUnauthorized={onUnauthorized} />
      {meta.groupName && (
        <div className='flex items-center gap-2'>
          <Button variant={group ? 'outline' : 'default'} onClick={() => setGroup(false)}>
            {t('apikeys.groups.current')}
          </Button>
          <Button variant={group ? 'default' : 'outline'} onClick={() => setGroup(true)}>
            {t('apikeys.groups.scope')}: {meta.groupName}
          </Button>
        </div>
      )}
      <DatePicker meta={meta} onChange={setRange} />
      {stats.isPending && <p role='status'>{t('selfUsage.loading')}</p>}
      {stats.error && (
        <PageError
          error={stats.error}
          retry={() => {
            void stats.refetch();
          }}
        />
      )}
      {stats.data && (
        <>
          <Summary usage={stats.data.totals} meta={meta} models={stats.data.models} />
          <Trend daily={stats.data.daily} meta={meta} />
          <ModelTable models={stats.data.models} meta={meta} />
        </>
      )}
      <RequestLog
        key={`${group}:${range.start}:${range.end}`}
        group={group}
        keyValue={keyValue}
        range={range}
        meta={meta}
        models={stats.data?.models.map((item) => item.model) ?? []}
        expectedScope={stats.data}
        scopeRevision={scopeRevision}
        onScopeMismatch={syncScope}
        onUnauthorized={onUnauthorized}
      />
    </div>
  );
}

function PageContent() {
  const { t, i18n } = useTranslation();
  useEffect(() => {
    document.title = `${t('selfUsage.title')} · AxonHub`;
  }, [t, i18n.language]);
  const [keyValue, setKeyValue] = useState<string | null>(getPageKey);
  const [verifiedMeta, setVerifiedMeta] = useState<SelfUsageMeta | null>(null);
  const [loginError, setLoginError] = useState<Error | null>(null);
  const meta = useQuery({
    queryKey: ['selfUsage', 'meta'],
    queryFn: ({ signal }) => fetchMeta(keyValue!, signal),
    enabled: keyValue !== null && verifiedMeta === null,
  });
  function logout() {
    selfUsageQueryClient.clear();
    setPageKey(null);
    setKeyValue(null);
    setVerifiedMeta(null);
    setLoginError(null);
  }
  useEffect(() => {
    if (meta.error instanceof SelfUsageError && meta.error.status === 401) logout();
  }, [meta.error]);
  const activeMeta = verifiedMeta ?? meta.data;
  const updateIdentity = useCallback(
    (identity: Pick<SelfUsageMeta, 'apiKeyName' | 'groupName'>) => {
      setVerifiedMeta((previous) => {
        const current = previous ?? meta.data;
        if (!current || (current.apiKeyName === identity.apiKeyName && current.groupName === identity.groupName)) return previous;
        return { ...current, apiKeyName: identity.apiKeyName, groupName: identity.groupName };
      });
    },
    [meta.data]
  );
  return (
    <main className='bg-background text-foreground min-h-screen'>
      <div className='mx-auto max-w-7xl space-y-6 px-4 py-8 sm:px-6'>
        <header className='flex flex-wrap items-center justify-between gap-3 border-b pb-5'>
          <div>
            <h1 className='text-2xl font-semibold'>{t('selfUsage.title')}</h1>
            <p className='text-muted-foreground text-sm'>
              {activeMeta
                ? `${activeMeta.apiKeyName} · ${t('selfUsage.timezone', { timezone: activeMeta.timezone })}`
                : t('selfUsage.subtitle')}
            </p>
          </div>
          <div className='flex items-center gap-2'>
            <Button
              variant='outline'
              size='sm'
              aria-label={t('selfUsage.language')}
              onClick={() => {
                void i18n.changeLanguage(i18n.language.startsWith('zh') ? 'en' : 'zh-CN');
              }}
            >
              {i18n.language.startsWith('zh') ? 'English' : '中文'}
            </Button>
            <ThemeSwitch />
            {keyValue && (
              <Button variant='outline' size='sm' onClick={logout}>
                {t('selfUsage.logout')}
              </Button>
            )}
          </div>
        </header>
        {!keyValue ? (
          <Login
            error={loginError}
            onError={setLoginError}
            onLogin={(key, value) => {
              selfUsageQueryClient.clear();
              setPageKey(key);
              setVerifiedMeta(value);
              setKeyValue(key);
              setLoginError(null);
            }}
          />
        ) : !activeMeta ? (
          meta.error ? (
            <PageError
              error={meta.error}
              retry={() => {
                void meta.refetch();
              }}
            />
          ) : (
            <p role='status'>{t('selfUsage.loading')}</p>
          )
        ) : (
          <Dashboard key={keyValue} keyValue={keyValue} meta={activeMeta} onUnauthorized={logout} onIdentity={updateIdentity} />
        )}
      </div>
    </main>
  );
}

export function SelfUsagePage() {
  return (
    <QueryClientProvider client={selfUsageQueryClient}>
      <PageContent />
    </QueryClientProvider>
  );
}
