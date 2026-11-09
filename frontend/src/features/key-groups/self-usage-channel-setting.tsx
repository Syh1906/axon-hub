import { useId } from 'react';
import { useTranslation } from 'react-i18next';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';

export function SelfUsageChannelSetting({ checked, onCheckedChange }: { checked: boolean; onCheckedChange: (checked: boolean) => void }) {
  const { t } = useTranslation();
  const id = useId();
  return (
    <div className='flex flex-col gap-2 rounded-md border p-3'>
      <div className='flex items-center justify-between gap-4'>
        <Label htmlFor={id}>{t('kg.field.showChannelInSelfUsage')}</Label>
        <Switch id={id} checked={checked} onCheckedChange={onCheckedChange} aria-describedby={`${id}-help`} />
      </div>
      <p id={`${id}-help`} className='text-muted-foreground text-xs'>
        {t('kg.selfUsageChannelHelp')}
      </p>
    </div>
  );
}
