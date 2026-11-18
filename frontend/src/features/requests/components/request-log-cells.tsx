import { useTranslation } from 'react-i18next';
import { extractNumberID } from '@/lib/utils';
import { formatDuration } from '@/utils/format-duration';
import { Badge } from '@/components/ui/badge';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { getProviderFromChannelType } from '@/features/channels/data/config_providers';
import type { Request } from '../data/schema';
import { getTokensPerSecondValue } from '../utils/tokens-per-second';
import { getStatusColor } from './help';
import { RequestChannelDisplayCell } from './request-channel-display-cell';

export type RequestDiagnosticTab = 'request' | 'response' | 'routing' | 'errors';

export function finalRequestChannel(request: Request) {
  const execution = request.sessionExecution ?? request.executions?.edges[0]?.node;
  const channel = request.sessionExecution ? request.sessionExecution.channel : (execution?.channel ?? request.channel);
  return { execution, channel };
}

export function requestProvider(request: Request) {
  const { execution, channel } = finalRequestChannel(request);
  return execution?.selection?.provider || (channel?.type ? (getProviderFromChannelType(channel.type) ?? channel.type) : undefined);
}

export function RequestLogStatusCell({
  request,
  onOpen,
  onToggleContinuation,
  continuationExpanded = false,
  continued = false,
}: {
  request: Pick<Request, 'id' | 'status' | 'clientStatusCode' | 'resultExecution' | 'stream' | 'continuationCount'> & {
    executions?: { edges: { node?: { responseStatusCode?: number | null } | null }[]; totalCount?: number };
  };
  onOpen?: (tab: RequestDiagnosticTab) => void;
  onToggleContinuation?: () => void;
  continuationExpanded?: boolean;
  continued?: boolean;
}) {
  const { t } = useTranslation();
  const status = request.clientStatusCode ?? request.resultExecution?.responseStatusCode;
  const retries = Math.max((request.executions?.totalCount ?? 0) - 1, 0);
  return (
    <div className='flex min-w-0 flex-col gap-1.5'>
      <div className='flex min-w-0 items-center gap-2'>
        {onOpen ? (
          <button
            type='button'
            onClick={() => onOpen('request')}
            title={`#${extractNumberID(request.id)}`}
            className='text-primary min-w-0 truncate font-mono text-xs hover:underline'
          >
            #{extractNumberID(request.id)}
          </button>
        ) : (
          <span className='text-primary font-mono text-xs'>#{extractNumberID(request.id)}</span>
        )}
        <span className='text-muted-foreground shrink-0 text-xs'>
          {t(request.stream ? 'requests.stream.streaming' : 'requests.stream.nonStreaming')}
        </span>
        {!!request.continuationCount && onToggleContinuation && (
          <button
            type='button'
            className='text-muted-foreground shrink-0 text-xs hover:underline'
            onClick={onToggleContinuation}
            aria-expanded={continuationExpanded}
            title={t('requests.continuation.count', { count: request.continuationCount })}
          >
            {t('requests.continuation.count', { count: request.continuationCount })}
          </button>
        )}
      </div>
      <div className='flex items-center gap-2 whitespace-nowrap'>
        <Badge
          className={continued ? 'bg-muted text-muted-foreground' : getStatusColor(request.status)}
          title={continued ? t('requests.continuation.canceled') : undefined}
          asChild={!!onOpen}
        >
          {onOpen ? (
            <button type='button' onClick={() => onOpen('errors')}>
              {t(`requests.status.${request.status}`)}
              {status != null ? ` · ${status}` : ''}
            </button>
          ) : (
            <span>
              {t(`requests.status.${request.status}`)}
              {status != null ? ` · ${status}` : ''}
            </span>
          )}
        </Badge>
        {retries > 0 && onOpen && (
          <button type='button' className='text-muted-foreground text-xs hover:underline' onClick={() => onOpen('routing')}>
            {t('requests.diagnostics.retries', { count: retries })}
          </button>
        )}
      </div>
    </div>
  );
}

export function RequestChannelCell({ request, onOpen }: { request: Request; onOpen: () => void }) {
  const { execution, channel } = finalRequestChannel(request);
  const selection = execution?.selection;
  const usage = request.usageSummary;
  const multiplier = execution?.costSnapshot
    ? usage?.totalCost != null
      ? (usage.costPriceMultiplier ?? 'mixed')
      : execution.costSnapshot.applied.value
    : undefined;
  return (
    <RequestChannelDisplayCell
      channel={{
        name: selection?.channelName || channel?.name,
        provider: requestProvider(request),
        tags: channel?.tags ?? [],
        sessionState: selection?.sessionState,
        multiplier,
      }}
      credentials={{ keyHint: selection?.keyHint, keyRemark: selection?.keyRemark }}
      onOpen={onOpen}
    />
  );
}

type RequestCacheUsage = Pick<
  NonNullable<Request['usageSummary']>,
  'promptTokens' | 'promptCachedTokens' | 'promptWriteCachedTokens' | 'promptWriteCachedTokens5m' | 'promptWriteCachedTokens1h'
>;

export function RequestCacheCell({ request }: { request: { usageSummary?: RequestCacheUsage | null } }) {
  const { t } = useTranslation();
  const usage = request.usageSummary;
  if (!usage) return <span className='text-muted-foreground'>—</span>;
  const read = usage.promptCachedTokens ?? 0,
    write = usage.promptWriteCachedTokens ?? 0,
    input = usage.promptTokens ?? 0;
  const rate = input > 0 ? `${((read / input) * 100).toFixed(1)}%` : '—';
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <div tabIndex={0} className='flex min-w-[125px] flex-col gap-1 text-xs whitespace-nowrap'>
          <span>
            {t('requests.columns.read')} {read.toLocaleString()} · {t('requests.columns.write')} {write.toLocaleString()}
          </span>
          <span className='text-muted-foreground'>{t('requests.diagnostics.hitRate', { rate })}</span>
        </div>
      </TooltipTrigger>
      <TooltipContent>
        <div className='flex flex-col gap-1 text-xs'>
          <p>
            {t('requests.columns.readCache')}: {read.toLocaleString()}
          </p>
          <p>
            {t('requests.columns.writeCache')}: {write.toLocaleString()}
          </p>
          <p>
            {t('requests.diagnostics.cache5m')}: {usage.promptWriteCachedTokens5m?.toLocaleString() ?? '—'}
          </p>
          <p>
            {t('requests.diagnostics.cache1h')}: {usage.promptWriteCachedTokens1h?.toLocaleString() ?? '—'}
          </p>
          <p>{t('requests.diagnostics.hitRateFormula')}</p>
        </div>
      </TooltipContent>
    </Tooltip>
  );
}

const latencyColors = {
  good: { text: 'text-emerald-700 dark:text-emerald-400', bar: 'var(--color-emerald-500)' },
  warn: { text: 'text-amber-700 dark:text-amber-400', bar: 'var(--color-amber-400)' },
  slow: { text: 'text-orange-700 dark:text-orange-400', bar: 'var(--color-orange-500)' },
  critical: { text: 'text-red-700 dark:text-red-400', bar: 'var(--color-red-500)' },
};

function latencyColor(ms: number | null | undefined, metric: 'firstToken' | 'total') {
  if (ms == null) return undefined;
  const [warn, slow, critical] = metric === 'firstToken' ? [10_000, 30_000, 60_000] : [60_000, 180_000, 300_000];
  if (ms >= critical) return latencyColors.critical;
  if (ms >= slow) return latencyColors.slow;
  if (ms >= warn) return latencyColors.warn;
  return latencyColors.good;
}

export function RequestDurationCell({
  request,
}: {
  request: Pick<Request, 'stream' | 'metricsLatencyMs' | 'metricsFirstTokenLatencyMs' | 'outputTokensPerSecond'>;
}) {
  const { t } = useTranslation();
  const latency = request.metricsLatencyMs;
  const first = request.stream ? request.metricsFirstTokenLatencyMs : null;
  const speed = getTokensPerSecondValue(request);
  const firstColor = latencyColor(first, 'firstToken');
  const totalColor = latencyColor(latency, 'total');
  const bar = firstColor && totalColor
    ? `linear-gradient(to bottom, ${firstColor.bar} 40%, ${totalColor.bar} 60%)`
    : (firstColor ?? totalColor)?.bar;

  return (
    <div className='flex min-w-[150px] gap-2 text-xs whitespace-nowrap'>
      <span aria-hidden='true' className='w-1 shrink-0 rounded-full' style={{ background: bar }} />
      <dl className='text-muted-foreground grid grid-cols-[max-content_max-content] items-baseline gap-x-2 gap-y-0.5 tabular-nums'>
        <dt title={t('requests.duration.firstTokenHelp')}>{t('requests.duration.firstTokenLabel')}</dt>
        <dd title={t('requests.duration.firstTokenHelp')} className={firstColor && `font-medium ${firstColor.text}`}>{first == null ? '—' : formatDuration(first)}</dd>
        <dt>{t('requests.duration.totalLabel')}</dt>
        <dd className={totalColor && `font-medium ${totalColor.text}`}>{latency == null ? '—' : formatDuration(latency)}</dd>
        <dt title={t('consumption.speedHelp')}>{t('requests.duration.outputRate')}</dt>
        <dd title={t('consumption.speedHelp')} className={speed == null ? undefined : 'text-foreground'}>{speed == null ? '—' : `${Math.round(speed)} tok/s`}</dd>
      </dl>
    </div>
  );
}
