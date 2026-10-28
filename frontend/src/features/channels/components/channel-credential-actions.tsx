import { useEffect, useRef, useState } from 'react';
import { Copy, Eye, EyeOff } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { copyTextToClipboard } from '@/lib/clipboard';
import { Button } from '@/components/ui/button';
import { revealChannelCredential } from '../data/channel-accounts';

export function ChannelCredentialActions({
  channelId,
  keyRef,
  accountRef,
  label = '••••••••',
}: {
  channelId: string;
  keyRef?: string;
  accountRef?: string;
  label?: string;
}) {
  const { t } = useTranslation();
  const [value, setValue] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const active = useRef(true);
  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
    };
  }, []);
  const read = async (copy: boolean) => {
    if (!copy && value !== null) {
      setValue(null);
      return;
    }
    setPending(true);
    try {
      const secret = value ?? (await revealChannelCredential(channelId, { keyRef, accountRef }));
      if (!active.current) return;
      if (copy) {
        await copyTextToClipboard(secret);
        toast.success(t('channels.accounts.copied'));
      } else setValue(secret);
    } catch {
      if (active.current) toast.error(t('channels.accounts.readFailed'));
    } finally {
      if (active.current) setPending(false);
    }
  };
  return (
    <div className='flex min-w-0 items-center gap-1'>
      <span className='min-w-0 flex-1 font-mono text-xs break-all'>{value ?? label}</span>
      <Button
        type='button'
        variant='ghost'
        size='icon'
        className='size-7 shrink-0'
        disabled={pending}
        onClick={() => void read(false)}
        aria-label={t(value === null ? 'channels.accounts.show' : 'channels.accounts.hide')}
      >
        {value === null ? <Eye /> : <EyeOff />}
      </Button>
      <Button
        type='button'
        variant='ghost'
        size='icon'
        className='size-7 shrink-0'
        disabled={pending}
        onClick={() => void read(true)}
        aria-label={t('channels.accounts.copy')}
      >
        <Copy />
      </Button>
    </div>
  );
}
