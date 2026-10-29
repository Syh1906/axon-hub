import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import type { ModelPrice, ModelPriceItem, Pricing } from '@/features/channels/data/schema';
import { useGeneralSettings } from '@/features/system/data/system';

export function ModelsPriceDisplay({ price, compact = false }: { price?: ModelPrice | null; compact?: boolean }) {
  const { t } = useTranslation();
  if (!price) return <span className='text-muted-foreground text-xs'>{t('models.price.unset')}</span>;
  const tiers = price.volumeTiers ?? [];
  if (compact)
    return (
      <div className='min-w-52 space-y-1 text-xs'>
        <PriceItems items={price.items.filter((item) => item.itemCode === 'prompt_tokens' || item.itemCode === 'completion_tokens')} />
        {tiers.length > 0 && <Badge variant='outline'>{t('price.volume.tierCount', { count: tiers.length })}</Badge>}
        {price.schedule && <Badge variant='outline'>{t('price.schedule.title')}</Badge>}
      </div>
    );
  return (
    <div className='space-y-4 text-sm'>
      <div className='space-y-2'>
        <h5 className='font-medium'>{tiers.length ? t('price.volume.baseRange', { above: tiers[0].above }) : t('price.volume.base')}</h5>
        <PriceItems items={price.items} />
      </div>
      {tiers.map((tier, i) => (
        <div key={tier.above} className='space-y-2 border-t pt-3'>
          <h5 className='font-medium'>
            {t(i === tiers.length - 1 ? 'price.volume.aboveRange' : 'price.volume.betweenRange', {
              above: tier.above,
              upTo: tiers[i + 1]?.above,
            })}
          </h5>
          <PriceItems items={tier.items} />
        </div>
      ))}
      {tiers.length > 0 && <p className='text-muted-foreground text-xs'>{t('price.volume.description')}</p>}
      {price.schedule && (
        <div className='space-y-3 border-t pt-3'>
          <h5 className='font-medium'>
            {t('price.schedule.title')} · {price.schedule.timezone}
          </h5>
          <p className='text-muted-foreground text-xs'>{t('price.volume.schedulePrecedence')}</p>
          {price.schedule.overrides.map((override, i) => (
            <div key={i} className='space-y-2 rounded-md border p-2'>
              <div className='font-medium'>
                {override.name || t('price.schedule.overrides')} · {t('price.schedule.override.priority')}: {override.priority}
              </div>
              <div className='text-muted-foreground text-xs'>
                {override.when.dailyTime && (
                  <div>
                    {t('price.schedule.when.dailyTime')}: {override.when.dailyTime.start} – {override.when.dailyTime.end}
                  </div>
                )}
                {override.when.weekdays?.length ? (
                  <div>
                    {t('price.schedule.when.weekdays')}:{' '}
                    {override.when.weekdays.map((day) => t(`price.schedule.weekdays.${day}`)).join(', ')}
                  </div>
                ) : null}
                {override.when.dateRange && (
                  <div>
                    {t('price.schedule.when.dateRange')}: {override.when.dateRange.start} – {override.when.dateRange.end}
                  </div>
                )}
              </div>
              <PriceItems items={override.items} />
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function PriceItems({ items }: { items: ModelPriceItem[] }) {
  const { t } = useTranslation();
  return (
    <div className='space-y-1'>
      {items.map((item) => (
        <div key={item.itemCode} className='space-y-1'>
          <div className='flex justify-between gap-3'>
            <span className='text-muted-foreground'>{t(`price.itemCodes.${item.itemCode}`)}</span>
            <PriceValue pricing={item.pricing} />
          </div>
          {item.promptWriteCacheVariants?.map((variant) => (
            <div key={variant.variantCode} className='ml-3 flex justify-between gap-3 text-xs'>
              <span className='text-muted-foreground'>{t(`price.variantCodes.${variant.variantCode}`)}</span>
              <PriceValue pricing={variant.pricing} />
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}

function PriceValue({ pricing }: { pricing: Pricing }) {
  const { t, i18n } = useTranslation();
  const { data: settings } = useGeneralSettings();
  const amount = (value: string | number | null | undefined) =>
    value == null
      ? '-'
      : t('currencies.format', {
          val: Number(value),
          currency: settings?.currencyCode,
          locale: i18n.language === 'zh' ? 'zh-CN' : 'en-US',
          minimumFractionDigits: 6,
        });
  if (pricing.mode === 'flat_fee')
    return (
      <span className='text-right font-mono text-xs'>
        {amount(pricing.flatFee)} · {t('price.mode_flat_fee')}
      </span>
    );
  if (pricing.mode === 'image_fixed')
    return (
      <div className='text-right text-xs'>
        <span className='text-muted-foreground'>
          {t('price.mode_image_fixed')} · {t(pricing.image?.automatic ? 'price.image.autoShort' : 'price.image.manualShort')}
        </span>
        {pricing.image?.rules.slice(0, 3).map((rule, i) => (
          <div key={i}>
            {rule.name}: {amount(rule.fee)} / {t('price.image.imageUnit')}
          </div>
        ))}
        {(pricing.image?.rules.length ?? 0) > 3 && <div>{t('price.image.moreRules', { count: pricing.image!.rules.length - 3 })}</div>}
      </div>
    );
  if (pricing.mode === 'usage_per_unit') return <span className='text-right font-mono text-xs'>{amount(pricing.usagePerUnit)} / 1M</span>;
  return (
    <div className='text-right text-xs'>
      <span className='text-muted-foreground'>{t(`price.mode_${pricing.mode}`)}</span>
      {pricing.usageTiered?.tiers.map((tier, i) => (
        <div key={i} className='font-mono'>
          {tier.upTo == null ? '∞' : `≤ ${tier.upTo}`}: {amount(tier.pricePerUnit)} / 1M
        </div>
      ))}
    </div>
  );
}
