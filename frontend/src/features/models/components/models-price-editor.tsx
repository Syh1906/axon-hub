import { ServiceTierPriceEditor, ModeRulesEditor } from './service-tier-price-editor';
import { useId, useState, type ComponentProps } from 'react';
import { useFieldArray, useFormContext, useWatch } from 'react-hook-form';
import { IconPlus, IconTrash } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { FormControl, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { ModelPriceEditor } from '@/components/model-price-editor';
import { PriceScheduleEditor } from '@/components/price-schedule-editor';
import type { ModelPrice, ModelPriceItem } from '@/features/channels/data/schema';
import { useGeneralSettings } from '@/features/system/data/system';
import { isValidPriceMultiplier, multiplyPriceItems } from '../data/pricing';

export function ModelsPriceEditor({ portalContainer }: { portalContainer: HTMLDivElement | null }) {
  const { t } = useTranslation();
  return (
    <section className='space-y-4 rounded-md border p-4'>
      <h4 className='text-sm font-medium'>{t('models.price.title')}</h4>
      <p className='text-muted-foreground text-xs'>{t('models.price.description')}</p>
      <CompleteModelPriceEditor pricePath='modelCard.price' portalContainer={portalContainer} />
      <p className='text-muted-foreground text-xs'>{t('models.priceImport.autoFillHint')}</p>
    </section>
  );
}

export function CompleteModelPriceEditor({
  pricePath,
  portalContainer,
  multiplier,
}: {
  pricePath: string;
  portalContainer: HTMLDivElement | null;
  multiplier?: string;
}) {
  const { t } = useTranslation();
  const { data: settings } = useGeneralSettings();
  const { control, getValues, getFieldState, formState } = useFormContext();
  const price = useWatch({ control, name: pricePath }) as ModelPrice | null | undefined;
  const modelID = useWatch({ control, name: pricePath.startsWith('prices.') ? pricePath.replace(/\.price$/, '.modelId') : 'modelID' }) as string;
  const items = price?.items ?? [];
  const { fields, append, remove, replace } = useFieldArray({ control, name: `${pricePath}.volumeTiers` });
  const unified = fields.length > 0;
  const unifiedId = useId();
  const canUseUnified =
    items.length > 0 &&
    items.every(
      (item) =>
        item.pricing.mode === 'usage_per_unit' &&
        (item.promptWriteCacheVariants ?? []).every((variant) => variant.pricing.mode === 'usage_per_unit')
    );
  const itemControl = control as unknown as ComponentProps<typeof ModelPriceEditor>['control'];
  const errorMessages: string[] = [];
  const collectErrors = (error: unknown) => {
    if (!error || typeof error !== 'object') return;
    if ('message' in error && typeof error.message === 'string') errorMessages.push(error.message);
    Object.entries(error).forEach(([key, value]) => {
      if (key !== 'ref' && key !== 'message' && key !== 'type') collectErrors(value);
    });
  };
  collectErrors(getFieldState(pricePath, formState).error);
  return (
    <div className='space-y-3'>
      <div className='flex items-center gap-2'>
        <Switch
          id={unifiedId}
          checked={unified}
          disabled={!unified && !canUseUnified}
          onCheckedChange={(enabled) => {
            if (enabled) append({ above: NaN, items: structuredClone(getValues(`${pricePath}.items`) ?? []) });
            else replace([]);
          }}
        />
        <Label htmlFor={unifiedId}>{t('price.volume.title')}</Label>
      </div>
      <p className='text-muted-foreground text-xs'>{t(unified ? 'price.volume.description' : 'price.volume.enableHint')}</p>
      {!canUseUnified && !unified && <p className='text-muted-foreground text-xs'>{t('price.validation.perUnitOnly')}</p>}
      <div className='space-y-2 rounded-md border p-3'>
        <h4 className='text-sm font-medium'>
          {unified && Number.isFinite(price?.volumeTiers?.[0]?.above)
            ? t('price.volume.baseRange', { above: price?.volumeTiers?.[0]?.above })
            : t('price.volume.base')}
        </h4>
        <ModelPriceEditor
          control={itemControl}
          priceIndex={0}
          itemsPath={`${pricePath}.items`}
          currencyCode={settings?.currencyCode}
          multiplier={multiplier}
          perUnitOnly={unified}
          hideHeader
        />
      </div>
      {fields.map((field, index) => (
        <VolumeTierRow
          key={field.id}
          pricePath={pricePath}
          index={index}
          above={price?.volumeTiers?.[index]?.above ?? NaN}
          nextAbove={price?.volumeTiers?.[index + 1]?.above}
          baseItems={items}
          currencyCode={settings?.currencyCode}
          multiplier={multiplier}
          portalContainer={portalContainer}
          onRemove={() => remove(index)}
        />
      ))}
      {unified && (
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() => append({ above: NaN, items: structuredClone(getValues(`${pricePath}.items`) ?? []) })}
        >
          <IconPlus size={14} />
          {t('price.volume.addTier')}
        </Button>
      )}
      <ServiceTierPriceEditor pricePath={pricePath} portalContainer={portalContainer} modelID={modelID} channel={pricePath.startsWith('prices.')} />
      <PriceScheduleEditor
        control={control as unknown as ComponentProps<typeof PriceScheduleEditor>['control']}
        priceIndex={0}
        pricePath={pricePath}
        currencyCode={settings?.currencyCode}
        defaultTimezone={settings?.timezone}
        portalContainer={portalContainer}
        renderItems={(itemsPath) => (
          <><ModelPriceEditor
            control={itemControl}
            priceIndex={0}
            itemsPath={itemsPath}
            currencyCode={settings?.currencyCode}
            multiplier={multiplier}
          /><details className='rounded-md border p-3'><summary className='cursor-pointer text-sm'>{t('serviceTier.items.contextOverride')}</summary><ModeRulesEditor pricePath={pricePath} rulesPath={`${itemsPath.replace(/\.items$/, '')}.serviceTiers`} contextPath={itemsPath.replace(/\.items$/, '')} portalContainer={portalContainer} /></details></>
        )}
      />
      {price?.schedule != null && <p className='text-muted-foreground text-xs'>{t('price.volume.schedulePrecedence')}</p>}
      {errorMessages.length > 0 && (
        <p role='alert' className='text-destructive text-sm'>
          {[...new Set(errorMessages)].join(' · ')}
        </p>
      )}
    </div>
  );
}

function VolumeTierRow({
  pricePath,
  index,
  above,
  nextAbove,
  baseItems,
  currencyCode,
  multiplier,
  onRemove,
  portalContainer,
}: {
  pricePath: string;
  index: number;
  above: number;
  nextAbove?: number;
  baseItems: ModelPriceItem[];
  currencyCode?: string;
  multiplier?: string;
  onRemove: () => void;
  portalContainer: HTMLDivElement | null;
}) {
  const { t } = useTranslation();
  const { control, setValue, getValues } = useFormContext();
  const [tierMultiplier, setTierMultiplier] = useState('1');
  const tierMultiplierId = useId();
  const completeBase =
    baseItems.length > 0 &&
    baseItems.every((item) =>
      [item.pricing, ...(item.promptWriteCacheVariants ?? []).map((variant) => variant.pricing)].every(
        (pricing) =>
          pricing.usagePerUnit != null &&
          String(pricing.usagePerUnit).trim() !== '' &&
          Number.isFinite(Number(pricing.usagePerUnit)) &&
          Number(pricing.usagePerUnit) >= 0
      )
    );
  return (
    <div className='space-y-3 rounded-md border p-3'>
      <div className='flex items-center justify-between gap-2'>
        <h4 className='text-sm font-medium'>
          {Number.isFinite(above)
            ? t(Number.isFinite(nextAbove) ? 'price.volume.betweenRange' : 'price.volume.aboveRange', { above, upTo: nextAbove })
            : t('price.volume.enterThreshold')}
        </h4>
        <Button type='button' variant='ghost' size='icon-sm' aria-label={t('price.volume.removeTier')} onClick={onRemove}>
          <IconTrash size={14} />
        </Button>
      </div>
      <div className='flex flex-wrap items-end gap-3'>
        <FormField
          control={control}
          name={`${pricePath}.volumeTiers.${index}.above`}
          render={({ field }) => (
            <FormItem className='min-w-40 flex-1'>
              <FormLabel>{t('price.volume.above')}</FormLabel>
              <FormControl>
                <Input
                  {...field}
                  type='number'
                  min={0}
                  step={1}
                  placeholder={t('price.volume.enterThreshold')}
                  value={Number.isFinite(field.value) ? field.value : ''}
                  onChange={(event) => field.onChange(event.target.value === '' ? NaN : Number(event.target.value))}
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
        <div className='space-y-2'>
          <Label htmlFor={tierMultiplierId}>{t('price.volume.multiplier')}</Label>
          <Input
            id={tierMultiplierId}
            className='w-28'
            inputMode='decimal'
            value={tierMultiplier}
            onChange={(event) => setTierMultiplier(event.target.value)}
          />
        </div>
        <Button
          type='button'
          variant='outline'
          disabled={!completeBase || !isValidPriceMultiplier(tierMultiplier)}
          onClick={() =>
            setValue(`${pricePath}.volumeTiers.${index}.items`, multiplyPriceItems(getValues(`${pricePath}.items`) ?? [], tierMultiplier), {
              shouldDirty: true,
              shouldValidate: true,
            })
          }
        >
          {t('price.volume.fill')}
        </Button>
      </div>
      <FormField control={control} name={`${pricePath}.volumeTiers.${index}.longContext`} render={({ field }) => (
        <FormItem className='flex items-center gap-2'><FormControl><Switch checked={!!field.value} onCheckedChange={field.onChange} aria-label={t('serviceTier.context.mark')} /></FormControl><FormLabel>{t('serviceTier.context.mark')}</FormLabel></FormItem>
      )} />
      <p className='text-muted-foreground text-xs'>{t('price.volume.multiplierHint')}</p>
      <ModelPriceEditor
        control={control as unknown as ComponentProps<typeof ModelPriceEditor>['control']}
        priceIndex={0}
        itemsPath={`${pricePath}.volumeTiers.${index}.items`}
        currencyCode={currencyCode}
        multiplier={multiplier}
        perUnitOnly
        hideHeader
      />
      <details className='rounded-md border p-3'><summary className='cursor-pointer text-sm'>{t('serviceTier.items.contextOverride')}</summary><ModeRulesEditor pricePath={pricePath} rulesPath={`${pricePath}.volumeTiers.${index}.serviceTiers`} contextPath={`${pricePath}.volumeTiers.${index}`} portalContainer={portalContainer} /></details>
    </div>
  );
}
