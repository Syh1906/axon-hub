import { useState } from 'react';
import { Info, RefreshCw } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { usePermissions } from '@/hooks/usePermissions';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
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
  verifyUpstreamAccount,
  type ChannelUpstreamState,
} from '../data/channel-upstream';
import { hasUsableUpstreamRate } from '../data/channel-upstream-refresh';
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
  const [mode, setMode] = useState(state.settings.costMode);
  const [interval, setInterval] = useState(String(state.settings.refreshIntervalMinutes));
  const [manual, setManual] = useState(state.manualMultiplier);
  const [token, setToken] = useState('');
  const [accountID, setAccountID] = useState(state.accountId ? String(state.accountId) : '');
  const [requiresID, setRequiresID] = useState(false);
  const [clearAccount, setClearAccount] = useState(false);
  const [verifying, setVerifying] = useState(false);
  const [verified, setVerified] = useState(false);
  const [error, setError] = useState('');
  const [container, setContainer] = useState<HTMLFormElement | null>(null);
  const save = useSaveChannelUpstream(channel.id);
  const refresh = useRefreshChannelUpstreams();
  const pendingRefresh = useUpstreamRefreshPending(channel.id);
  const locked = save.isPending || verifying || !channelPermissions.canWrite;
  const connected = !clearAccount && (state.accountConnected || verified);
  const canFollow = enabled && (state.provider === 'SUB2_API' || (state.provider === 'NEW_API' && connected));
  const redetect = () => refresh.mutate({ channelIds: [channel.id], force: true, includeDisabled: true });
  const verify = async () => {
    setVerifying(true);
    setError('');
    try {
      const result = await verifyUpstreamAccount(channel.id, token, accountID ? Number(accountID) : undefined);
      setRequiresID(result.requiresAccountId);
      setVerified(result.success);
      if (result.accountId) setAccountID(String(result.accountId));
      if (!result.success)
        setError(t(`channels.upstream.errors.${result.errorCode}`, { defaultValue: t('channels.upstream.verifyFailed') }));
    } catch {
      setError(t('channels.upstream.verifyFailed'));
    } finally {
      setVerifying(false);
    }
  };
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    setError('');
    if (!isValidPriceMultiplier(manual)) {
      setError(t('channels.routing.invalidMultiplier'));
      return;
    }
    if (requiresID && (!accountID || !Number.isInteger(Number(accountID)) || Number(accountID) <= 0)) {
      setError(t('channels.upstream.errors.ACCOUNT_ID_REQUIRED'));
      return;
    }
    try {
      await save.mutateAsync({
        enabled,
        refreshIntervalMinutes: Number(interval),
        costMode: enabled && canFollow ? mode : 'MANUAL',
        manualMultiplier: manual,
        ...(token.trim() ? { accountToken: token.trim() } : {}),
        ...(accountID ? { accountId: Number(accountID) } : {}),
        clearAccount,
      });
      setToken('');
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
        {state.provider === 'NEW_API' && (
          <section className='flex flex-col gap-4 rounded-lg border p-4'>
            <div className='flex items-center justify-between'>
              <h3 className='font-semibold'>{t('channels.upstream.connectAccount')}</h3>
              <Badge variant={connected ? 'secondary' : 'outline'}>
                {t(connected ? 'channels.upstream.connected' : 'channels.upstream.notConnected')}
              </Badge>
            </div>
            <p className='text-muted-foreground text-sm'>{t('channels.upstream.accountHint')}</p>
            <div className='grid gap-2 sm:grid-cols-[170px_1fr]'>
              <Label htmlFor='upstream-account-token' className='pt-2'>
                {t('channels.upstream.accountToken')}
              </Label>
              <div className='flex flex-col gap-2'>
                <Input
                  id='upstream-account-token'
                  type='password'
                  autoComplete='new-password'
                  value={token}
                  placeholder={state.accountConnected ? t('channels.upstream.keepToken') : t('channels.upstream.enterToken')}
                  onChange={(event) => {
                    setToken(event.target.value);
                    setVerified(false);
                    setClearAccount(false);
                  }}
                />
                <p className='text-muted-foreground text-xs'>{t('channels.upstream.tokenHint')}</p>
              </div>
            </div>
            {(requiresID || accountID) && (
              <div className='grid gap-2 sm:grid-cols-[170px_1fr]'>
                <Label htmlFor='upstream-account-id' className='pt-2'>
                  {t('channels.upstream.accountID')}
                  {requiresID && ' *'}
                </Label>
                <div className='flex flex-col gap-2'>
                  <Input
                    id='upstream-account-id'
                    type='number'
                    min='1'
                    step='1'
                    value={accountID}
                    onChange={(event) => {
                      setAccountID(event.target.value);
                      setVerified(false);
                    }}
                  />
                  <p className='text-muted-foreground text-xs'>{t('channels.upstream.accountIDHint')}</p>
                </div>
              </div>
            )}
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <p className='text-muted-foreground max-w-md text-xs'>
                {t(verified ? 'channels.upstream.verifiedHint' : 'channels.upstream.tokenLocation')}
              </p>
              <div className='flex gap-2'>
                {state.accountConnected && (
                  <Button
                    type='button'
                    variant='ghost'
                    size='sm'
                    onClick={() => {
                      setClearAccount(true);
                      setToken('');
                      setAccountID('');
                      setMode('MANUAL');
                    }}
                  >
                    {t('channels.upstream.disconnect')}
                  </Button>
                )}
                <Button type='button' variant='outline' disabled={!token.trim() || verifying} onClick={verify}>
                  {t(verifying ? 'channels.upstream.verifying' : 'channels.upstream.verify')}
                </Button>
              </div>
            </div>
          </section>
        )}
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
              {t(canFollow ? 'channels.upstream.sourceHint' : 'channels.upstream.followRequires')}
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
