import { useTranslation } from 'react-i18next';
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group';

export function AssociationPriceEditor({ value, onChange }: { value: boolean; onChange: (value: boolean) => void }) {
  const { t } = useTranslation();
  const label = t('models.dialogs.association.pricing.label');
  return (
    <fieldset className='space-y-2'>
      <legend className='text-xs font-medium'>{label}</legend>
      <RadioGroup
        aria-label={label}
        className='flex flex-wrap gap-x-5 gap-y-2'
        value={value ? 'actual' : 'requested'}
        onValueChange={(choice) => onChange(choice === 'actual')}
      >
        {(['requested', 'actual'] as const).map((choice) => (
          <label key={choice} className='flex cursor-pointer items-center gap-2 text-sm'>
            <RadioGroupItem value={choice} />
            {t(`models.dialogs.association.pricing.${choice}`)}
          </label>
        ))}
      </RadioGroup>
    </fieldset>
  );
}
