import { useState } from 'react';
import { ChevronDown, ChevronRight, Plus, Trash2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { usePermissions } from '@/hooks/usePermissions';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { useChannels } from '../context/channels-context';
import { useDeleteUpstreamAccount, useSaveUpstreamAccount } from '../data/channel-accounts';
import { useRefreshChannelUpstreams, verifyUpstreamAccount } from '../data/channel-upstream';
import type { ChannelUpstreamState, UpstreamAccount } from '../data/schema';
import { ChannelCredentialActions } from './channel-credential-actions';
import { useBalanceLabel } from './channel-upstream-cell';

function AccountForm({
  state,
  account,
  onSaved,
  onCancel,
}: {
  state: ChannelUpstreamState;
  account?: UpstreamAccount;
  onSaved: (ref: string) => void;
  onCancel?: () => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState(account?.name ?? '');
  const [provider, setProvider] = useState<'NEW_API' | 'SUB2_API'>(
    account?.provider ?? (state.provider === 'SUB2_API' ? 'SUB2_API' : 'NEW_API')
  );
  const [accountId, setAccountId] = useState(account?.accountId?.toString() ?? '');
  const [token, setToken] = useState('');
  const [replacing, setReplacing] = useState(!account);
  const [verifying, setVerifying] = useState(false);
  const [container, setContainer] = useState<HTMLFormElement | null>(null);
  const save = useSaveUpstreamAccount(state.channelId);
  const refresh = useRefreshChannelUpstreams();
  const verify = async () => {
    if (!account) return;
    setVerifying(true);
    try {
      const result = await verifyUpstreamAccount(state.channelId, account.ref);
      toast[result.success ? 'success' : 'error'](
        result.success
          ? t('channels.accounts.verified')
          : t(`channels.upstream.errors.${result.errorCode}`, { defaultValue: t('channels.upstream.verifyFailed') })
      );
      if (result.success) refresh.mutate({ channelIds: [state.channelId], force: true, includeDisabled: true });
    } catch {
      toast.error(t('channels.upstream.verifyFailed'));
    } finally {
      setVerifying(false);
    }
  };
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    try {
      const result = await save.mutateAsync({
        ref: account?.ref,
        name,
        provider,
        ...(token.trim() ? { accountToken: token.trim() } : {}),
        ...(accountId ? { accountId: Number(accountId) } : {}),
      });
      setToken('');
      setReplacing(false);
      save.reset();
      onSaved(account?.ref ?? result.accounts.at(-1)!.ref);
    } catch {
      save.reset();
    }
  };
  return (
    <form ref={setContainer} onSubmit={submit} className='flex flex-col gap-4'>
      <div className='flex flex-col gap-2'>
        <Label htmlFor={`account-name-${account?.ref ?? 'new'}`}>{t('channels.accounts.name')}</Label>
        <Input id={`account-name-${account?.ref ?? 'new'}`} required value={name} onChange={(event) => setName(event.target.value)} />
      </div>
      {!account && (
        <div className='flex flex-col gap-2'>
          <Label>{t('channels.accounts.provider')}</Label>
          <Select value={provider} onValueChange={(value) => setProvider(value as typeof provider)} disabled={state.provider !== 'UNKNOWN'}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent container={container}>
              <SelectGroup>
                <SelectItem value='NEW_API'>New API</SelectItem>
                <SelectItem value='SUB2_API'>Sub2API</SelectItem>
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>
      )}
      {provider === 'NEW_API' ? (
        <>
          <div className='flex flex-col gap-2'>
            <Label htmlFor={`account-id-${account?.ref ?? 'new'}`}>{t('channels.upstream.accountID')}</Label>
            <Input
              id={`account-id-${account?.ref ?? 'new'}`}
              type='number'
              min='1'
              step='1'
              readOnly={!!account?.accountId}
              value={accountId}
              onChange={(event) => setAccountId(event.target.value)}
            />
            <p className='text-muted-foreground text-xs'>{t('channels.upstream.accountIDHint')}</p>
          </div>
          <div className='flex flex-col gap-2'>
            <Label htmlFor={`account-token-${account?.ref ?? 'new'}`}>
              {t(account ? 'channels.accounts.savedToken' : 'channels.upstream.accountToken')}
            </Label>
            {account?.hasToken && !replacing ? (
              <div className='rounded-md border px-3 py-1'>
                <ChannelCredentialActions channelId={state.channelId} accountRef={account.ref} />
              </div>
            ) : (
              <Input
                id={`account-token-${account?.ref ?? 'new'}`}
                type='password'
                autoComplete='new-password'
                required={!account}
                value={token}
                onChange={(event) => setToken(event.target.value)}
              />
            )}
            <p className='text-muted-foreground text-xs'>{t('channels.accounts.tokenHint')}</p>
          </div>
          {account && (
            <div className='flex flex-wrap gap-2'>
              <Button type='button' variant='outline' disabled={verifying || save.isPending} onClick={() => void verify()}>
                {t(verifying ? 'channels.upstream.verifying' : 'channels.accounts.verifyConnection')}
              </Button>
              <Button
                type='button'
                variant='ghost'
                onClick={() => {
                  setReplacing(!replacing);
                  setToken('');
                }}
              >
                {t(replacing ? 'common.buttons.cancel' : 'channels.accounts.replaceToken')}
              </Button>
            </div>
          )}
        </>
      ) : (
        <Alert>
          <AlertDescription>{t('channels.accounts.manualHint')}</AlertDescription>
        </Alert>
      )}
      <div className='flex justify-end gap-2'>
        {onCancel && (
          <Button type='button' variant='outline' onClick={onCancel}>
            {t('common.buttons.cancel')}
          </Button>
        )}
        <Button type='submit' disabled={save.isPending || verifying}>
          {t('common.buttons.save')}
        </Button>
      </div>
    </form>
  );
}

export function ChannelAccountsPanel({ state }: { state: ChannelUpstreamState }) {
  const { t } = useTranslation();
  const { channelPermissions } = usePermissions();
  const { setOpen } = useChannels();
  const formatBalance = useBalanceLabel();
  const [expanded, setExpanded] = useState<string | null>(state.accounts[0]?.ref ?? null);
  const [adding, setAdding] = useState(false);
  const remove = useDeleteUpstreamAccount(state.channelId);
  return (
    <div className='flex flex-col gap-3 pt-2'>
      <div className='flex items-center justify-between gap-2'>
        <p className='text-sm'>
          {t('channels.accounts.summary', { accounts: state.accounts.length, keys: state.keys.length })}
          {state.balanceSummary.balances.length > 0 && ` · ${state.balanceSummary.balances.map(formatBalance).join(' / ')}`}
          {!state.balanceSummary.complete &&
            ` · ${t(state.balanceSummary.balances.length ? 'channels.accounts.partial' : 'channels.accounts.notProvided')}`}
        </p>
        {channelPermissions.canWrite && (
          <Button variant='outline' size='sm' onClick={() => setAdding(true)}>
            <Plus />
            {t('channels.accounts.add')}
          </Button>
        )}
      </div>
      {adding && (
        <div className='rounded-lg border p-4'>
          <AccountForm
            state={state}
            onSaved={(ref) => {
              setAdding(false);
              setExpanded(ref);
            }}
            onCancel={() => setAdding(false)}
          />
        </div>
      )}
      {state.accounts.map((account) => (
        <section key={account.ref} className='rounded-lg border'>
          <button
            type='button'
            className='flex w-full items-center gap-2 p-3 text-left'
            aria-expanded={expanded === account.ref}
            onClick={() => setExpanded(expanded === account.ref ? null : account.ref)}
          >
            {expanded === account.ref ? <ChevronDown className='size-4' /> : <ChevronRight className='size-4' />}
            <strong className='min-w-0 truncate'>{account.name}</strong>
            <Badge variant='outline'>
              {t(
                account.provider === 'SUB2_API'
                  ? 'channels.accounts.manual'
                  : account.verifiedAt
                    ? 'channels.accounts.verified'
                    : 'channels.accounts.notVerified'
              )}
            </Badge>
            <span className='text-muted-foreground text-xs'>{t('channels.accounts.keyCount', { count: account.keyCount })}</span>
            <span className='ml-auto shrink-0 tabular-nums'>
              {account.balance ? formatBalance(account.balance) : t('channels.accounts.notProvided')}
            </span>
          </button>
          {expanded === account.ref && (
            <div className='flex flex-col gap-4 border-t p-4'>
              {channelPermissions.canWrite && (
                <AccountForm
                  key={account.ref}
                  state={state}
                  account={account}
                  onSaved={() => toast.success(t('channels.upstream.saved'))}
                />
              )}
              <div className='flex items-center justify-between'>
                <span className='text-sm font-medium'>{t('channels.accounts.linkedKeys', { count: account.keyCount })}</span>
                {channelPermissions.canWrite && (
                  <Button variant='link' size='sm' onClick={() => setOpen('keyManagement')}>
                    {t('channels.accounts.manageLinks')}
                  </Button>
                )}
              </div>
              <div className='flex flex-col gap-1'>
                {state.keys
                  .filter((key) => key.accountRef === account.ref)
                  .map((key) => (
                    <div key={key.keyRef} className='flex items-center gap-3 rounded-md border px-3 py-2 text-sm'>
                      <span className='min-w-0 truncate'>{key.remark || key.label}</span>
                      {key.remark && <span className='text-muted-foreground font-mono text-xs'>{key.label}</span>}
                      <Badge variant='outline'>{t(key.disabled ? 'channels.upstream.keyDisabled' : 'channels.accounts.enabled')}</Badge>
                    </div>
                  ))}
              </div>
              <p className='text-muted-foreground text-xs'>{t('channels.accounts.sharedHint')}</p>
              {account.updatedAt && (
                <p className='text-muted-foreground text-xs'>
                  {t('channels.upstream.updatedAt', { time: new Date(account.updatedAt).toLocaleString() })}
                  {!account.complete && ` · ${t('channels.accounts.previousResult')}`}
                </p>
              )}
              {channelPermissions.canWrite && (
                <Popover>
                  <PopoverTrigger asChild>
                    <Button variant='ghost' size='sm' className='text-destructive self-start'>
                      <Trash2 />
                      {t('channels.accounts.remove')}
                    </Button>
                  </PopoverTrigger>
                  <PopoverContent>
                    <p className='mb-3 text-sm'>{t('channels.accounts.removeHint')}</p>
                    <Button variant='destructive' size='sm' disabled={remove.isPending} onClick={() => remove.mutate(account.ref)}>
                      {t('common.buttons.confirm')}
                    </Button>
                  </PopoverContent>
                </Popover>
              )}
            </div>
          )}
        </section>
      ))}
      {state.accounts.length === 0 && !adding && (
        <Alert>
          <AlertDescription>{t('channels.accounts.empty')}</AlertDescription>
        </Alert>
      )}
      <p className='text-muted-foreground text-xs'>{t('channels.accounts.callingUnaffected')}</p>
    </div>
  );
}
