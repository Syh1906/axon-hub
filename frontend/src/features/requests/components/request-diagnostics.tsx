import { useEffect, useState } from 'react';
import { AlertCircle, CheckCircle2, Copy } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { copyTextToClipboard } from '@/lib/clipboard';
import { extractNumberID } from '@/lib/utils';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Separator } from '@/components/ui/separator';
import { useRequestExecutions } from '../data';
import type { Request, RequestExecution } from '../data/schema';
import { getStatusColor } from './help';

function knownFailureKind(execution: RequestExecution) {
  const codes = [execution.failure?.code, execution.failure?.type];
  if (
    codes.some((code) =>
      ['insufficient_quota', 'quota_exceeded', 'billing_hard_limit_reached', 'credit_balance_too_low'].includes(code ?? '')
    )
  )
    return 'quota';
  if (codes.some((code) => ['rate_limit_exceeded', 'rate_limit_error', 'too_many_requests'].includes(code ?? ''))) return 'rateLimit';
  return 'generic';
}

export function RequestDiagnostics({
  request,
  mode,
  projectId,
  onModeChange,
}: {
  request: Request;
  mode: 'routing' | 'errors';
  projectId?: string | null;
  onModeChange: (mode: 'routing' | 'errors') => void;
}) {
  const { t } = useTranslation();
  const [after, setAfter] = useState<string>();
  const [previous, setPrevious] = useState<RequestExecution[]>([]);
  const [selectedID, setSelectedID] = useState('');
  const query = useRequestExecutions(request.id, { first: 50, after, orderBy: { field: 'CREATED_AT', direction: 'DESC' } }, { projectId });
  const executions = [...previous, ...(query.data?.edges.map((edge) => edge.node) ?? [])];
  const selected =
    executions.find((execution) => execution.id === selectedID) ??
    (mode === 'errors' ? executions.find((execution) => execution.status === 'failed' || execution.status === 'canceled') : undefined) ??
    executions[0];
  useEffect(() => {
    if (!selectedID && selected) setSelectedID(selected.id);
  }, [selectedID, selected]);
  const total = query.data?.totalCount ?? executions.length;
  const unknown = t('requests.diagnostics.unrecorded');
  const providerName = (provider?: string) => (provider ? t(`channels.providers.${provider}`, { defaultValue: provider }) : unknown);
  const channelName = (execution: RequestExecution) =>
    [
      execution.selection?.channelName || execution.channel?.name || unknown,
      providerName(execution.selection?.provider || execution.channel?.type),
    ].join(' · ');
  const decisionName = (value?: string | null) =>
    value ? t(`requests.diagnostics.decisions.${value}`, { defaultValue: unknown }) : unknown;
  const copy = async (value: string) => {
    try {
      await copyTextToClipboard(value);
      toast.success(t('requests.actions.copied'));
    } catch {
      toast.error(t('common.errors.copyFailed'));
    }
  };
  useEffect(() => {
    if (!query.data || request.status === 'completed' || request.status === 'failed' || request.status === 'canceled') return;
    const timer = setInterval(() => {
      void query.refetch();
    }, 3000);
    return () => clearInterval(timer);
  }, [request.status, query.refetch, query.data]);

  if (query.isPending && !executions.length) return <p role='status'>{t('common.loading')}</p>;
  if (query.error)
    return (
      <Alert variant='destructive'>
        <AlertCircle />
        <AlertTitle>{t('requests.diagnostics.loadFailed')}</AlertTitle>
        <AlertDescription>
          {query.error.message}
          <Button variant='outline' size='sm' onClick={() => void query.refetch()}>
            {t('requests.diagnostics.retry')}
          </Button>
        </AlertDescription>
      </Alert>
    );
  if (!selected) return <p className='text-muted-foreground'>{t('requests.diagnostics.noAttempts')}</p>;
  const failed = selected.status === 'failed' || selected.status === 'canceled';
  const kind = knownFailureKind(selected);
  const selection = selected.selection;
  const ordinal = (execution: RequestExecution) => total - executions.findIndex((item) => item.id === execution.id);
  const diagnosticText = JSON.stringify(
    {
      requestID: extractNumberID(request.id),
      finalStatus: request.status,
      clientStatusCode: request.clientStatusCode ?? null,
      execution: {
        attempt: ordinal(selected),
        channel: channelName(selected),
        key: [selection?.keyRemark, selection?.keyHint].filter(Boolean).join(' · ') || unknown,
        session: selection?.sessionState ?? null,
        source: selected.failure?.source ?? null,
        upstreamStatusCode: selected.responseStatusCode ?? null,
        errorCode: selected.failure?.code ?? null,
        errorMessage: selected.errorMessage,
        response: selected.failure?.body ?? null,
        requestURL: selected.requestURL,
        retryDecision: selected.retryDecision ?? null,
      },
    },
    null,
    2
  );

  return (
    <div className='flex flex-col gap-5'>
      <Alert variant={failed ? 'destructive' : 'default'}>
        {failed ? <AlertCircle /> : <CheckCircle2 />}
        <AlertTitle className='line-clamp-none'>
          {failed ? t(`requests.diagnostics.failure.${kind}`) : t(`requests.status.${selected.status}`)}
        </AlertTitle>
        <AlertDescription>
          <p className='break-words'>
            {failed ? selected.errorMessage || t('requests.diagnostics.noMessage') : t('requests.diagnostics.attemptCompleted')}
          </p>
          <p>{t('requests.diagnostics.finalResult', { status: t(`requests.status.${request.status}`), count: total })}</p>
        </AlertDescription>
      </Alert>

      {mode === 'routing' ? (
        <>
          <div className='flex flex-col gap-2'>
            {executions.toReversed().map((execution) => (
              <button
                key={execution.id}
                type='button'
                onClick={() => {
                  setSelectedID(execution.id);
                  onModeChange('errors');
                }}
                className='hover:bg-accent flex items-start gap-3 rounded-lg border p-3 text-left'
              >
                <Badge variant='outline' className='shrink-0'>
                  {ordinal(execution)}
                </Badge>
                <div className='flex min-w-0 flex-1 flex-col gap-1'>
                  <strong className='text-sm break-words'>{channelName(execution)}</strong>
                  <span className='text-muted-foreground text-xs break-all'>
                    {execution.modelID} ·{' '}
                    {[execution.selection?.keyRemark, execution.selection?.keyHint].filter(Boolean).join(' · ') || unknown}
                  </span>
                  <span className='text-muted-foreground text-xs'>{decisionName(execution.retryDecision)}</span>
                  {execution.errorMessage && (
                    <span className='text-destructive line-clamp-2 text-xs break-all'>{execution.errorMessage}</span>
                  )}
                </div>
                <Badge className={`${getStatusColor(execution.status)} shrink-0`}>
                  {t(`requests.status.${execution.status}`)}
                  {execution.responseStatusCode ? ` · ${execution.responseStatusCode}` : ''}
                </Badge>
              </button>
            ))}
          </div>
          {selection && (
            <section className='flex flex-col gap-2'>
              {selection.upstreamTransport && (
                <p className='text-xs'>
                  {t('requests.diagnostics.clientTransport')}: {selection.clientTransport === 'websocket' ? 'WebSocket' : 'HTTP(S)'}
                  {' → '}{t('requests.diagnostics.upstreamTransport')}: {selection.upstreamTransport === 'websocket' ? 'WebSocket' : 'HTTP(S)'}
                  {selection.transportFallbackReason && <> · {t(`requests.diagnostics.transportReasons.${selection.transportFallbackReason}`, { defaultValue: selection.transportFallbackReason })}</>}
                </p>
              )}
              <h3 className='text-sm font-semibold'>{t('requests.diagnostics.selectionTitle')}</h3>
              <p className='text-muted-foreground text-sm'>
                {t('requests.diagnostics.selectionReason', {
                  strategy: t(
                    `system.retry.loadBalancerStrategy.options.${({ round_robin: 'roundRobin', circuit_breaker: 'circuitBreaker', health_gated: 'healthGated' } as Record<string, string>)[selection.strategy || ''] || selection.strategy}`,
                    { defaultValue: selection.strategy || unknown }
                  ),
                  priority: selection.priority,
                })}
              </p>
              <p className='text-xs'>
                {t('requests.diagnostics.session')}:{' '}
                {t(`requests.diagnostics.sessions.${selection.sessionState}`, { defaultValue: unknown })}
              </p>
              <div className='flex flex-col gap-1'>
                {selection.candidates?.map((candidate, index) => (
                  <div key={`${candidate.channelID}-${index}`} className='flex gap-2 rounded border px-3 py-2 text-xs'>
                    <span>{index + 1}.</span>
                    <div className='min-w-0'>
                      <span className='font-medium break-words'>
                        {candidate.channelName} · {providerName(candidate.provider)}
                      </span>
                      <p className='text-muted-foreground break-all'>
                        {candidate.models.join(', ')} · {t('requests.diagnostics.priority', { priority: candidate.priority })}
                        {candidate.sessionReuse ? ` · ${t('requests.diagnostics.sessions.reused')}` : ''}
                      </p>
                    </div>
                  </div>
                ))}
              </div>
            </section>
          )}
          {!!request.routingDecision?.skipped.length && (
            <section className='flex flex-col gap-2'>
              <h3 className='text-sm font-semibold'>{t('requests.diagnostics.skipped')}</h3>
              {request.routingDecision.skipped.map((item, index) => (
                <p key={index} className='text-muted-foreground text-xs break-words'>
                  {item.channelName} · {item.actualModel} ·{' '}
                  {t(`channels.healthGate.states.${item.state}`, { defaultValue: item.state || unknown })}
                </p>
              ))}
            </section>
          )}
        </>
      ) : (
        <>
          <div className='flex items-center gap-3'>
            <span className='shrink-0 text-sm'>{t('requests.diagnostics.selectAttempt')}</span>
            <Select value={selected.id} onValueChange={setSelectedID}>
              <SelectTrigger className='min-w-0 flex-1'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {executions.map((execution) => (
                    <SelectItem key={execution.id} value={execution.id}>
                      {t('requests.diagnostics.attempt', { count: ordinal(execution) })}
                      {ordinal(execution) === total ? ` (${t('requests.diagnostics.final')})` : ''} ·{' '}
                      {t(`requests.status.${execution.status}`)}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>
          <dl className='grid grid-cols-1 gap-x-6 gap-y-3 text-sm sm:grid-cols-2'>
            {[
              [
                t('requests.diagnostics.source'),
                selected.failure?.source
                  ? t(`requests.diagnostics.sources.${selected.failure.source}`, { defaultValue: unknown })
                  : unknown,
              ],
              [t('requests.columns.channel'), channelName(selected)],
              [
                t('requests.diagnostics.upstreamStatus'),
                selected.failure && !['upstream_http', 'upstream_stream'].includes(selected.failure.source)
                  ? unknown
                  : selected.responseStatusCode != null
                    ? `HTTP ${selected.responseStatusCode}`
                    : unknown,
              ],
              [t('requests.diagnostics.key'), [selection?.keyRemark, selection?.keyHint].filter(Boolean).join(' · ') || unknown],
              [t('requests.diagnostics.clientStatus'), request.clientStatusCode ? `HTTP ${request.clientStatusCode}` : unknown],
              [
                t('requests.diagnostics.session'),
                t(`requests.diagnostics.sessions.${selection?.sessionState || 'unrecorded'}`, { defaultValue: unknown }),
              ],
              [t('requests.diagnostics.code'), selected.failure?.code || selected.failure?.type || unknown],
              [t('requests.diagnostics.decision'), decisionName(selected.retryDecision)],
            ].map(([label, value]) => (
              <div key={label} className='flex min-w-0 flex-col gap-1'>
                <dt className='text-muted-foreground'>{label}</dt>
                <dd className='break-all'>{value}</dd>
              </div>
            ))}
          </dl>
          <Separator />
          <section className='flex flex-col gap-2'>
            <h3 className='text-sm font-medium'>{t('requests.diagnostics.url')}</h3>
            <div className='bg-muted flex items-start gap-2 rounded-md border p-3'>
              <code className='min-w-0 flex-1 text-xs break-all'>{selected.requestURL || unknown}</code>
              {selected.requestURL && (
                <Button
                  variant='ghost'
                  size='icon-sm'
                  aria-label={t('requests.actions.copy')}
                  onClick={() => void copy(selected.requestURL!)}
                >
                  <Copy />
                </Button>
              )}
            </div>
          </section>
          <section className='flex flex-col gap-2'>
            <div className='flex items-center justify-between'>
              <h3 className='text-sm font-medium'>{t('requests.diagnostics.rawError')}</h3>
              {selected.failure?.body && (
                <Button
                  variant='ghost'
                  size='icon-sm'
                  aria-label={t('requests.actions.copy')}
                  onClick={() => void copy(selected.failure!.body!)}
                >
                  <Copy />
                </Button>
              )}
            </div>
            <pre className='bg-muted max-h-72 overflow-auto rounded-md border p-3 text-xs break-all whitespace-pre-wrap'>
              {selected.failure?.body || t('requests.diagnostics.bodyMissing')}
            </pre>
            {selected.failure?.bodyTruncated && <p className='text-muted-foreground text-xs'>{t('requests.diagnostics.bodyTruncated')}</p>}
          </section>
          {failed && kind !== 'generic' && (
            <Alert>
              <AlertCircle />
              <AlertDescription>{t(`requests.diagnostics.hints.${kind}`)}</AlertDescription>
            </Alert>
          )}
          <div className='flex flex-wrap justify-end gap-2'>
            <Button variant='outline' onClick={() => onModeChange('routing')}>
              {t('requests.diagnostics.routing')}
            </Button>
            <Button onClick={() => void copy(diagnosticText)}>
              <Copy />
              {t('requests.diagnostics.copyDiagnostic')}
            </Button>
          </div>
        </>
      )}
      {query.data?.pageInfo.hasNextPage && (
        <Button
          variant='outline'
          disabled={query.isFetching}
          onClick={() => {
            setPrevious(executions);
            setAfter(query.data!.pageInfo.endCursor ?? undefined);
          }}
        >
          {t('requests.diagnostics.loadEarlier')}
        </Button>
      )}
    </div>
  );
}
