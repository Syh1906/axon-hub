import { useTranslation } from 'react-i18next';
import { loadErrorKey, loadErrorRequestId } from '@/lib/load-error';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';

export function QueryLoadAlert({ error, hasData, retrying, onRetry, label }: {
  error: unknown;
  hasData: boolean;
  retrying: boolean;
  onRetry: () => void;
  label?: string;
}) {
  const { t } = useTranslation();
  if (!error) return null;
  const id = loadErrorRequestId(error);
  return (
    <Alert variant={hasData ? 'default' : 'destructive'} className='my-2 shrink-0'>
      <AlertTitle>{label ?? t(hasData ? 'common.loadFailure.stale' : 'common.loadFailure.unavailable')}</AlertTitle>
      <AlertDescription>
        <p>{t(loadErrorKey(error))}</p>
        {id && <p className='break-all text-xs'>{t('common.loadFailure.requestId', { id })}</p>}
        <Button variant='outline' size='sm' disabled={retrying} onClick={onRetry}>
          {t(retrying ? 'common.loadFailure.retrying' : 'common.buttons.retry')}
        </Button>
      </AlertDescription>
    </Alert>
  );
}
