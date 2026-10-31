import { useEffect, useState } from 'react';
import { Loader2, Save } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { usePermissions } from '@/hooks/usePermissions';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { Switch } from '@/components/ui/switch';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { useStoragePolicy, useUpdateStoragePolicy, type StoragePolicy } from '../data/system';
import { CleanupTaskProgress } from './cleanup-task-progress';
import { MaintenanceSettingsDialog } from './maintenance-settings-dialog';
import { ManualCleanupDialog } from './manual-cleanup-dialog';

export function StoragePolicySettings() {
  const { t } = useTranslation();
  const query = useStoragePolicy();
  const update = useUpdateStoragePolicy();
  const { hasSystemScope } = usePermissions();
  const [draft, setDraft] = useState<StoragePolicy | null>(null);
  const [dirty, setDirty] = useState(false);
  useEffect(() => {
    if (query.data && !dirty) setDraft(query.data);
  }, [query.data, dirty]);
  const change = (next: StoragePolicy) => {
    setDraft(next);
    setDirty(true);
  };
  if (query.isLoading) return <Skeleton className='h-64 w-full' />;
  if (!draft) return <p className='text-destructive'>{t('common.errors.internalServerError')}</p>;
  const writable = hasSystemScope('write_settings');
  const disabled = update.isPending || !writable;
  const invalid = draft.cleanupOptions.some(
    (option) => !Number.isInteger(option.cleanupDays) || option.cleanupDays < 1 || option.cleanupDays > 36500
  );
  const toggles = [
    { field: 'storeChunks', key: 'storeChunks' },
    { field: 'livePreview', key: 'livePreview' },
    { field: 'storeRequestBody', key: 'storeRequestBody' },
    { field: 'storeResponseBody', key: 'storeResponseBody' },
  ] as const;
  async function save() {
    if (!draft) return;
    try {
      await update.mutateAsync(draft);
      setDirty(false);
    } catch {
      /* The mutation reports the error. */
    }
  }
  return (
    <>
      <CleanupTaskProgress />
      <Card>
        <CardHeader className='flex flex-row flex-wrap items-start justify-between gap-3'>
          <div className='flex flex-col gap-2'>
            <CardTitle>{t('system.storage.policy.title')}</CardTitle>
            <CardDescription>{t('system.storage.policy.description')}</CardDescription>
          </div>
          <ManualCleanupDialog options={draft.cleanupOptions} disabled={disabled} />
        </CardHeader>
        <CardContent className='flex flex-col gap-5'>
          {toggles.map(({ field, key }) => (
            <div className='flex items-center justify-between gap-4' key={field}>
              <div className='flex flex-col gap-1'>
                <Label htmlFor={`storage-policy-${field}`}>{t(`system.storage.policy.${key}.label`)}</Label>
                <p className='text-muted-foreground text-sm'>{t(`system.storage.policy.${key}.description`)}</p>
              </div>
              <Switch
                id={`storage-policy-${field}`}
                checked={draft[field]}
                disabled={disabled}
                onCheckedChange={(checked) => change({ ...draft, [field]: checked })}
              />
            </div>
          ))}
          <div className='flex flex-wrap items-center justify-between gap-3'>
            <div>
              <h3 className='font-medium'>{t('system.maintenance.autoPolicy')}</h3>
              <p className='text-muted-foreground text-sm'>{t('system.maintenance.autoPolicyHint')}</p>
            </div>
            <MaintenanceSettingsDialog
              settings={draft.maintenance}
              disabled={disabled}
              onChange={(maintenance) => change({ ...draft, maintenance })}
            />
          </div>
          <div className='overflow-x-auto rounded-md border'>
            <Table className='min-w-[570px]'>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('system.maintenance.resource')}</TableHead>
                  <TableHead>{t('system.maintenance.impact')}</TableHead>
                  <TableHead className='w-28'>{t('system.storage.policy.runCleanupRetentionLabel')}</TableHead>
                  <TableHead className='w-20'>{t('system.maintenance.enabled')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {draft.cleanupOptions.map((option) => (
                  <TableRow key={option.resourceType}>
                    <TableCell className='font-medium'>
                      {t(`system.storage.policy.resourceTypes.${option.resourceType}`, { defaultValue: option.resourceType })}
                    </TableCell>
                    <TableCell className='text-muted-foreground max-w-80 text-xs whitespace-normal'>
                      {t(`system.maintenance.impact.${option.resourceType}`, { defaultValue: '' })}
                    </TableCell>
                    <TableCell>
                      <Input
                        type='number'
                        aria-label={
                          t(`system.storage.policy.resourceTypes.${option.resourceType}`) + t('system.storage.policy.cleanupDays')
                        }
                        min={1}
                        max={36500}
                        value={option.cleanupDays}
                        disabled={disabled}
                        onChange={(e) =>
                          change({
                            ...draft,
                            cleanupOptions: draft.cleanupOptions.map((item) =>
                              item.resourceType === option.resourceType ? { ...item, cleanupDays: Number(e.target.value) } : item
                            ),
                          })
                        }
                      />
                    </TableCell>
                    <TableCell>
                      <Switch
                        aria-label={t(`system.storage.policy.resourceTypes.${option.resourceType}`)}
                        checked={option.enabled}
                        disabled={disabled}
                        onCheckedChange={(enabled) =>
                          change({
                            ...draft,
                            cleanupOptions: draft.cleanupOptions.map((item) =>
                              item.resourceType === option.resourceType ? { ...item, enabled } : item
                            ),
                          })
                        }
                      />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          {invalid && <p className='text-destructive text-sm'>{t('system.maintenance.invalidDays')}</p>}
          <div className='flex justify-end'>
            <Button size='sm' onClick={save} disabled={disabled || !dirty || invalid}>
              {update.isPending ? <Loader2 className='animate-spin' /> : <Save />}
              {t('system.buttons.save')}
            </Button>
          </div>
        </CardContent>
      </Card>
    </>
  );
}
