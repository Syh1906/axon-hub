import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
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

export function TokensCell({ prompt, completion, reasoning }: { prompt: number; completion: number; reasoning: number }) {
  const { t } = useTranslation();
  return (
    <div className='space-y-0.5 text-xs'>
      <div className='text-sm font-medium'>{t('requests.columns.totalTokens')}{(prompt + completion).toLocaleString()}</div>
      <div className='text-muted-foreground'>
        {t('requests.columns.input')}: {prompt.toLocaleString()} | {t('requests.columns.output')}: {completion.toLocaleString()}
      </div>
      {reasoning > 0 && <div className='text-muted-foreground'>{t('requests.columns.reasoning')}: {reasoning.toLocaleString()}</div>}
    </div>
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
  if (total == null && !breakdown?.items.some((item) => item.unpriced)) return <span className='font-mono text-xs'>{emptyLabel}</span>;
  const locale = i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US';
  const money = (val: number, minimumFractionDigits: number, maximumFractionDigits = minimumFractionDigits) =>
    t('currencies.format', { val, currency: currencyCode, locale, minimumFractionDigits, maximumFractionDigits });
  // Per-million unit prices below keep 2–6 decimals so sub-cent prices such as 0.0028 stay visible.
  const amount = total == null ? t('price.image.unpriced') : money(total, 6);
  if (!breakdown) return <span className='font-mono text-xs font-medium'>{amount}</span>;
  const rows = buildCostBreakdown(total ?? 0, breakdown);
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
    <Tooltip>
      <TooltipTrigger asChild>
        <button type='button' className='cursor-help font-mono text-xs font-medium underline decoration-dotted underline-offset-2'>
          {amount}
        </button>
      </TooltipTrigger>
      <TooltipContent className='max-h-[70vh] max-w-[min(90vw,400px)] overflow-y-auto p-3' side='top'>
        <div className='min-w-[220px] space-y-2 text-xs'>
          <div className='font-semibold'>{t('requests.costBreakdown.title')}</div>
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
                <span className='font-mono'>{item.unpriced ? t('price.image.unpriced') : money(item.subtotal, 6)}</span>
              </div>
              {item.image && (
                <div>
                  {item.image.width > 0 && `${item.image.width} × ${item.image.height} · `}
                  {t(`price.image.source.${item.image.source}`)}
                  {item.image.tierSource &&
                    item.image.tierSource !== item.image.source &&
                    ` · ${t(`price.image.source.${item.image.tierSource}`)}`}
                </div>
              )}
              {item.unitPrice !== null && (
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
            <div className='flex justify-between gap-4'>
              <span>{t('requests.costBreakdown.baseTotal')}</span>
              <span className='font-mono'>{total == null || rows.baseTotal === null ? '-' : money(rows.baseTotal, 6)}</span>
            </div>
            <div className='flex justify-between gap-4'>
              <span>{t('requests.costBreakdown.multiplier')}</span>
              <span>{rows.multiplier === 'mixed' ? t('requests.costBreakdown.mixed') : `×${rows.multiplier}`}</span>
            </div>
          </div>
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
