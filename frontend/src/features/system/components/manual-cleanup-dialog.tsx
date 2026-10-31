import { useEffect, useMemo, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Loader2, Play } from 'lucide-react';
import { nanoid } from 'nanoid';
import { useTranslation } from 'react-i18next';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { CLEANUP_RESOURCES, cleanupIsActive, useCleanupTask, useStartCleanupTask, useStorageOverview } from '../data/maintenance';
import { previewGcCleanup, type CleanupOption, type TriggerGcCleanupInput } from '../data/system';

export function ManualCleanupDialog({ options, disabled }: { options: CleanupOption[]; disabled: boolean }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [selection, setSelection] = useState<Record<string, { selected: boolean; days: number }>>({});
  const [debounced, setDebounced] = useState<TriggerGcCleanupInput>({ requestsCleanupDays: 0, usageLogsCleanupDays: 0 });
  const key = useRef('');
  const submitted = useRef<TriggerGcCleanupInput | null>(null);
  const task = useCleanupTask();
  const storage = useStorageOverview();
  const start = useStartCleanupTask();
  const active = cleanupIsActive(task.data);
  const input = useMemo(() => {
    const value: TriggerGcCleanupInput = { requestsCleanupDays: 0, usageLogsCleanupDays: 0 };
    for (const resource of CLEANUP_RESOURCES)
      value[resource.field] = selection[resource.type]?.selected ? selection[resource.type].days : 0;
    return value;
  }, [selection]);
  const selected = Object.values(selection).some((item) => item.selected);
  const invalid = Object.values(selection).some(
    (item) => item.selected && (!Number.isInteger(item.days) || item.days < 1 || item.days > 36500)
  );
  useEffect(() => {
    if (!open || invalid) return;
    const timeout = setTimeout(() => setDebounced(input), 400);
    return () => clearTimeout(timeout);
  }, [open, invalid, input]);
  const preview = useQuery({
    queryKey: ['cleanupPreview', debounced],
    queryFn: ({ signal }) => previewGcCleanup(debounced, signal),
    enabled: open && selected && !invalid,
    staleTime: 15000,
    gcTime: 60000,
    retry: false,
  });
  const currentPreview = JSON.stringify(input) === JSON.stringify(debounced);
  function changeOpen(value: boolean) {
    if (value) {
      setSelection(
        Object.fromEntries(
          CLEANUP_RESOURCES.map((resource) => {
            const submittedDays = submitted.current?.[resource.field] ?? 0;
            return [
              resource.type,
              {
                selected: submitted.current ? submittedDays > 0 : resource.type.endsWith('bodies') || resource.type === 'response_chunks',
                days:
                  submittedDays > 0
                    ? submittedDays
                    : (options.find((option) => option.resourceType === resource.type)?.cleanupDays ?? resource.days),
              },
            ];
          })
        )
      );
      if (!submitted.current) key.current = nanoid();
      start.reset();
    }
    setOpen(value);
  }
  async function submit() {
    const request = submitted.current ?? { ...input, submissionKey: key.current };
    submitted.current = request;
    try {
      await start.mutateAsync(request);
      submitted.current = null;
      setOpen(false);
    } catch {
      /* Query the server state before retrying the same submission. */
    }
  }
  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogTrigger asChild>
        <Button variant='outline' size='sm' disabled={disabled || active}>
          <Play />
          {t('system.storage.policy.runCleanupManualTitle')}
        </Button>
      </DialogTrigger>
      <DialogContent className='flex max-h-[85dvh] flex-col overflow-hidden sm:max-w-4xl'>
        <DialogHeader>
          <DialogTitle>{t('system.storage.policy.runCleanupManualTitle')}</DialogTitle>
          <DialogDescription>{t('system.maintenance.manualDescription')}</DialogDescription>
        </DialogHeader>
        <div className='flex min-h-0 flex-col gap-4 overflow-auto'>
          <Alert>
            <AlertDescription>{t('system.maintenance.estimateHint')}</AlertDescription>
          </Alert>
          <details className='rounded-md border p-3 text-sm'>
            <summary className='cursor-pointer'>{t('system.maintenance.tableSizes')}</summary>
            <p className='text-muted-foreground mt-2 text-xs'>{t('system.maintenance.occupancyHint')}</p>
            {storage.data?.tables.length ? (
              <div className='mt-2 grid gap-2 sm:grid-cols-2'>
                {storage.data.tables.map((table) => (
                  <div key={table.name} className='flex justify-between gap-3'>
                    <span className='truncate'>{table.name}</span>
                    <span className='shrink-0 tabular-nums'>
                      {(table.bytes / 1024 ** 2).toLocaleString(undefined, { maximumFractionDigits: 2 })} MiB
                    </span>
                  </div>
                ))}
              </div>
            ) : (
              <p className='text-muted-foreground mt-2 text-xs'>{t('system.maintenance.tableSizesUnavailable')}</p>
            )}
          </details>
          <Table className='min-w-[650px]'>
            <TableHeader>
              <TableRow>
                <TableHead className='w-10' />
                <TableHead>{t('system.maintenance.resource')}</TableHead>
                <TableHead className='w-28'>{t('system.storage.policy.runCleanupRetentionLabel')}</TableHead>
                <TableHead>{t('system.maintenance.estimate')}</TableHead>
                <TableHead>{t('system.maintenance.impact')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {CLEANUP_RESOURCES.map((resource) => {
                const value = selection[resource.type];
                const item = currentPreview && preview.data?.find((item) => item.resourceType === resource.type);
                return (
                  <TableRow key={resource.type}>
                    <TableCell>
                      <Checkbox
                        checked={value?.selected ?? false}
                        disabled={start.isPending || !!submitted.current || active}
                        aria-label={t(`system.storage.policy.resourceTypes.${resource.type}`)}
                        onCheckedChange={(checked) =>
                          setSelection((old) => ({ ...old, [resource.type]: { ...old[resource.type], selected: checked === true } }))
                        }
                      />
                    </TableCell>
                    <TableCell className='font-medium'>{t(`system.storage.policy.resourceTypes.${resource.type}`)}</TableCell>
                    <TableCell>
                      <Input
                        aria-label={t(`system.storage.policy.resourceTypes.${resource.type}`) + t('system.storage.policy.cleanupDays')}
                        type='number'
                        min={1}
                        max={36500}
                        value={value?.days ?? resource.days}
                        disabled={!value?.selected || start.isPending || !!submitted.current || active}
                        onChange={(e) =>
                          setSelection((old) => ({ ...old, [resource.type]: { ...old[resource.type], days: Number(e.target.value) } }))
                        }
                      />
                    </TableCell>
                    <TableCell className='whitespace-nowrap tabular-nums'>
                      {!value?.selected ? (
                        '—'
                      ) : preview.isFetching || !currentPreview ? (
                        <Loader2 className='size-4 animate-spin' />
                      ) : item ? (
                        `${item.countComplete ? '' : '≥ '}${item.estimatedCount.toLocaleString()}`
                      ) : (
                        t('system.maintenance.unknown')
                      )}
                    </TableCell>
                    <TableCell className='text-muted-foreground max-w-64 text-xs whitespace-normal'>
                      {t(`system.maintenance.impact.${resource.type}`)}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
          <p className='text-muted-foreground text-xs'>{t('system.maintenance.protectionHint')}</p>
          {invalid && <p className='text-destructive text-sm'>{t('system.maintenance.invalidDays')}</p>}
          {preview.isError && <p className='text-muted-foreground text-sm'>{t('system.storage.policy.runCleanupPreviewFailed')}</p>}
          {(start.isError || active || (!!submitted.current && !start.isPending)) && (
            <Alert variant={active ? 'default' : 'destructive'}>
              <AlertDescription>{t(active ? 'system.maintenance.existingTask' : 'system.maintenance.startError')}</AlertDescription>
            </Alert>
          )}
        </div>
        <DialogFooter>
          <Button variant='outline' onClick={() => setOpen(false)}>
            {t('system.storage.policy.runCleanupCancel')}
          </Button>
          <Button disabled={!selected || invalid || start.isPending || active} onClick={submit}>
            {start.isPending && <Loader2 className='animate-spin' />}
            {t('system.maintenance.startSelected')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
