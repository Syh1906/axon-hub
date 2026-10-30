import { useState } from 'react';
import { Info, RefreshCw } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { usePermissions } from '@/hooks/usePermissions';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { DialogFooter } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group';
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Separator } from '@/components/ui/separator';
import { Switch } from '@/components/ui/switch';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { isValidPriceMultiplier } from '@/features/models/data/pricing';
import {
  useRefreshChannelUpstreams,
  useSaveChannelUpstream,
  useUpstreamRefreshPending,
  type ChannelUpstreamState,
} from '../data/channel-upstream';
import { isBalanceOnlyProvider, hasUsableUpstreamRate } from '../data/channel-upstream-refresh';
import type { Channel } from '../data/schema';

export function ChannelUpstreamSettingsForm({
  channel,
  state,
  onClose,
}: {
  channel: Channel;
  state: ChannelUpstreamState;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const { channelPermissions } = usePermissions();
  const [enabled, setEnabled] = useState(state.settings.enabled);
  const balanceOnly = isBalanceOnlyProvider(state.provider);
  const [selectedMode, setMode] = useState(state.settings.costMode);
  const mode = balanceOnly ? 'MANUAL' : selectedMode;
  const [interval, setInterval] = useState(String(state.settings.refreshIntervalMinutes));
  const [manual, setManual] = useState(state.manualMultiplier);
  const [error, setError] = useState('');
  const [container, setContainer] = useState<HTMLFormElement | null>(null);
  const save = useSaveChannelUpstream(channel.id);
  const refresh = useRefreshChannelUpstreams();
  const pendingRefresh = useUpstreamRefreshPending(channel.id);
  const locked = save.isPending || !channelPermissions.canWrite;
  const canFollow = enabled && !balanceOnly;
  const redetect = () => refresh.mutate({ channelIds: [channel.id], force: true, includeDisabled: true });
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    setError('');
    if (!isValidPriceMultiplier(manual)) {
      setError(t('channels.routing.invalidMultiplier'));
      return;
    }
    try {
      await save.mutateAsync({
        enabled,
        refreshIntervalMinutes: Number(interval),
        costMode: enabled && canFollow ? mode : 'MANUAL',
        manualMultiplier: manual,
      });
      save.reset();
      if (enabled) refresh.mutate({ channelIds: [channel.id], force: true });
      onClose();
    } catch {
      /* Preserve the draft; the mutation displays the failure. */
    }
  };
  const manualField = (
    <div className='grid items-start gap-2 sm:grid-cols-[180px_1fr]'>
      <Label htmlFor='upstream-manual' className='pt-2'>
        {t(mode === 'UPSTREAM' && enabled ? 'channels.upstream.fallbackMultiplier' : 'channels.upstream.manualMultiplier')}
      </Label>
      <div className='flex flex-col gap-2'>
        <div className='flex items-center gap-2'>
          <Input
            id='upstream-manual'
            className='w-40'
            type='number'
            min='0'
            step='any'
            value={manual}
            onChange={(event) => setManual(event.target.value)}
          />
          <span>×</span>
        </div>
        <p className='text-muted-foreground text-xs'>
          {t(mode === 'UPSTREAM' && enabled ? 'channels.upstream.fallbackHint' : 'channels.upstream.manualHint')}
        </p>
      </div>
    </div>
  );
  return (
    <form ref={setContainer} onSubmit={submit} className='flex flex-col gap-5 pt-3'>
      <fieldset disabled={locked} className='flex min-w-0 flex-col gap-5'>
        <div className='flex items-start justify-between gap-5'>
          <div className='flex flex-col gap-1'>
            <Label htmlFor='upstream-enabled' className='text-base'>
              {t('channels.upstream.autoSync')}
            </Label>
            <p className='text-muted-foreground text-sm'>{t('channels.upstream.autoHint', { minutes: interval })}</p>
          </div>
          <Switch
            id='upstream-enabled'
            checked={enabled}
            onCheckedChange={(next) => {
              setEnabled(next);
              if (!next) setMode('MANUAL');
              else if (state.provider === 'UNKNOWN' && !pendingRefresh) redetect();
            }}
          />
        </div>
        {!enabled && (
          <Alert>
            <Info className='size-4' />
            <AlertDescription>{t('channels.upstream.disabledHint')}</AlertDescription>
          </Alert>
        )}
        <Alert>
          <Info className='size-4' />
          <AlertDescription className='flex items-center justify-between gap-3'>
            <div>
              <p className='font-medium'>{t(`channels.upstream.providers.${state.provider}`)}</p>
              <p className='text-muted-foreground mt-1 text-xs'>
                {t(state.provider === 'SUB2_API' ? 'channels.upstream.sub2Auto' : 'channels.upstream.detectHint', {
                  count: state.keys.length,
                })}
              </p>
            </div>
            <Button type='button' variant='ghost' size='sm' disabled={pendingRefresh} onClick={redetect}>
              <RefreshCw className={cn(pendingRefresh && 'animate-spin')} />
              {t(state.provider === 'UNKNOWN' ? 'channels.upstream.detect' : 'channels.upstream.redetect')}
            </Button>
          </AlertDescription>
        </Alert>
        <p className='text-muted-foreground text-sm'>
          {t(
            state.provider !== 'NEW_API' && state.provider !== 'UNKNOWN' ? 'channels.accounts.keyAuthHint' : 'channels.accounts.authInTab'
          )}
        </p>
        <Separator />
        <div className='grid gap-3 sm:grid-cols-[180px_1fr]'>
          <Label className='pt-1'>{t('channels.upstream.costSource')}</Label>
          <div className='flex flex-col gap-2'>
            <RadioGroup value={mode} onValueChange={(value) => setMode(value as 'MANUAL' | 'UPSTREAM')} className='flex flex-wrap gap-6'>
              <div className='flex items-center gap-2'>
                <RadioGroupItem id='upstream-mode-manual' value='MANUAL' />
                <Label htmlFor='upstream-mode-manual'>{t('channels.upstream.manual')}</Label>
              </div>
              <div className='flex items-center gap-2'>
                <RadioGroupItem id='upstream-mode-follow' value='UPSTREAM' disabled={!canFollow} />
                <Label htmlFor='upstream-mode-follow'>{t('channels.upstream.follow')}</Label>
              </div>
            </RadioGroup>
            <p className='text-muted-foreground text-xs'>
              {t(
                balanceOnly
                  ? 'channels.upstream.balanceOnlyHint'
                  : canFollow
                    ? 'channels.upstream.sourceHint'
                    : 'channels.upstream.followRequires'
              )}
            </p>
          </div>
        </div>
        {mode === 'MANUAL' && manualField}
        {enabled && mode === 'UPSTREAM' && (
          <div className='flex flex-col gap-2'>
            <div className='max-h-48 overflow-auto rounded-lg border'>
              <Table>
                <TableHeader>
                  <TableRow>
                    {['key', 'upstreamRate', 'current', 'afterSave'].map((key) => (
                      <TableHead key={key}>{t(`channels.upstream.table.${key}`)}</TableHead>
                    ))}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {state.keys
                    .filter((key) => !key.disabled)
                    .map((key, index) => {
                      const fresh = hasUsableUpstreamRate(key);
                      return (
                        <TableRow key={key.keyRef}>
                          <TableCell className='font-mono'>
                            #{index + 1} · {key.label}
                          </TableCell>
                          <TableCell>{key.upstreamMultiplier == null ? '—' : `${Number(key.upstreamMultiplier)}×`}</TableCell>
                          <TableCell>{Number(key.applied.value)}×</TableCell>
                          <TableCell className='text-primary'>
                            {fresh ? `${Number(key.upstreamMultiplier)}×` : `${Number(manual)}× · ${t('channels.upstream.estimatedShort')}`}
                          </TableCell>
                        </TableRow>
                      );
                    })}
                </TableBody>
              </Table>
            </div>
            <p className='text-muted-foreground text-xs'>{t('channels.upstream.newRequestsOnly')}</p>
          </div>
        )}
        <details className='rounded-lg border p-3' open={mode === 'UPSTREAM'}>
          <summary className='cursor-pointer text-sm font-medium'>
            {t('channels.upstream.moreSettings')}
            <span className='text-muted-foreground ml-4 text-xs'>{t('channels.upstream.intervalSummary', { minutes: interval })}</span>
          </summary>
          <div className='mt-4 flex flex-col gap-4'>
            <div className='grid gap-2 sm:grid-cols-[180px_1fr]'>
              <Label htmlFor='upstream-interval' className='pt-2'>
                {t('channels.upstream.interval')}
              </Label>
              <Select value={interval} onValueChange={setInterval}>
                <SelectTrigger id='upstream-interval' className='w-40'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent container={container}>
                  <SelectGroup>
                    {[...new Set([1, 5, 10, 15, 30, 60, Number(interval)])]
                      .sort((a, b) => a - b)
                      .map((minutes) => (
                        <SelectItem key={minutes} value={String(minutes)}>
                          {t('channels.upstream.minutes', { count: minutes })}
                        </SelectItem>
                      ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
            {mode === 'UPSTREAM' && manualField}
            <p className='text-muted-foreground text-xs'>{t('channels.upstream.expiryHint')}</p>
          </div>
        </details>
        <Alert>
          <Info className='size-4' />
          <AlertDescription>{t('channels.upstream.basePriceHint')}</AlertDescription>
        </Alert>
      </fieldset>
      {error && (
        <p role='alert' className='text-destructive text-sm'>
          {error}
        </p>
      )}
      <DialogFooter className='bg-background sticky bottom-0 border-t pt-4'>
        <Button type='button' variant='outline' onClick={onClose} disabled={save.isPending}>
          {t('common.buttons.cancel')}
        </Button>
        {channelPermissions.canWrite && (
          <Button type='submit' disabled={locked || pendingRefresh}>
            {t(
              save.isPending ? 'channels.upstream.saving' : enabled ? 'channels.upstream.saveAndEnable' : 'channels.upstream.saveSettings'
            )}
          </Button>
        )}
      </DialogFooter>
    </form>
  );
}
