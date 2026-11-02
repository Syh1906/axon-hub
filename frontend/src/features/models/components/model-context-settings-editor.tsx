import { type Control, useWatch } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { FormControl, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form';
import { Input } from '@/components/ui/input';
import { type CreateModelInput } from '../data/schema';

export function ModelContextSettingsEditor({ control }: { control: Control<CreateModelInput> }) {
  const { t } = useTranslation();
  const maximum = useWatch({ control, name: 'settings.contextSettings.maxContextWindow' });
  return (
    <section className='flex flex-col gap-3 rounded-md border p-4'>
      <p className='text-sm font-medium'>{t('modelContext.modelTitle')}</p>
      <p className='text-muted-foreground text-xs'>{t('modelContext.modelDescription')}</p>
      <div className='grid gap-3 sm:grid-cols-2'>
        {(['defaultContextWindow', 'maxContextWindow'] as const).map((name) => (
          <FormField
            key={name}
            control={control}
            name={`settings.contextSettings.${name}`}
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t(`modelContext.${name}`)}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={1}
                    max={2147483647}
                    step={1}
                    value={field.value ?? ''}
                    placeholder={t(name === 'defaultContextWindow' && maximum ? 'modelContext.useMaximum' : 'modelContext.upstream')}
                    onChange={(event) => field.onChange(event.target.value === '' ? null : Number(event.target.value))}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        ))}
      </div>
      <p className='text-muted-foreground text-xs'>{t('modelContext.defaultFallback')}</p>
      <p className='text-muted-foreground text-xs'>{t('modelContext.modelScope')}</p>
    </section>
  );
}
