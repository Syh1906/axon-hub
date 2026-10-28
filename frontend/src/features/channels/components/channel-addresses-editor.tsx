import { useState } from 'react';
import { Plus, RefreshCw, Trash2, ChevronLeft } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { probeChannelAddress } from '../data/channel-accounts';
import type { ChannelAddress } from '../data/schema';

export function ChannelAddressesEditor({
  addresses,
  failover,
  channelId,
  onChange,
  onFailoverChange,
  onClose,
}: {
  addresses: ChannelAddress[];
  failover: boolean;
  channelId?: string;
  onChange: (addresses: ChannelAddress[]) => void;
  onFailoverChange: (enabled: boolean) => void;
  onClose?: () => void;
}) {
  const { t } = useTranslation();
  const [results, setResults] = useState<
    Record<string, { url: string; reachable: boolean; latencyMs: number; statusCode: number; errorCode: string }>
  >({});
  const [pending, setPending] = useState<string[]>([]);
  const update = (id: string, patch: Partial<ChannelAddress>) =>
    onChange(addresses.map((address) => (address.id === id ? { ...address, ...patch } : address)));
  const test = async (address: ChannelAddress) => {
    setPending((current) => [...current, address.id]);
    try {
      const result = await probeChannelAddress(address.url, channelId);
      setResults((current) => ({ ...current, [address.id]: { ...result, url: address.url } }));
    } catch {
      setResults((current) => ({
        ...current,
        [address.id]: { url: address.url, reachable: false, latencyMs: 0, statusCode: 0, errorCode: 'QUERY_FAILED' },
      }));
    } finally {
      setPending((current) => current.filter((id) => id !== address.id));
    }
  };
  return (
    <div className='flex h-full min-h-0 flex-col gap-3'>
      <div className='flex items-center justify-between'>
        <h3 className='text-sm font-semibold'>{t('channels.addresses.title', { count: addresses.length })}</h3>
        {onClose && (
          <Button type='button' variant='ghost' size='icon' className='size-7' onClick={onClose} aria-label={t('common.buttons.close')}>
            <ChevronLeft />
          </Button>
        )}
      </div>
      <div className='flex items-center gap-2'>
        <Switch id='address-failover' checked={failover} onCheckedChange={onFailoverChange} />
        <Label htmlFor='address-failover'>{t('channels.addresses.failover')}</Label>
      </div>
      <p className='text-muted-foreground text-xs'>{t('channels.addresses.sameUpstream')}</p>
      <div className='flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto pr-2'>
        {addresses.map((address) => {
          const result = results[address.id]?.url === address.url ? results[address.id] : undefined;
          const lastEnabled = address.enabled && addresses.filter((item) => item.enabled).length === 1;
          return (
            <div key={address.id} className='bg-muted/20 flex flex-col gap-2 rounded-md border p-3'>
              <div className='flex items-center gap-2'>
                <Input
                  aria-label={t('channels.addresses.name')}
                  value={address.name}
                  className='h-8 text-xs'
                  onChange={(event) => update(address.id, { name: event.target.value })}
                />
                <Switch
                  aria-label={t('channels.accounts.enabled')}
                  disabled={lastEnabled}
                  checked={address.enabled}
                  onCheckedChange={(enabled) => update(address.id, { enabled })}
                />
              </div>
              <Input
                aria-label={t('channels.addresses.url')}
                type='url'
                value={address.url}
                placeholder='https://api.example.com/v1'
                className='h-8 font-mono text-xs'
                onChange={(event) => update(address.id, { url: event.target.value })}
              />
              <div className='flex items-center gap-2'>
                <Label htmlFor={`priority-${address.id}`} className='shrink-0 text-xs'>
                  {t('channels.addresses.priority')}
                </Label>
                <Input
                  id={`priority-${address.id}`}
                  type='number'
                  step='1'
                  className='h-8 w-20 text-xs'
                  value={address.priority}
                  onChange={(event) => update(address.id, { priority: Number(event.target.value) })}
                />
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  disabled={pending.includes(address.id) || !address.url}
                  onClick={() => void test(address)}
                >
                  <RefreshCw className={pending.includes(address.id) ? 'animate-spin' : ''} />
                  {t('channels.addresses.test')}
                </Button>
                <Button
                  type='button'
                  variant='ghost'
                  size='icon'
                  className='text-destructive size-7'
                  disabled={addresses.length <= 1 || lastEnabled}
                  aria-label={t('channels.addresses.remove')}
                  onClick={() => onChange(addresses.filter((item) => item.id !== address.id))}
                >
                  <Trash2 />
                </Button>
              </div>
              {result && (
                <p className={result.reachable ? 'text-muted-foreground text-xs' : 'text-destructive text-xs'}>
                  {result.reachable
                    ? t('channels.addresses.reachable', { ms: result.latencyMs, status: result.statusCode })
                    : t(`channels.upstream.errors.${result.errorCode}`, { defaultValue: t('channels.addresses.failed') })}
                </p>
              )}
            </div>
          );
        })}
      </div>
      <div className='flex gap-2'>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() =>
            onChange([
              ...addresses,
              {
                id: crypto.randomUUID(),
                name: t('channels.addresses.backup', { count: addresses.length }),
                url: '',
                priority: (addresses.at(-1)?.priority ?? 0) + 10,
                enabled: true,
              },
            ])
          }
        >
          <Plus />
          {t('channels.addresses.add')}
        </Button>
        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={pending.length > 0}
          onClick={async () => {
            for (const address of addresses) await test(address);
          }}
        >
          {t('channels.addresses.testAll')}
        </Button>
      </div>
      <p className='text-muted-foreground text-xs'>{t('channels.addresses.priorityHint')}</p>
      <p className='text-muted-foreground text-xs'>{t('channels.addresses.testHint')}</p>
    </div>
  );
}
