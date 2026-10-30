import { useState } from 'react';
import { Database, RefreshCw, Info } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { usePermissions } from '@/hooks/usePermissions';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { useChannels } from '../context/channels-context';
import {
  useChannelUpstreamStates,
  useRefreshChannelUpstreams,
  useUpstreamRefreshPending,
  type ChannelUpstreamState,
} from '../data/channel-upstream';
import { isBalanceOnlyProvider } from '../data/channel-upstream-refresh';
import type { Channel } from '../data/schema';
import { ChannelAccountsPanel } from './channel-accounts-panel';
import { useBalanceLabel } from './channel-upstream-cell';
import { ChannelUpstreamSettingsForm } from './channel-upstream-settings-form';

export function ChannelsUpstreamDialog({ channel, onClose }: { channel: Channel; onClose: () => void }) {
  const { t } = useTranslation();
  const [tab, setTab] = useState('keys');
  const query = useChannelUpstreamStates([channel.id]);
  const state = query.data?.[0];
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent className='flex max-h-[90vh] flex-col gap-4 sm:max-w-4xl'>
        <DialogHeader>
          <DialogTitle className='flex items-center gap-2'>
            <Database className='size-5' />
            {t('channels.upstream.title')}
          </DialogTitle>
          <DialogDescription className='flex items-center gap-2'>
            {channel.name}
            {state && <Badge variant='secondary'>{t(`channels.upstream.providers.${state.provider}`)}</Badge>}
          </DialogDescription>
        </DialogHeader>
        {query.isPending ? (
          <Skeleton className='h-72 w-full' />
        ) : state ? (
          <Tabs value={tab} onValueChange={setTab} className='flex min-h-0 flex-1 flex-col'>
            <TabsList className='w-fit'>
              <TabsTrigger value='keys'>{t('channels.upstream.keyDetails')}</TabsTrigger>
              <TabsTrigger value='accounts'>{t('channels.accounts.tab')}</TabsTrigger>
              <TabsTrigger value='settings'>{t('channels.upstream.syncSettings')}</TabsTrigger>
            </TabsList>
            <TabsContent value='keys' className='min-h-0 overflow-y-auto'>
              <UpstreamKeyDetails state={state} />
              <DialogFooter className='mt-4'>
                <Button variant='outline' onClick={onClose}>
                  {t('common.buttons.close')}
                </Button>
                <Button onClick={() => setTab('settings')}>{t('channels.upstream.syncSettings')}</Button>
              </DialogFooter>
            </TabsContent>
            <TabsContent value='settings' className='min-h-0 overflow-y-auto'>
              <ChannelUpstreamSettingsForm channel={channel} state={state} onClose={onClose} />
            </TabsContent>
            <TabsContent value='accounts' className='min-h-0 overflow-y-auto'>
              <ChannelAccountsPanel state={state} />
              <DialogFooter className='bg-background sticky bottom-0 py-3'>
                <Button variant='outline' onClick={onClose}>
                  {t('common.buttons.close')}
                </Button>
              </DialogFooter>
            </TabsContent>
          </Tabs>
        ) : (
          <Alert variant='destructive'>
            <AlertDescription>
              {t('channels.upstream.loadFailed')}
              <Button variant='link' onClick={() => query.refetch()}>
                {t('channels.upstream.retry')}
              </Button>
            </AlertDescription>
          </Alert>
        )}
      </DialogContent>
    </Dialog>
  );
}

function UpstreamKeyDetails({ state }: { state: ChannelUpstreamState }) {
  const { t } = useTranslation();
  const formatBalance = useBalanceLabel();
  const { channelPermissions } = usePermissions();
  const refresh = useRefreshChannelUpstreams();
  const pending = useUpstreamRefreshPending(state.channelId);
  const { setOpen } = useChannels();
  const balanceOnly = isBalanceOnlyProvider(state.provider);
  const rates = state.keys.filter((key) => !key.disabled && key.snapshot.rate && key.snapshot.rateState === 'SUCCESS');
  const inconsistent =
    new Set(rates.map((key) => key.snapshot.rate!.multiplier)).size > 1 ||
    new Set(rates.map((key) => key.snapshot.rate!.group).filter(Boolean)).size > 1;
  return (
    <div className='flex flex-col gap-4 pt-2'>
      <div className='flex items-center justify-between gap-3'>
        <p className='text-muted-foreground text-sm'>
          {t(state.settings.enabled ? 'channels.upstream.detailsHint' : 'channels.upstream.disabledHint')}
        </p>
        {channelPermissions.canWrite && (
          <Button
            variant='outline'
            size='sm'
            disabled={pending}
            onClick={() => refresh.mutate({ channelIds: [state.channelId], force: true, includeDisabled: true })}
          >
            <RefreshCw className={cn(pending && 'animate-spin')} />
            {t('channels.upstream.queryOnce')}
          </Button>
        )}
      </div>
      {state.balanceSummary.balances.length > 0 && (
        <div className='flex flex-wrap items-center gap-4 rounded-lg border p-4'>
          <div className='w-full text-sm'>
            {t('channels.accounts.walletSummary', {
              accounts: state.balanceSummary.knownAccountCount,
              keys: state.keys.length,
              unassigned: state.balanceSummary.unassignedKeyCount,
            })}
          </div>
          {state.balanceSummary.balances.map((balance) => (
            <div key={`${balance.currency}:${balance.unit}`} className='flex flex-col gap-1'>
              <span className='text-muted-foreground text-xs'>{balance.currency}</span>
              <strong className='tabular-nums'>{formatBalance(balance)}</strong>
            </div>
          ))}
          {!state.balanceSummary.complete && <Badge variant='outline'>{t('channels.accounts.partial')}</Badge>}
        </div>
      )}
      {balanceOnly && (
        <Alert>
          <Info className='size-4' />
          <AlertDescription>{t('channels.upstream.balanceOnlyHint')}</AlertDescription>
        </Alert>
      )}
      {inconsistent && (
        <Alert>
          <Info className='size-4' />
          <AlertDescription>{t('channels.accounts.inconsistent')}</AlertDescription>
        </Alert>
      )}
      {state.keys.length === 0 ? (
        <Alert>
          <Info className='size-4' />
          <AlertDescription>{t('channels.upstream.noKeys')}</AlertDescription>
        </Alert>
      ) : (
        <div className='overflow-x-auto rounded-lg border'>
          <Table>
            <TableHeader>
              <TableRow>
                {['key', 'queryResult', balanceOnly ? 'account' : 'group', 'upstreamRate', 'appliedRate', 'state'].map((name) => (
                  <TableHead key={name}>{t(`channels.upstream.table.${name}`)}</TableHead>
                ))}
                {channelPermissions.canWrite && <TableHead />}
              </TableRow>
            </TableHeader>
            <TableBody>
              {state.keys.map((key, index) => (
                <TableRow key={key.keyRef}>
                  <TableCell className='whitespace-nowrap'>
                    <span className='font-mono'>
                      #{index + 1} · {key.label}
                    </span>
                    <p className='text-muted-foreground max-w-40 truncate text-xs'>
                      {key.remark} ·{' '}
                      {state.accounts.find((account) => account.ref === key.accountRef)?.name || t('channels.accounts.unassigned')}
                    </p>
                    {!key.accountRef && channelPermissions.canWrite && (
                      <Button variant='link' size='sm' className='h-auto p-0' onClick={() => setOpen('keyManagement')}>
                        {t('channels.accounts.manageLinks')}
                      </Button>
                    )}
                    {key.disabled && (
                      <Badge variant='outline' className='ml-1'>
                        {t('channels.upstream.keyDisabled')}
                      </Badge>
                    )}
                  </TableCell>
                  <TableCell>
                    <div className='flex min-w-24 flex-col gap-1'>
                      {key.snapshot.balances.map((balance, i) => (
                        <div key={i}>
                          <span className='tabular-nums'>{formatBalance(balance)}</span>
                          {balance.kind === 'QUOTA' && (
                            <div className='text-muted-foreground flex flex-col gap-1 text-xs'>
                              {balance.limit != null && (
                                <span>
                                  {t('channels.upstream.billingLimit', {
                                    value: formatBalance({ ...balance, unlimited: false, amount: balance.limit }),
                                  })}
                                </span>
                              )}
                              {balance.used != null && (
                                <span>
                                  {t('channels.upstream.billingUsed', {
                                    value: formatBalance({ ...balance, unlimited: false, amount: balance.used }),
                                  })}
                                </span>
                              )}
                              {balance.usageStart && balance.usageEnd && (
                                <span>
                                  {balance.usageStart.slice(0, 10)} — {balance.usageEnd.slice(0, 10)}
                                </span>
                              )}
                              {balance.amount == null && !balance.unlimited && <span>{t('channels.upstream.billingUnknownPeriod')}</span>}
                            </div>
                          )}
                          <p className='text-muted-foreground text-xs'>
                            {t(`channels.upstream.kinds.${balance.kind}`)}
                            {balance.expiresAt &&
                              ` · ${t('channels.upstream.expires', { time: new Date(balance.expiresAt).toLocaleDateString() })}`}
                          </p>
                        </div>
                      ))}
                      {key.snapshot.balances.length === 0 && '—'}
                    </div>
                  </TableCell>
                  <TableCell className='max-w-32 break-words'>
                    {balanceOnly
                      ? state.accounts.find((account) => account.ref === key.accountRef)?.name || t('channels.accounts.unassigned')
                      : key.snapshot.rate?.group ||
                        t(
                          key.snapshot.provider === 'SUB2_API' || isBalanceOnlyProvider(key.snapshot.provider)
                            ? 'channels.upstream.notProvided'
                            : 'channels.upstream.unmatched'
                        )}
                  </TableCell>
                  <TableCell className='font-mono'>
                    {key.upstreamMultiplier == null ? t('channels.upstream.notProvided') : `${Number(key.upstreamMultiplier)}×`}
                  </TableCell>
                  <TableCell>
                    <span className='font-mono'>{Number(key.applied.value)}×</span>
                    <p className='text-muted-foreground text-xs'>{t(`channels.upstream.sources.${key.applied.source}`)}</p>
                  </TableCell>
                  <TableCell>
                    <div className='flex min-w-24 flex-col gap-1'>
                      {(['balance', 'rate'] as const).map((kind) => (
                        <Tooltip key={kind}>
                          <TooltipTrigger asChild>
                            <span className='text-muted-foreground text-xs'>
                              {t(`channels.upstream.${kind}`)} · {t(`channels.upstream.states.${key.snapshot[`${kind}State`]}`)}
                            </span>
                          </TooltipTrigger>
                          <TooltipContent className='max-w-sm'>
                            <p>
                              {key.snapshot[`${kind}ErrorCode`]
                                ? t(`channels.upstream.errors.${key.snapshot[`${kind}ErrorCode`]}`, {
                                    defaultValue: t('channels.upstream.refreshFailed'),
                                  })
                                : key.snapshot[`${kind}State`] === 'NEVER'
                                  ? t('channels.upstream.states.NEVER')
                                  : t('channels.upstream.updatedAt', {
                                      time: new Date(
                                        (kind === 'rate' ? key.snapshot.rate?.syncedAt : key.snapshot.balanceUpdatedAt) ?? 0
                                      ).toLocaleString(),
                                    })}
                            </p>
                          </TooltipContent>
                        </Tooltip>
                      ))}
                    </div>
                  </TableCell>
                  {channelPermissions.canWrite && (
                    <TableCell>
                      <Button
                        size='icon'
                        variant='ghost'
                        className='size-7'
                        disabled={pending}
                        onClick={() =>
                          refresh.mutate({ channelIds: [state.channelId], force: true, includeDisabled: true, keyRef: key.keyRef })
                        }
                        aria-label={`${t('channels.upstream.refresh')} Key ${index + 1}`}
                      >
                        <RefreshCw className={cn(pending && 'animate-spin')} />
                      </Button>
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <p className='text-muted-foreground text-xs'>{t('channels.accounts.unassignedBalanceHint')}</p>
    </div>
  );
}
