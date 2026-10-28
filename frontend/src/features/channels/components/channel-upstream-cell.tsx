import { useState } from 'react';
import { RefreshCw } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { cn } from '@/lib/utils';
import { usePermissions } from '@/hooks/usePermissions';
import { Button } from '@/components/ui/button';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { useChannels } from '../context/channels-context';
import {
  getUpstreamRefreshTargets,
  multiplierRange,
  useRefreshChannelUpstreams,
  useUpstreamRefreshPending,
  type UpstreamBalance,
} from '../data/channel-upstream';
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
  const keys = state?.keys.filter((key) => !key.disabled) ?? [];
  const accounts = new Map(
    keys.flatMap((key) => (key.snapshot.account ? [[key.snapshot.account.balance.accountRef, key.snapshot.account] as const] : []))
  );
  const singleBalance =
    keys.length === 1
      ? (keys[0].snapshot.balances.find((balance) => balance.kind === 'ACCOUNT') ?? keys[0].snapshot.balances[0])
      : undefined;
  const account = accounts.size === 1 ? [...accounts.values()][0] : undefined;
  const balance = account?.balance ?? singleBalance;
  const anyBalances = keys.some((key) => key.snapshot.balances.length > 0);
  const unsupported = keys.length > 0 && keys.every((key) => key.snapshot.balanceState === 'UNSUPPORTED');
  const failed = keys.some((key) => key.snapshot.balanceState === 'ERROR' || key.snapshot.balanceState === 'AUTH_REQUIRED');
  const lastUpdated =
    account?.updatedAt ??
    keys
      .map((key) => key.snapshot.balanceUpdatedAt)
      .filter(Boolean)
      .sort()
      .at(-1);
  const openDetails = () => {
    setCurrentRow(channel);
    setOpen('upstream');
  };
  const label = !state
    ? t(channel.upstreamLoadError ? 'channels.upstream.unavailable' : 'channels.upstream.loading')
    : balance
      ? formatBalance(balance)
      : accounts.size > 1
        ? t('channels.upstream.multipleAccounts', { count: accounts.size })
        : anyBalances
          ? t('channels.upstream.multipleKeys', { count: keys.length })
          : unsupported
            ? t('channels.upstream.unsupported')
            : state?.settings.enabled
              ? t('channels.upstream.noSnapshot')
              : t('channels.upstream.notEnabled');
  const detail = !state
    ? t('channels.upstream.viewDetails')
    : !state.settings.enabled
      ? t(lastUpdated ? 'channels.upstream.syncOff' : 'channels.upstream.syncOffEmpty')
      : failed
        ? t('channels.upstream.refreshFailedShort')
        : account
          ? t('channels.upstream.sharedAccount', {
              count: keys.filter((key) => key.snapshot.account?.accountId === account.accountId).length,
            })
          : balance
            ? t(`channels.upstream.kinds.${balance.kind}`)
            : t('channels.upstream.viewDetails');
  return (
    <div className='flex min-w-36 flex-col items-center gap-1'>
      <div className='flex items-center gap-1'>
        <Button
          variant='ghost'
          size='sm'
          className='h-8 px-1 tabular-nums'
          onClick={openDetails}
          aria-label={`${t('channels.upstream.title')} · ${channel.name}`}
        >
          {label}
        </Button>
        {channelPermissions.canWrite && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant='outline'
                size='icon'
                className='size-7'
                disabled={pending || channel.status === 'archived'}
                aria-label={`${t('channels.upstream.refresh')} · ${channel.name}`}
                onClick={() => refresh.mutate({ channelIds: [channel.id], force: true, includeDisabled: true })}
              >
                <RefreshCw className={cn(pending && 'animate-spin')} />
              </Button>
            </TooltipTrigger>
            <TooltipContent>{t('channels.upstream.refreshHint')}</TooltipContent>
          </Tooltip>
        )}
      </div>
      <Tooltip>
        <TooltipTrigger asChild>
          <span className='text-muted-foreground max-w-44 truncate text-xs'>{detail}</span>
        </TooltipTrigger>
        <TooltipContent>
          {lastUpdated ? t('channels.upstream.updatedAt', { time: new Date(lastUpdated).toLocaleString() }) : detail}
        </TooltipContent>
      </Tooltip>
    </div>
  );
}

export function ChannelUpstreamMultiplierCell({ channel, canWrite }: { channel: Channel; canWrite: boolean }) {
  const { t } = useTranslation();
  const state = channel.upstreamState;
  const keys = state?.keys.filter((key) => !key.disabled) ?? [];
  const follow = state?.settings.enabled && state.settings.costMode === 'UPSTREAM';
  const range = multiplierRange(keys.map((key) => key.applied.value));
  const upstream = multiplierRange(keys.flatMap((key) => (key.upstreamMultiplier == null ? [] : [key.upstreamMultiplier])));
  const estimated = keys.some((key) => key.applied.source === 'ESTIMATED');
  const cached = keys.some((key) => key.applied.source === 'CACHED');
  return (
    <div className='flex min-w-36 flex-col items-center gap-1'>
      <ChannelRoutingValueCell
        channel={channel}
        field='costMultiplier'
        tag=''
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

export function RefreshAllUpstreamsButton() {
  const { t } = useTranslation();
  const { channelPermissions } = usePermissions();
  const [running, setRunning] = useState(false);
  const refresh = useRefreshChannelUpstreams();
  const pending = useUpstreamRefreshPending();
  const run = async () => {
    setRunning(true);
    try {
      const ids = await getUpstreamRefreshTargets();
      const { refreshed, skipped, failed } = await refresh.mutateAsync({ channelIds: ids, force: true, silent: true });
      (failed ? toast.warning : toast.success)(t('channels.upstream.refreshSummary', { refreshed, skipped, failed }));
    } catch {
      toast.error(t('channels.upstream.refreshFailed'));
    } finally {
      setRunning(false);
    }
  };
  if (!channelPermissions.canWrite) return null;
  return (
    <Button variant='outline' size='sm' disabled={running || pending} onClick={run} title={t('channels.upstream.refreshAllHint')}>
      <RefreshCw className={cn(running && 'animate-spin')} />
      {t('channels.upstream.refreshAll')}
    </Button>
  );
}
