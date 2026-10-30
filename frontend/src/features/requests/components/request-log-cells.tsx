import { ArrowLeftRight, CirclePlus, Link2, CircleHelp, Unlink } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { extractNumberID } from '@/lib/utils';
import { formatDuration } from '@/utils/format-duration';
import { Badge } from '@/components/ui/badge';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { getProviderFromChannelType } from '@/features/channels/data/config_providers';
import type { Request } from '../data/schema';
import { getTokensPerSecondValue } from '../utils/tokens-per-second';
import { getStatusColor } from './help';

export type RequestDiagnosticTab = 'request' | 'response' | 'routing' | 'errors';

export function finalRequestChannel(request: Request) {
  const execution = request.executions?.edges[0]?.node;
  return { execution, channel: execution?.channel ?? request.channel };
}

export function requestProvider(request: Request) {
  const { execution, channel } = finalRequestChannel(request);
  return execution?.selection?.provider || (channel?.type ? (getProviderFromChannelType(channel.type) ?? channel.type) : undefined);
}

export function RequestLogStatusCell({ request, onOpen }: { request: Request; onOpen: (tab: RequestDiagnosticTab) => void }) {
  const { t } = useTranslation();
  const execution = request.executions?.edges[0]?.node;
  const status = request.clientStatusCode ?? execution?.responseStatusCode;
  const retries = Math.max((request.executions?.totalCount ?? 0) - 1, 0);
  return (
    <div className='flex min-w-[150px] flex-col gap-1.5'>
      <div className='flex items-center gap-2'>
        <button type='button' onClick={() => onOpen('request')} className='text-primary font-mono text-xs hover:underline'>
          #{extractNumberID(request.id)}
        </button>
        <span className='text-muted-foreground text-xs'>
          {t(request.stream ? 'requests.stream.streaming' : 'requests.stream.nonStreaming')}
        </span>
      </div>
      <div className='flex items-center gap-2 whitespace-nowrap'>
        <Badge className={getStatusColor(request.status)} asChild>
          <button
            type='button'
            onClick={() =>
              onOpen(request.status === 'failed' || request.status === 'canceled' ? 'errors' : retries ? 'routing' : 'response')
            }
          >
            {t(`requests.status.${request.status}`)}
            {status != null ? ` · ${status}` : ''}
          </button>
        </Badge>
        {retries > 0 && (
          <button type='button' className='text-muted-foreground text-xs hover:underline' onClick={() => onOpen('routing')}>
            {t('requests.diagnostics.retries', { count: retries })}
          </button>
        )}
      </div>
    </div>
  );
}

const SESSION_ICONS = { new: CirclePlus, reused: Link2, switched: ArrowLeftRight, disabled: Unlink, unrecorded: CircleHelp };

export function RequestChannelCell({ request, onOpen }: { request: Request; onOpen: () => void }) {
  const { t } = useTranslation();
  const { execution, channel } = finalRequestChannel(request);
  const selection = execution?.selection;
  const name = selection?.channelName || channel?.name;
  if (!name) return <span className='text-muted-foreground'>—</span>;
  const tags = channel?.tags ?? [];
  const state = selection?.sessionState || 'unrecorded';
  const Icon = SESSION_ICONS[state as keyof typeof SESSION_ICONS] ?? CircleHelp;
  const iconColor =
    state === 'new' ? 'text-green-600 dark:text-green-400' : state === 'reused' ? 'text-blue-600 dark:text-blue-400' : 'text-primary';
  const provider = requestProvider(request);
  const usage = request.usageLogs?.edges?.[0]?.node;
  const multiplier = execution?.costSnapshot
    ? usage?.totalCost != null
      ? (usage.costPriceMultiplier ?? 'mixed')
      : execution.costSnapshot.applied.value
    : undefined;
  const multiplierLabel = multiplier === 'mixed' ? t('requests.diagnostics.mixedMultiplier') : multiplier == null ? '—' : `×${multiplier}`;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type='button'
          onClick={onOpen}
          className='flex w-[170px] min-w-0 flex-col gap-1.5 rounded-md text-left'
          aria-label={t('requests.diagnostics.openRouting', { name })}
        >
          <span className='flex w-full items-center gap-1.5'>
            <Icon className={`size-4 shrink-0 ${iconColor}`} />
            <span className='truncate text-sm font-medium'>{name}</span>
            <Badge variant='outline' className='shrink-0 px-1 text-[10px]'>
              {multiplierLabel}
            </Badge>
          </span>
          <span className='flex h-5 w-full min-w-0 items-center gap-1'>
            {tags.length ? (
              <>
                <Badge variant='secondary' className='max-w-[106px] truncate'>
                  {tags[0]}
                </Badge>
                {tags.length > 1 && <span className='text-muted-foreground shrink-0 text-xs'>+{tags.length - 1}</span>}
              </>
            ) : (
              <span className='text-muted-foreground text-xs'>—</span>
            )}
          </span>
        </button>
      </TooltipTrigger>
      <TooltipContent
        side='bottom'
        align='start'
        className='bg-popover text-popover-foreground max-w-[min(390px,90vw)] border p-3 shadow-md'
      >
        <div className='flex flex-col gap-2 text-xs'>
          <strong className='text-sm break-words'>
            {name}
            {provider ? ` · ${t(`channels.providers.${provider}`, { defaultValue: provider })}` : ''}
          </strong>
          <p>
            {t('requests.diagnostics.session')}:{' '}
            {t(`requests.diagnostics.sessions.${state}`, { defaultValue: t('requests.diagnostics.unrecorded') })}
          </p>
          <p className='break-all'>
            {t('requests.diagnostics.key')}:{' '}
            {selection?.keyHint
              ? [selection.keyRemark, selection.keyHint].filter(Boolean).join(' · ')
              : t('requests.diagnostics.unrecorded')}
          </p>
          <p className='break-words'>
            {t('requests.diagnostics.tags')}: {tags.join(' / ') || '—'}
          </p>
          <p>
            {t('requests.diagnostics.requestMultiplier')}: {multiplier == null ? t('requests.diagnostics.unrecorded') : multiplierLabel}
          </p>
          <p className='text-muted-foreground'>{t('requests.diagnostics.openRoutingHint')}</p>
        </div>
      </TooltipContent>
    </Tooltip>
  );
}

export function RequestCacheCell({ request }: { request: Request }) {
  const { t } = useTranslation();
  const usage = request.usageLogs?.edges?.[0]?.node;
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
            {t('requests.diagnostics.cache5m')}: {(usage.promptWriteCachedTokens5m ?? 0).toLocaleString()}
          </p>
          <p>
            {t('requests.diagnostics.cache1h')}: {(usage.promptWriteCachedTokens1h ?? 0).toLocaleString()}
          </p>
          <p>{t('requests.diagnostics.hitRateFormula')}</p>
        </div>
      </TooltipContent>
    </Tooltip>
  );
}

export function RequestDurationCell({ request }: { request: Request }) {
  const { t } = useTranslation();
  const latency = request.metricsLatencyMs;
  const first = request.metricsFirstTokenLatencyMs;
  const speed = getTokensPerSecondValue(request);
  return (
    <div className='flex min-w-[150px] flex-col gap-1 text-xs whitespace-nowrap'>
      <span>
        {request.stream && first != null ? `${t('requests.duration.firstToken', { duration: formatDuration(first) })} · ` : ''}
        {t('requests.duration.total', { duration: latency == null ? '—' : formatDuration(latency) })}
      </span>
      <span className='text-muted-foreground'>
        {!request.stream ? `${t('requests.diagnostics.average')} ` : ''}
        {speed == null ? t('requests.diagnostics.noSpeed') : `${Math.round(speed)} tok/s`}
      </span>
    </div>
  );
}
