import { useState } from 'react';
import { Copy, Eye, EyeOff } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { extractNumberID } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { useManagementAPI } from './data';

export function Credential({ id }: { id: string }) {
  const { t } = useTranslation();
  const api = useManagementAPI();
  const [value, setValue] = useState<string>();
  const [visible, setVisible] = useState(false);
  const [busy, setBusy] = useState(false);
  async function access(copy: boolean) {
    setBusy(true);
    try {
      const secret = value ?? (await api.credentials([Number(extractNumberID(id))]))[0]?.key;
      if (!secret) throw new Error(t('kg.resultsUnavailable'));
      if (copy) {
        await navigator.clipboard.writeText(secret);
        toast.success(t('kg.copied'));
      } else {
        setValue(secret);
        setVisible(true);
      }
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className='flex max-w-60 items-center gap-1'>
      <code className='bg-muted max-w-32 truncate rounded px-2 py-1'>{visible ? value : '••••••••'}</code>
      <Button
        variant='ghost'
        size='icon'
        disabled={busy}
        aria-label={t(visible ? 'kg.hide' : 'kg.reveal')}
        onClick={() => (visible ? setVisible(false) : access(false))}
      >
        {visible ? <EyeOff className='size-4' /> : <Eye className='size-4' />}
      </Button>
      <Button variant='ghost' size='icon' disabled={busy} aria-label={t('kg.copyKey')} onClick={() => access(true)}>
        <Copy className='size-4' />
      </Button>
    </div>
  );
}
