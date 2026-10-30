import { useState } from 'react';
import { RefreshCw, Info } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { cn } from '@/lib/utils';
import { usePermissions } from '@/hooks/usePermissions';
import { Button } from '@/components/ui/button';
import { HoverCard, HoverCardContent, HoverCardTrigger } from '@/components/ui/hover-card';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { useChannels } from '../context/channels-context';
import {
  multiplierRange,
  useRefreshChannelUpstreams,
  useUpstreamRefreshPending,
  type UpstreamBalance,
} from '../data/channel-upstream';
import { isBalanceOnlyProvider } from '../data/channel-upstream-refresh';
import type { Channel } from '../data/schema';
import { ChannelRoutingValueCell } from './channel-routing-value-cell';

export function useBalanceLabel() {
  const { t, i18n } = useTranslation();
  return (balance: UpstreamBalance): string => {
    if (balance.unlimited) return t('channels.upstream.unlimited');
    if (balance.amount == null) return t('channels.upstream.notProvided');
    if (balance.currency)
      return t('currencies.format', {
        val: Number(balance.amount),
        currency: balance.currency,
        locale: i18n.language === 'zh' ? 'zh-CN' : 'en-US',
        minimumFractionDigits: 2,
        maximumFractionDigits: 4,
      });
    return `${Number(balance.amount).toLocaleString()} ${t(`channels.upstream.units.${balance.unit}`)}`;
  };
}

export function ChannelUpstreamBalanceCell({ channel }: { channel: Channel }) {
  const { t } = useTranslation();
  const formatBalance = useBalanceLabel();
  const { channelPermissions } = usePermissions();
  const { setOpen, setCurrentRow } = useChannels();
  const refresh = useRefreshChannelUpstreams();
  const pending = useUpstreamRefreshPending(channel.id);
  const state = channel.upstreamState;
  const summary = state?.balanceSummary;
  const openDetails = () => {
    setCurrentRow(channel);
    setOpen('upstream');
  };
  const singleQuota = state?.keys.length === 1 ? state.keys[0].snapshot.balances.find((balance) => balance.kind === 'QUOTA') : undefined;
  const label = !state
    ? t(channel.upstreamLoadError ? 'channels.upstream.unavailable' : 'channels.upstream.loading')
    : summary!.balances.length === 1
      ? formatBalance(summary!.balances[0])
      : summary!.balances.length > 1
        ? t('channels.accounts.multipleCurrencies')
        : singleQuota
          ? formatBalance(singleQuota)
          : t('channels.accounts.notProvided');
  return (
    <div className='flex w-36 flex-col items-center gap-1'>
      <div className='flex w-full items-center justify-center gap-1'>
        <Button variant='ghost' size='sm' className='min-w-0 truncate px-1 tabular-nums' onClick={openDetails} title={label}>
          {label}
        </Button>
        {summary && !summary.complete && summary.balances.length > 0 && (
          <Tooltip>
            <TooltipTrigger asChild>
              <span tabIndex={0} aria-label={t('channels.accounts.partial')}>
                <Info className='text-muted-foreground size-3' />
              </span>
            </TooltipTrigger>
            <TooltipContent>
              {t('channels.accounts.partialHint', {
                known: summary.knownAccountCount,
                total: summary.accountCount,
                unassigned: summary.unassignedKeyCount,
              })}
            </TooltipContent>
          </Tooltip>
        )}
        {channelPermissions.canWrite && (
          <Button
            variant='outline'
            size='icon'
            className='size-7 shrink-0'
            disabled={pending || channel.status === 'archived'}
            aria-label={t('channels.upstream.refresh')}
            onClick={() => refresh.mutate({ channelIds: [channel.id], force: true, includeDisabled: true })}
          >
            <RefreshCw className={cn(pending && 'animate-spin')} />
          </Button>
        )}
      </div>
      {singleQuota && <span className='text-muted-foreground text-xs'>{t('channels.upstream.kinds.QUOTA')}</span>}
      {state && state.keys.length > 1 && (
        <HoverCard openDelay={150} closeDelay={100}>
          <HoverCardTrigger asChild>
            <Button variant='link' className='text-muted-foreground h-auto p-0 text-xs' onClick={openDetails}>
              {t('channels.accounts.keyCount', { count: state.keys.length })}
            </Button>
          </HoverCardTrigger>
          <HoverCardContent className='w-80' align='end'>
            <p className='font-medium'>{t('channels.upstream.keyDetails')}</p>
            <p className='text-muted-foreground mb-3 text-xs'>
              {t('channels.accounts.summary', { accounts: summary!.accountCount, keys: state.keys.length })}
              {summary!.balances.length > 0 && ` · ${summary!.balances.map(formatBalance).join(' / ')}`}
            </p>
            <div className='flex flex-col gap-2'>
              {state.keys.slice(0, 3).map((key) => {
                const account = state.accounts.find((account) => account.ref === key.accountRef);
                return (
                  <div key={key.keyRef} className='flex items-center gap-3 text-xs'>
                    <div className='min-w-0 flex-1'>
                      <p className='truncate'>{key.remark || account?.name || t('channels.accounts.unassigned')}</p>
                      <p className='text-muted-foreground truncate font-mono'>{key.label}</p>
                    </div>
                    <div className='flex max-w-40 min-w-0 flex-col gap-2 text-right tabular-nums'>
                      {key.snapshot.balances.length ? (
                        key.snapshot.balances.map((balance, index) => (
                          <div key={index}>
                            {balance.kind === 'QUOTA' && balance.amount == null && !balance.unlimited ? (
                              <>
                                {balance.limit != null && (
                                  <p>{t('channels.upstream.billingLimit', { value: formatBalance({ ...balance, amount: balance.limit }) })}</p>
                                )}
                                {balance.used != null && (
                                  <p>{t('channels.upstream.billingUsed', { value: formatBalance({ ...balance, amount: balance.used }) })}</p>
                                )}
                              </>
                            ) : formatBalance(balance)}
                            <p className='text-muted-foreground'>{t(`channels.upstream.kinds.${balance.kind}`)}</p>
                          </div>
                        ))
                      ) : t('channels.upstream.notProvided')}
                    </div>
                    <span className='text-muted-foreground shrink-0'>
                      {t(key.disabled ? 'channels.upstream.keyDisabled' : 'channels.accounts.enabled')}
                    </span>
                  </div>
                );
              })}
            </div>
            <Button variant='link' className='mt-2 h-auto px-0' onClick={openDetails}>
              {t('channels.accounts.expandKeys', { count: state.keys.length })}
            </Button>
            <p className='text-muted-foreground text-xs'>{t('channels.accounts.balanceHint')}</p>
          </HoverCardContent>
        </HoverCard>
      )}
    </div>
  );
}

export function ChannelUpstreamMultiplierCell({ channel, canWrite }: { channel: Channel; canWrite: boolean }) {
  const { t } = useTranslation();
  const state = channel.upstreamState;
  const keys = state?.keys.filter((key) => !key.disabled) ?? [];
  const follow = state?.settings.enabled && state.settings.costMode === 'UPSTREAM' && !isBalanceOnlyProvider(state.provider);
  const range = multiplierRange(keys.map((key) => key.applied.value));
  const upstream = multiplierRange(keys.flatMap((key) => (key.upstreamMultiplier == null ? [] : [key.upstreamMultiplier])));
  const estimated = keys.some((key) => key.applied.source === 'ESTIMATED');
  const cached = keys.some((key) => key.applied.source === 'CACHED');
  return (
    <div className='flex min-w-36 flex-col items-center gap-1'>
      <ChannelRoutingValueCell
        channel={channel}
        field='costMultiplier'
        canWrite={canWrite}
        displayOverride={follow ? range : undefined}
        upstreamFollow={Boolean(follow)}
      />
      <span
        className='text-muted-foreground max-w-44 truncate text-xs'
        title={follow ? t('channels.upstream.perKeyHint') : t('channels.upstream.manualHint')}
      >
        {!state
          ? t(channel.upstreamLoadError ? 'channels.upstream.unavailable' : 'channels.upstream.loading')
          : follow
            ? t(estimated ? 'channels.upstream.estimated' : cached ? 'channels.upstream.cached' : 'channels.upstream.followPerKey')
            : upstream !== '—'
              ? t('channels.upstream.manualReference', { multiplier: upstream })
              : t('channels.upstream.manual')}
      </span>
    </div>
  );
}

export function RefreshPageUpstreamsButton({ channelIds }: { channelIds: string[] }) {
  const { t } = useTranslation();
  const { channelPermissions } = usePermissions();
  const [running, setRunning] = useState(false);
  const refresh = useRefreshChannelUpstreams();
  const pending = useUpstreamRefreshPending();
  const run = async () => {
    setRunning(true);
    try {
      const { refreshed, skipped, failed } = await refresh.mutateAsync({ channelIds, force: true, silent: true });
      (failed ? toast.warning : toast.success)(t('channels.upstream.refreshSummary', { refreshed, skipped, failed }));
    } catch {
      toast.error(t('channels.upstream.refreshFailed'));
    } finally {
      setRunning(false);
    }
  };
  if (!channelPermissions.canWrite) return null;
  return (
    <Button variant='outline' size='sm' disabled={!channelIds.length || running || pending} onClick={run} title={t('channels.upstream.refreshAllHint')}>
      <RefreshCw className={cn(running && 'animate-spin')} />
      {t('channels.upstream.refreshAll')}
    </Button>
  );
}
