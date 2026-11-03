import { Trash2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { extractNumberID } from '@/lib/utils';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { ConfirmDialog } from '@/components/confirm-dialog';
import { useDeleteApiKeys } from '../data/apikeys';

export type DeleteKeyTarget = { id: string; name: string; displayName?: string };

export function DeleteKeysDialog({
  keys,
  open,
  onOpenChange,
  onDeleted,
}: {
  keys: DeleteKeyTarget[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onDeleted: () => void;
}) {
  const { t } = useTranslation();
  const deletion = useDeleteApiKeys();
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={(next) => {
        if (!deletion.isPending) onOpenChange(next);
      }}
      title={
        <span className='flex items-center gap-2'>
          <Trash2 className='text-destructive size-5' />
          {t('apikeys.delete.title', { count: keys.length })}
        </span>
      }
      desc={
        <div className='flex flex-col gap-3'>
          <p>{t('apikeys.delete.description')}</p>
          <p className='text-muted-foreground text-xs'>{t('apikeys.delete.inFlight')}</p>
          {deletion.isError && (
            <Alert variant='destructive'>
              <AlertDescription>{t('apikeys.delete.failed')}</AlertDescription>
            </Alert>
          )}
        </div>
      }
      cancelBtnText={t('common.buttons.cancel')}
      confirmText={t('apikeys.delete.confirm')}
      destructive
      disabled={!keys.length || deletion.isSuccess}
      isLoading={deletion.isPending}
      handleConfirm={async () => {
        try {
          await deletion.mutateAsync(keys.map((key) => key.id));
          onDeleted();
        } catch {
          // Keep the selection and dialog visible; never retry an uncertain delete.
        }
      }}
    >
      <ul className='bg-muted/30 max-h-48 overflow-y-auto rounded-md border p-3 text-sm'>
        {keys.map((key) => (
          <li key={key.id} className='flex items-start justify-between gap-3 py-1'>
            <span className='min-w-0 break-words'>{key.displayName || key.name}</span>
            <span className='text-muted-foreground shrink-0'>#{extractNumberID(key.id)}</span>
          </li>
        ))}
      </ul>
    </ConfirmDialog>
  );
}
