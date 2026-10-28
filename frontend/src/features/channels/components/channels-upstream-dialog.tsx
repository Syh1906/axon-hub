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
import {
  useChannelUpstreamStates,
  useRefreshChannelUpstreams,
  useUpstreamRefreshPending,
  type ChannelUpstreamState,
} from '../data/channel-upstream';
import type { Channel } from '../data/schema';
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
  const accounts = [
    ...new Map(
      state.keys.flatMap((key) => (key.snapshot.account ? [[key.snapshot.account.balance.accountRef, key.snapshot.account] as const] : []))
    ).values(),
  ];
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
      {accounts.map((account) => (
        <Alert key={account.balance.accountRef}>
          <Database className='size-4' />
          <AlertDescription>
            <strong className='text-foreground mr-3'>{formatBalance(account.balance)}</strong>
            {t('channels.upstream.accountSummary', {
              id: account.accountId,
              count: state.keys.filter((key) => key.snapshot.account?.accountId === account.accountId).length,
            })}
            <span className='text-muted-foreground ml-3 text-xs'>{new Date(account.updatedAt).toLocaleString()}</span>
          </AlertDescription>
        </Alert>
      ))}
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
                {['key', 'quota', 'group', 'upstreamRate', 'appliedRate', 'state'].map((name) => (
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
                    {key.disabled && (
                      <Badge variant='outline' className='ml-1'>
                        {t('channels.upstream.keyDisabled')}
                      </Badge>
                    )}
                  </TableCell>
                  <TableCell>
                    <div className='flex min-w-24 flex-col gap-1'>
                      {key.snapshot.balances
                        .filter((balance) => balance.kind !== 'ACCOUNT' || !key.snapshot.account)
                        .map((balance, i) => (
                          <div key={i}>
                            <span className='tabular-nums'>{formatBalance(balance)}</span>
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
                    {key.snapshot.rate?.group ||
                      t(key.snapshot.provider === 'SUB2_API' ? 'channels.upstream.notProvided' : 'channels.upstream.unmatched')}
                  </TableCell>
                  <TableCell className='font-mono'>{key.upstreamMultiplier == null ? '—' : `${Number(key.upstreamMultiplier)}×`}</TableCell>
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
                                : key.snapshot[`${kind}State`] === 'NEVER' ? t('channels.upstream.states.NEVER') : t('channels.upstream.updatedAt', {
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
      <p className='text-muted-foreground text-xs'>{t('channels.upstream.identityHint')}</p>
    </div>
  );
}
