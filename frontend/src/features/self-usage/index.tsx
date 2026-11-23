import { useCallback, useEffect, useState, type FormEvent } from 'react';
import { QueryClientProvider, useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Area, AreaChart, CartesianGrid, Legend, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import { currencyFractionDigits } from '@/utils/currency-display';
import { formatNumber } from '@/utils/format-number';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import {
  fetchMeta,
  fetchStats,
  SelfUsageError,
  type SelfUsageDisplayMeta as SelfUsageMeta,
  type Usage,
} from './api';
import { SelfUsageBudgets } from './components/budgets';
import { CacheHitCard } from './components/cache-hit-card';
import { CompactTokens } from './components/compact-tokens';
import { Cost } from './components/cost';
import { PageError } from './components/page-error';
import { UsagePageShell } from './components/page-shell';
import { RequestLog } from './components/request-log';
import { displayCost } from './cost/format';
import { selfUsageQueryClient } from './query-client';
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
      (val) =>
        t('currencies.format', { val, currency: meta.currencyCode, locale, ...currencyFractionDigits(meta.currencyDisplayDecimals, 6) }),
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
  const [preset, setPreset] = useState<DatePreset | 'custom'>('last7');
  const [draft, setDraft] = useState<DateRange>(() => presetRange('last7', meta.timezone));
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
            <CardContent className='text-2xl font-semibold tabular-nums'>
              {label === 'success' || label === 'failed' ? number.format(value) : <CompactTokens value={value} />}
            </CardContent>
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
              <YAxis
                tick={{ fontSize: 11 }}
                width={60}
                tickFormatter={metric === 'tokens' ? (value) => formatNumber(value, { digits: 2 }) : undefined}
              />
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
              <TableHead>{t('selfUsage.models.cacheHitRate')}</TableHead>
              <TableHead>{t('selfUsage.summary.cost')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {models.map(({ model, usage }) => (
              <TableRow key={model}>
                <TableCell>{model}</TableCell>
                <TableCell>{number.format(usage.successRequests)}</TableCell>
                <TableCell>{number.format(usage.failedRequests)}</TableCell>
                <TableCell>
                  <CompactTokens value={usage.totalTokens} />
                </TableCell>
                <TableCell
                  title={`${t('selfUsage.cacheHit.formula')} (${number.format(usage.cacheReadTokens)} / ${number.format(usage.inputTokens)})`}
                >
                  {usage.inputTokens > 0 ? `${((usage.cacheReadTokens / usage.inputTokens) * 100).toFixed(1)}%` : '—'}
                </TableCell>
                <TableCell>
                  <Cost value={usage.cost} unpriced={usage.unpricedRecords} money={money} />
                </TableCell>
              </TableRow>
            ))}
            {!models.length && (
              <TableRow>
                <TableCell colSpan={6}>{t('selfUsage.requests.empty')}</TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
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
  const [sessionID, setSessionID] = useState('');
  // A log-only interaction can discover reassignment before the summary does.
  // Advance both queries together so old totals and a new group log cannot mix.
  const [scopeRevision, setScopeRevision] = useState(0);
  const syncScope = useCallback(() => setScopeRevision((value) => value + 1), []);
  const [range, setRange] = useState<DateRange>(() => presetRange('last7', meta.timezone));
  const stats = useQuery({
    queryKey: ['selfUsage', 'stats', scopeRevision, group, range.start, range.end, sessionID],
    queryFn: ({ signal }) => fetchStats(keyValue, range.start, range.end, signal, group, sessionID || undefined),
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
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
      {sessionID && <p className='text-muted-foreground break-all text-sm'>{t('requests.session.selected', { id: sessionID })}</p>}
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
        sessionID={sessionID}
        onSessionFilter={setSessionID}
        keyValue={keyValue}
        range={range}
        meta={meta}
        models={stats.data?.models.map((item) => item.model) ?? []}
        refreshStats={stats.refetch}
        expectedScope={stats.data}
        scopeRevision={scopeRevision}
        onScopeMismatch={syncScope}
        onUnauthorized={onUnauthorized}
      />
    </div>
  );
}

function PageContent() {
  const { t } = useTranslation();
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
    <UsagePageShell meta={activeMeta} onLogout={keyValue ? logout : undefined}>
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
    </UsagePageShell>
  );
}

export function SelfUsagePage() {
  return (
    <QueryClientProvider client={selfUsageQueryClient}>
      <PageContent />
    </QueryClientProvider>
  );
}
