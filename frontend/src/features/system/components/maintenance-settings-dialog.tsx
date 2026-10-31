import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import type { MaintenanceSettings } from '../data/maintenance';

const FIELDS = [
  { key: 'batchSize', min: 1, max: 500 },
  { key: 'batchIntervalMs', min: 100, max: 60000 },
  { key: 'batchTimeoutSeconds', min: 1, max: 10 },
  { key: 'maxRunSeconds', min: 10, max: 3600 },
  { key: 'busyConnectionsPercent', min: 10, max: 90 },
  { key: 'diskWarningPercent', min: 1, max: 98 },
  { key: 'diskCriticalPercent', min: 2, max: 99 },
  { key: 'reserveDiskGiB', min: 0, max: 1000000 },
] as const;

export function MaintenanceSettingsDialog({
  settings,
  onChange,
  disabled,
}: {
  settings: MaintenanceSettings;
  onChange: (settings: MaintenanceSettings) => void;
  disabled: boolean;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(settings);
  const valid =
    FIELDS.every(
      ({ key, min, max }) =>
        Number.isFinite(draft[key]) && draft[key] >= min && draft[key] <= max && (key === 'reserveDiskGiB' || Number.isInteger(draft[key]))
    ) && draft.diskWarningPercent < draft.diskCriticalPercent;
  return (
    <Dialog
      open={open}
      onOpenChange={(value) => {
        if (value) setDraft({ ...settings });
        setOpen(value);
      }}
    >
      <DialogTrigger asChild>
        <Button variant='outline' size='sm' disabled={disabled}>
          {t('system.maintenance.configure')}
        </Button>
      </DialogTrigger>
      <DialogContent className='flex max-h-[85dvh] flex-col overflow-hidden'>
        <DialogHeader>
          <DialogTitle>{t('system.maintenance.configure')}</DialogTitle>
          <DialogDescription>{t('system.maintenance.limitsHint')}</DialogDescription>
        </DialogHeader>
        <div className='flex min-h-0 flex-col gap-4 overflow-auto'>
          {FIELDS.map(({ key, min, max }) => (
            <div className='grid gap-2' key={key}>
              <Label htmlFor={`maintenance-${key}`}>{t(`system.maintenance.settings.${key}`)}</Label>
              <Input
                id={`maintenance-${key}`}
                type='number'
                min={min}
                max={max}
                step={key === 'reserveDiskGiB' ? 'any' : 1}
                value={draft[key]}
                onChange={(event) => setDraft((value) => ({ ...value, [key]: Number(event.target.value) }))}
              />
            </div>
          ))}
          <div className='grid gap-2'>
            <Label htmlFor='maintenance-diskPath'>{t('system.maintenance.settings.diskPath')}</Label>
            <Input
              id='maintenance-diskPath'
              value={draft.diskPath}
              onChange={(event) => setDraft((value) => ({ ...value, diskPath: event.target.value }))}
            />
            <p className='text-muted-foreground text-xs'>{t('system.maintenance.diskPathHint')}</p>
          </div>
          {!valid && <p className='text-destructive text-sm'>{t('system.maintenance.invalidSettings')}</p>}
        </div>
        <DialogFooter>
          <Button variant='outline' onClick={() => setOpen(false)}>
            {t('system.storage.policy.runCleanupCancel')}
          </Button>
          <Button
            disabled={!valid}
            onClick={() => {
              onChange(draft);
              setOpen(false);
            }}
          >
            {t('system.maintenance.applyDraft')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
