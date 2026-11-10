import { useId } from 'react';
import { useTranslation } from 'react-i18next';
import { Switch } from '@/components/ui/switch';

export function VersionOrderEditor({ checked, onChange }: { checked?: boolean; onChange: (value: boolean) => void }) {
  const { t } = useTranslation();
  const id = useId();
  const text = (key: string) => t(`models.dialogs.association.versionOrder.${key}`);
  return (
    <div className='space-y-2 text-xs'>
      <div className='flex flex-wrap items-center gap-2'>
        <Switch id={id} checked={Boolean(checked)} onCheckedChange={onChange} aria-describedby={`${id}-description`} />
        <label htmlFor={id} className='font-medium'>
          {text('label')}
        </label>
        <span className='text-muted-foreground'>{text('scope')}</span>
      </div>
      <p id={`${id}-description`} className='text-muted-foreground'>
        {text('summary')}
      </p>
      <details>
        <summary className='cursor-pointer py-1'>{text('help')}</summary>
        <div className='space-y-2 py-2'>
          <p>{text('capture')}</p>
          <code className='bg-muted block overflow-x-auto rounded p-2'>{'^gpt-(?<version>[0-9]+(?:[.][0-9]+)*)-sol$'}</code>
          <p>{text('family')}</p>
          <p>{text('catalog')}</p>
          <p>{text('retry')}</p>
        </div>
      </details>
    </div>
  );
}
