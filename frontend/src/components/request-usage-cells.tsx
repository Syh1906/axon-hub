import { CostPricingDetails } from './cost-pricing-details';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { HoverCard, HoverCardContent, HoverCardTrigger } from '@/components/ui/hover-card';
import { getStatusColor } from '@/features/requests/components/help';
import { extractNumberID } from '@/lib/utils';
import { formatDuration } from '@/utils/format-duration';
import { buildCostBreakdown, type CostBreakdown } from './request-usage-cost-breakdown';

export type { CostBreakdown, CostBreakdownItem } from './request-usage-cost-breakdown';

export function RequestIdCell({ id, status, stream, onClick }: { id: number | string; status: string; stream: boolean; onClick?: () => void }) {
  const { t } = useTranslation();
  return (
    <div className='flex min-w-[120px] flex-col gap-1.5'>
      {onClick ? (
        <button type='button' onClick={onClick} className='text-primary w-fit cursor-pointer font-mono text-xs hover:underline'>
          #{typeof id === 'number' ? id : extractNumberID(id)}
        </button>
      ) : (
        <span className='text-primary w-fit font-mono text-xs'>#{typeof id === 'number' ? id : extractNumberID(id)}</span>
      )}
      <div className='flex flex-wrap items-center gap-1.5'>
        <Badge className={`${getStatusColor(status)} w-fit`}>{t(`requests.status.${status}`)}</Badge>
        <Badge
          className={
            stream
              ? 'border-green-200 bg-green-100 text-green-800 dark:border-green-800 dark:bg-green-900/20 dark:text-green-300'
              : 'border-gray-200 bg-gray-100 text-gray-800 dark:border-gray-800 dark:bg-gray-900/20 dark:text-gray-300'
          }
        >
          {stream ? t('requests.stream.streaming') : t('requests.stream.nonStreaming')}
        </Badge>
      </div>
    </div>
  );
}

export function TokensCell({ prompt, completion, reasoning, cached, writeCached }: {
  prompt: number;
  completion: number;
  reasoning: number;
  cached: number;
  writeCached: number;
}) {
  const { t, i18n } = useTranslation();
  const [open, setOpen] = useState(false);
  const format = (value: number) => value.toLocaleString(i18n.language);
  const newInput = prompt - cached;
  const details = [
    ['inputTotal', prompt],
    ['cacheHit', cached],
    ['newInput', newInput],
    ['cacheWrite', writeCached],
    ['output', completion],
    ['reasoning', reasoning],
  ] as const;
  return (
    <HoverCard open={open} onOpenChange={setOpen}>
      <HoverCardTrigger asChild>
        <button
          type='button'
          aria-label={t('requests.tokenDetails.title')}
          onClick={() => setOpen((value) => !value)}
          className='focus-visible:ring-ring flex cursor-help flex-col gap-0.5 rounded-sm text-left text-xs whitespace-nowrap outline-none focus-visible:ring-2'
        >
          <span className='text-sm font-medium'>{t('requests.columns.totalTokens')}{format(prompt + completion)}</span>
          <span className='text-muted-foreground'>
            {t('requests.columns.input')}: {format(prompt)} | {t('requests.columns.output')}: {format(completion)}
          </span>
          <span className='text-muted-foreground'>
            {t('requests.tokenDetails.newInput')}: {format(newInput)}
            {reasoning > 0 && <> | {t('requests.columns.reasoning')}: {format(reasoning)}</>}
          </span>
        </button>
      </HoverCardTrigger>
      <HoverCardContent align='start' className='w-72 max-w-[calc(100vw-2rem)] text-xs'>
        <div className='mb-3 font-semibold'>{t('requests.tokenDetails.title')}</div>
        <dl className='flex flex-col gap-1.5'>
          {details.map(([key, value]) => (
            <div key={key} className='flex justify-between gap-4'>
              <dt className='text-muted-foreground'>{t(`requests.tokenDetails.${key}`)}</dt>
              <dd className='tabular-nums'>{format(value)}</dd>
            </div>
          ))}
          <div className='mt-1 flex justify-between gap-4 border-t pt-2 font-medium'>
            <dt>{t('requests.tokenDetails.total')}</dt>
            <dd className='tabular-nums'>{format(prompt + completion)}</dd>
          </div>
        </dl>
        <p className='text-muted-foreground mt-3 leading-relaxed'>{t('requests.tokenDetails.explanation')}</p>
      </HoverCardContent>
    </HoverCard>
  );
}

export function ReadCacheCell({ cached, prompt }: { cached: number; prompt: number }) {
  const { t } = useTranslation();
  if (cached === 0) return <div className='text-muted-foreground text-xs'>-</div>;
  const hitRate = prompt > 0 ? (cached / prompt) * 100 : 0;
  const isLowHitRate = hitRate < 80 && prompt >= 40000;
  return (
    <div className='text-xs'>
      <div className='text-sm font-medium'>{cached.toLocaleString()}</div>
      <div className={isLowHitRate ? 'font-medium text-red-600 dark:text-red-400' : 'text-muted-foreground'}>
        {t('requests.columns.cacheHitRate', { rate: hitRate.toFixed(1) })}
      </div>
    </div>
  );
}

export function WriteCacheCell({ writeCached, prompt }: { writeCached: number; prompt: number }) {
  const { t } = useTranslation();
  if (writeCached === 0) return <div className='text-muted-foreground text-xs'>-</div>;
  return (
    <div className='text-xs'>
      <div className='text-sm font-medium'>{writeCached.toLocaleString()}</div>
      <div className='text-muted-foreground'>
        {t('requests.columns.writeCacheRate', { rate: prompt > 0 ? ((writeCached / prompt) * 100).toFixed(1) : '0.0' })}
      </div>
    </div>
  );
}

export function DurationCell({ status, stream, latencyMs, firstTokenLatencyMs }: {
  status: string; stream: boolean; latencyMs: number | null; firstTokenLatencyMs: number | null;
}) {
  const { t } = useTranslation();
  if (status !== 'completed' || latencyMs == null) return <span className='text-muted-foreground text-xs'>-</span>;
  if (!stream) return <span className='font-mono text-xs'>{t('requests.duration.total', { duration: formatDuration(latencyMs) })}</span>;
  return (
    <div className='min-w-[128px] font-mono text-xs'>
      {firstTokenLatencyMs != null && <div>{t('requests.duration.firstToken', { duration: formatDuration(firstTokenLatencyMs) })}</div>}
      <div className='text-muted-foreground'>{t('requests.duration.total', { duration: formatDuration(latencyMs) })}</div>
    </div>
  );
}

export function CostCell({
  total,
  currencyCode,
  breakdown,
  emptyLabel = '—',
}: {
  total: number | null;
  currencyCode: string;
  breakdown: CostBreakdown | null;
  emptyLabel?: string;
}) {
  const { t, i18n } = useTranslation();
  const [costOpen, setCostOpen] = useState(false);
  if (total == null && !breakdown?.items.some((item) => item.unpriced)) return <span className='font-mono text-xs'>{emptyLabel}</span>;
  const locale = i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US';
  const money = (val: number, minimumFractionDigits: number, maximumFractionDigits = minimumFractionDigits) =>
    t('currencies.format', { val, currency: currencyCode, locale, minimumFractionDigits, maximumFractionDigits });
  // Per-million unit prices below keep 2–6 decimals so sub-cent prices such as 0.0028 stay visible.
  const amount = total == null ? t('serviceTier.cost.unpriced') : money(total, 6);
  if (!breakdown) return <span className='font-mono text-xs font-medium'>{amount}</span>;
  const rows = buildCostBreakdown(total ?? 0, breakdown);
  const evidence = rows.evidence;
  const mode = evidence && evidence !== 'mixed' ? evidence.serviceTier.mode : null;
  const itemLabel = (code: string) => {
    switch (code) {
      case 'prompt_tokens':
        return t('requests.columns.input');
      case 'completion_tokens':
        return t('requests.columns.output');
      case 'prompt_cached_tokens':
        return t('requests.columns.readCache');
      case 'prompt_write_cached_tokens':
        return t('requests.columns.writeCache');
      default:
        return code;
    }
  };
  return (
    <Tooltip open={costOpen} onOpenChange={setCostOpen}>
      <TooltipTrigger asChild>
        <button type='button' onClick={(event) => { event.preventDefault(); setCostOpen((open) => !open); }} className='inline-flex cursor-help items-center gap-1 whitespace-nowrap font-mono text-xs font-medium'>
          <span className='underline decoration-dotted underline-offset-2'>{amount}</span>
          {(mode === 'fast' || mode === 'ultrafast') && (
            <Badge variant='outline' className='border-orange-200 bg-orange-50 px-1 text-[10px] leading-tight text-orange-700 dark:border-orange-800 dark:bg-orange-950/30 dark:text-orange-300'>
              {t(`serviceTier.mode.${mode}`)}
            </Badge>
          )}
          {evidence && evidence !== 'mixed' && evidence.context.longContext && (
            <Badge variant='outline' className='border-purple-200 bg-purple-50 px-1 text-[10px] leading-tight text-purple-700 dark:border-purple-800 dark:bg-purple-950/30 dark:text-purple-300'>
              1M
            </Badge>
          )}
          {evidence === 'mixed' && <Badge variant='outline' className='px-1 text-[10px] leading-tight'>{t('serviceTier.cost.mixed')}</Badge>}
        </button>
      </TooltipTrigger>
      <TooltipContent className='max-h-[70vh] max-w-[min(90vw,400px)] overflow-y-auto p-3' side='top'>
        <div className='min-w-[220px] space-y-2 text-xs'>
          <div className='font-semibold'>{t('requests.costBreakdown.title')}</div>
          <CostPricingDetails evidence={evidence} />
          {rows.items.map((item, index) => (
            <div key={`${item.itemCode}:${item.variant ?? ''}:${index}`}>
              <div className='flex justify-between gap-4'>
                <span className='flex items-center gap-1'>
                  {item.image ? `${t('price.image.output')} ${item.imageRule || item.image.tier}` : itemLabel(item.itemCode)}
                  {item.itemCode === 'prompt_write_cached_tokens' && item.variant && (
                    <Badge variant='secondary' className='h-4 px-1 text-[10px]'>
                      {item.variant === 'five_min' ? '5m' : item.variant === 'one_hour' ? '1h' : item.variant}
                    </Badge>
                  )}{' '}
                  · {item.quantity.toLocaleString(locale)}
                </span>
                <span className='font-mono'>{item.unpriced ? t('serviceTier.cost.unpriced') : money(item.pricingEvidence?.modeSubtotal ?? item.pricingEvidence?.baseSubtotal ?? item.subtotal, 6)}</span>
              </div>
              {evidence === 'mixed' && <CostPricingDetails evidence={item.pricingEvidence ?? null} />}
              {item.pricingEvidence?.itemMethod && <p className='text-background/75'>
                {item.pricingEvidence.itemMethod === 'unit_price' ? t('serviceTier.items.unit_price') : item.pricingEvidence.itemMethod === 'unpriced' ? t('serviceTier.items.unpriced') : `${item.pricingEvidence.baseUnitPrice == null ? '' : money(item.pricingEvidence.baseUnitPrice, 2, 6)} ×${item.pricingEvidence.modeMultiplier}`}
                {item.pricingEvidence.finalUnitPrice != null && ` → ${money(item.pricingEvidence.finalUnitPrice, 2, 6)} / 1M`}
              </p>}
              {item.pricingEvidence?.ruleSource && <p className='text-background/75'>{t(`serviceTier.items.rule_${item.pricingEvidence.ruleSource}`)}{item.pricingEvidence.reference?.source && ` · ${t(`serviceTier.prices.${item.pricingEvidence.reference.source}`)}`}{item.pricingEvidence.reference?.referenceModel && ` · ${item.pricingEvidence.reference.referenceModel}`}{item.pricingEvidence.reference?.version && ` · ${item.pricingEvidence.reference.version}`}</p>}
              {(evidence === 'mixed' || rows.costMultiplier === 'mixed') && item.pricingEvidence && <p>{t('requests.costBreakdown.multiplier')}: ×{item.pricingEvidence.costMultiplier} · {t('requests.costBreakdown.total')}: {item.unpriced ? t('serviceTier.cost.unpriced') : money(item.subtotal, 6)}</p>}
              {item.image && (
                <div>
                  {item.image.width > 0 && `${item.image.width} × ${item.image.height} · `}
                  {t(`price.image.source.${item.image.source}`)}
                  {item.image.tierSource &&
                    item.image.tierSource !== item.image.source &&
                    ` · ${t(`price.image.source.${item.image.tierSource}`)}`}
                </div>
              )}
              {item.unitPrice !== null && !item.pricingEvidence?.itemMethod && (
                <div className='text-background/75'>
                  @ {money(item.unitPrice, 2, 6)} / {item.image ? t('price.image.imageUnit') : '1M'}
                </div>
              )}
              {item.tiers.map((tier, tierIndex) => (
                <div key={tierIndex} className='text-background/75 ml-3 flex justify-between gap-4'>
                  <span>
                    {tier.upTo === null
                      ? t('requests.costBreakdown.above')
                      : t('requests.costBreakdown.upTo', { upTo: tier.upTo.toLocaleString(locale) })}{' '}
                    · {tier.units.toLocaleString(locale)}
                    {tier.unitPrice !== null && ` · @ ${money(tier.unitPrice, 2, 6)} / 1M`}
                  </span>
                  <span className='font-mono'>{money(tier.subtotal, 6)}</span>
                </div>
              ))}
            </div>
          ))}
          <div className='space-y-1 border-t pt-2'>
            {rows.itemized && <p>{t('serviceTier.items.itemized')}</p>}
            {!rows.itemized && rows.modeMultiplier !== undefined && <div className='flex justify-between gap-4'><span>{t('serviceTier.prices.multiplier')}</span><span>{rows.modeMultiplier == null ? t('serviceTier.prices.unconfigured') : rows.modeMultiplier === 'mixed' ? t('requests.costBreakdown.mixed') : `×${rows.modeMultiplier}`}</span></div>}
            <div className='flex justify-between gap-4'>
              <span>{t(rows.hasModeSubtotals ? 'serviceTier.items.modeTotal' : 'requests.costBreakdown.baseTotal')}</span>
              <span className='font-mono'>{total == null || rows.baseTotal === null ? '-' : money(rows.hasModeSubtotals ? rows.modeTotal ?? 0 : rows.baseTotal, 6)}</span>
            </div>
            <div className='flex justify-between gap-4'>
              <span>{t('requests.costBreakdown.multiplier')}</span>
              <span>{rows.costMultiplier === 'mixed' ? t('requests.costBreakdown.mixed') : `×${rows.costMultiplier}`}</span>
            </div>
          </div>
          {rows.hasModeSubtotals && <p className='text-background/75'>{t('serviceTier.items.frozen')}</p>}
          {(breakdown.unpricedRecords ?? 0) > 0 && (
            <div className='text-background/75'>{t('selfUsage.cost.unpriced', { count: breakdown.unpricedRecords })}</div>
          )}
          <div className='flex justify-between gap-4 border-t pt-2 font-semibold'>
            <span>{t('requests.costBreakdown.total')}</span>
            <span className='font-mono'>{amount}</span>
          </div>
        </div>
      </TooltipContent>
    </Tooltip>
  );
}
