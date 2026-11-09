import { useChannelControlData } from '../context/channel-controls-context';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { currencyFractionDigits } from '@/utils/currency-display';
import { formatNumber } from '@/utils/format-number';
import { useCurrencyDisplayDecimals } from '@/hooks/use-currency-display';
import { HoverCard, HoverCardContent, HoverCardTrigger } from '@/components/ui/hover-card';
import { useChannelTodayUsageDetails } from '../data/channel-usage';
import type { Channel } from '../data/schema';

export function ChannelTodayUsageCell({ channel: source }: { channel: Channel }) {
  const channel = useChannelControlData(source, ['todayUsage', 'todayUsageError']);
  const { t, i18n } = useTranslation();
  const currencyDisplayDecimals = useCurrencyDisplayDecimals();
  const [open, setOpen] = useState(false);
  const usage = channel.todayUsage;
  if (!usage)
    return <span className='text-muted-foreground text-xs'>{channel.todayUsageError ? t('channels.todayUsage.loadFailed') : '…'}</span>;
  const allUnpriced = usage.requestCount > 0 && usage.unpricedCount === usage.requestCount && usage.totalCost === 0;
  return (
    <HoverCard open={open} onOpenChange={setOpen} openDelay={250} closeDelay={150}>
      <HoverCardTrigger asChild>
        <button
          type='button'
          onClick={() => setOpen(true)}
          aria-expanded={open}
          aria-label={t('channels.todayUsage.detailsFor', { name: channel.name })}
          className='flex flex-col gap-1 text-left text-xs whitespace-nowrap'
        >
          <span>{t('channels.todayUsage.requests', { count: usage.requestCount })}</span>
          <span className='text-muted-foreground'>
            {allUnpriced
              ? '—'
              : t('currencies.format', {
                  val: usage.totalCost,
                  currency: usage.currencyCode,
                  locale: i18n.language === 'zh' ? 'zh-CN' : 'en-US',
                  ...currencyFractionDigits(currencyDisplayDecimals, 6),
                })}
          </span>
        </button>
      </HoverCardTrigger>
      <HoverCardContent className='w-[1180px] max-w-[calc(100vw-24px)] p-3 text-left text-xs' sideOffset={6} collisionPadding={12}>
        <p className='mb-2 font-medium'>{t('channels.todayUsage.modelDetails')}</p>
        {open && <ChannelTodayUsageModels channelId={channel.id} />}
        <p>{t('channels.todayUsage.timezone', { timezone: usage.timezone })}</p>
      </HoverCardContent>
    </HoverCard>
  );
}

// Mount only while open: scrolling the channel list never starts detail queries.
function ChannelTodayUsageModels({ channelId }: { channelId: string }) {
  const { t, i18n } = useTranslation();
  const currencyDisplayDecimals = useCurrencyDisplayDecimals();
  const { data, isPending, error } = useChannelTodayUsageDetails(channelId);
  if (isPending)
    return (
      <p role='status' className='py-3'>
        {t('common.loading')}
      </p>
    );
  if (error)
    return (
      <p role='alert' className='py-3'>
        {t('channels.todayUsage.loadFailed')}
      </p>
    );
  if (!data.models.length) return <p className='py-3'>{t('common.noData')}</p>;
  const locale = i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US';
  const totals = data.models.reduce(
    (total, model) => ({
      actual: total.actual + model.totalCost,
      requests: total.requests + model.requestCount,
      unpriced: total.unpriced + model.unpricedCount,
      standard: total.standard + (model.standardCost ?? 0),
      standardKnown: total.standardKnown || model.standardCost !== null,
    }),
    { actual: 0, requests: 0, unpriced: 0, standard: 0, standardKnown: false }
  );
  const money = (value: number) =>
    t('currencies.format', { val: value, currency: data.currencyCode, locale, ...currencyFractionDigits(currencyDisplayDecimals, 6) });
  return (
    <>
      <div className='bg-muted/50 mb-2 grid grid-cols-2 gap-3 rounded-md p-2'>
        <div>
          <p className='text-muted-foreground'>{t('channels.todayUsage.actualTotal')}</p>
          <p className='mt-1 font-medium tabular-nums'>
            {totals.unpriced === totals.requests && totals.actual === 0 ? '—' : money(totals.actual)}
          </p>
        </div>
        <div>
          <p className='text-muted-foreground'>{t('channels.todayUsage.standardTotal')}</p>
          <p className='mt-1 font-medium tabular-nums'>{totals.standardKnown ? money(totals.standard) : '—'}</p>
        </div>
      </div>
      <div className='mb-2 max-h-[min(320px,50vh)] overflow-auto'>
        <table className='w-full min-w-[1154px] border-collapse text-xs whitespace-nowrap'>
          <thead>
            <tr className='border-b border-current/20'>
              <th className='px-2 py-2 text-left'>{t('channels.todayUsage.model')}</th>
              <th className='px-2 py-2 text-right' title={t('consumption.firstTokenHelp')}>
                {t('channels.todayUsage.avgFirstToken')}
                <span className='text-muted-foreground block font-normal'>ms</span>
              </th>
              <th className='px-2 py-2 text-right' title={t('consumption.speedHelp')}>
                {t('channels.todayUsage.avgOutput')}
                <span className='text-muted-foreground block font-normal'>tok/s</span>
              </th>
              <th className='px-2 py-2 text-right' title={t('consumption.channelSuccessRateHelp')}>
                {t('consumption.columns.successRate')}
              </th>
              <th className='px-2 py-2 text-right'>{t('consumption.columns.requestCount')}</th>
              <th className='px-2 py-2 text-right'>{t('consumption.columns.totalTokens')}</th>
              <th className='px-2 py-2 text-right'>{t('consumption.columns.totalCost')}</th>
              <th className='px-2 py-2 text-right'>{t('channels.todayUsage.perMillionCost')}</th>
              <th className='px-2 py-2 text-right'>{t('channels.todayUsage.standardCost')}</th>
              <th className='px-2 py-2 text-right' title={t('consumption.overallCacheHitRateHelp')}>
                {t('consumption.columns.cacheHitRate')}
              </th>
            </tr>
          </thead>
          <tbody>
            {data.models.map((model) => (
              <tr key={model.model} className='border-b border-current/10'>
                <td className='max-w-52 truncate px-2 py-2' title={model.model}>
                  {model.model}
                </td>
                <td className='px-2 py-2 text-right tabular-nums'>
                  {model.avgFirstTokenMs === null ? '—' : Math.round(model.avgFirstTokenMs).toLocaleString(locale)}
                </td>
                <td className='px-2 py-2 text-right tabular-nums'>
                  {model.avgTokensPerSecond === null ? '—' : model.avgTokensPerSecond.toFixed(1)}
                </td>
                <td className='px-2 py-2 text-right tabular-nums'>
                  {model.successRate === null ? '—' : `${(model.successRate * 100).toFixed(1)}%`}
                </td>
                <td className='px-2 py-2 text-right tabular-nums'>{model.requestCount.toLocaleString(locale)}</td>
                <td className='px-2 py-2 text-right tabular-nums' title={model.totalTokens.toLocaleString(locale)}>
                  {formatNumber(model.totalTokens)}
                </td>
                <td className='px-2 py-2 text-right tabular-nums'>
                  {model.unpricedCount === model.requestCount && model.totalCost === 0 ? '—' : money(model.totalCost)}
                </td>
                <td className='px-2 py-2 text-right tabular-nums'>
                  {model.avgCostPerMillionTokens === null ? '—' : money(model.avgCostPerMillionTokens)}
                </td>
                <td className='px-2 py-2 text-right tabular-nums'>{model.standardCost === null ? '—' : money(model.standardCost)}</td>
                <td className='px-2 py-2 text-right tabular-nums'>
                  {model.cacheHitRate === null ? '—' : `${(model.cacheHitRate * 100).toFixed(1)}%`}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className='text-muted-foreground mb-1'>{t('channels.todayUsage.perMillionHelp')}</p>
      <p className='text-muted-foreground mb-1'>{t('channels.todayUsage.standardHelp')}</p>
      <p className='text-muted-foreground mb-1'>{t('channels.todayUsage.performanceHelp')}</p>
    </>
  );
}
