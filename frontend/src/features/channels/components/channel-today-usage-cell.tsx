import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { formatNumber } from '@/utils/format-number';
import { HoverCard, HoverCardContent, HoverCardTrigger } from '@/components/ui/hover-card';
import { useChannelTodayUsageDetails } from '../data/channel-usage';
import type { Channel } from '../data/schema';

export function ChannelTodayUsageCell({ channel }: { channel: Channel }) {
  const { t, i18n } = useTranslation();
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
              ? t('channels.todayUsage.unpriced')
              : t('currencies.format', {
                  val: usage.totalCost,
                  currency: usage.currencyCode,
                  locale: i18n.language === 'zh' ? 'zh-CN' : 'en-US',
                  minimumFractionDigits: 6,
                })}
            {!allUnpriced && usage.unpricedCount > 0 ? '*' : ''}
          </span>
        </button>
      </HoverCardTrigger>
      <HoverCardContent className='w-[786px] max-w-[calc(100vw-24px)] p-3 text-left text-xs' sideOffset={6} collisionPadding={12}>
        <p className='mb-2 font-medium'>{t('channels.todayUsage.modelDetails')}</p>
        {open && <ChannelTodayUsageModels channelId={channel.id} />}
        <p>{t('channels.todayUsage.timezone', { timezone: usage.timezone })}</p>
        {usage.unpricedCount > 0 && <p>{t('channels.todayUsage.partial', { count: usage.unpricedCount })}</p>}
      </HoverCardContent>
    </HoverCard>
  );
}

// Mount only while open: scrolling the channel list never starts detail queries.
function ChannelTodayUsageModels({ channelId }: { channelId: string }) {
  const { t, i18n } = useTranslation();
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
      missing: total.missing + model.standardUnpricedCount,
    }),
    { actual: 0, requests: 0, unpriced: 0, standard: 0, standardKnown: false, missing: 0 }
  );
  const money = (value: number) => t('currencies.format', { val: value, currency: data.currencyCode, locale, minimumFractionDigits: 6 });
  return (
    <>
      <div className='bg-muted/50 mb-2 grid grid-cols-2 gap-3 rounded-md p-2'>
        <div>
          <p className='text-muted-foreground'>{t('channels.todayUsage.actualTotal')}</p>
          <p className='mt-1 font-medium tabular-nums'>
            {totals.unpriced === totals.requests && totals.actual === 0 ? t('channels.todayUsage.unpriced') : money(totals.actual)}
            {totals.unpriced > 0 && (totals.unpriced < totals.requests || totals.actual !== 0) ? '*' : ''}
          </p>
        </div>
        <div>
          <p className='text-muted-foreground'>{t('channels.todayUsage.standardTotal')}</p>
          <p className='mt-1 font-medium tabular-nums'>
            {totals.standardKnown ? money(totals.standard) : '—'}
            {totals.standardKnown && totals.missing > 0 ? '*' : ''}
          </p>
        </div>
      </div>
      <div className='mb-2 max-h-[min(320px,50vh)] overflow-auto'>
        <table className='w-[760px] border-collapse text-xs whitespace-nowrap'>
          <thead>
            <tr className='border-b border-current/20'>
              <th className='px-2 py-2 text-left'>{t('channels.todayUsage.model')}</th>
              <th className='px-2 py-2 text-right'>{t('consumption.columns.requestCount')}</th>
              <th className='px-2 py-2 text-right'>{t('consumption.columns.totalTokens')}</th>
              <th className='px-2 py-2 text-right'>{t('consumption.columns.totalCost')}</th>
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
                <td className='px-2 py-2 text-right tabular-nums'>{model.requestCount.toLocaleString(locale)}</td>
                <td className='px-2 py-2 text-right tabular-nums' title={model.totalTokens.toLocaleString(locale)}>
                  {formatNumber(model.totalTokens)}
                </td>
                <td
                  className='px-2 py-2 text-right tabular-nums'
                  title={model.unpricedCount ? t('channels.todayUsage.partial', { count: model.unpricedCount }) : undefined}
                >
                  {model.unpricedCount === model.requestCount && model.totalCost === 0
                    ? t('channels.todayUsage.unpriced')
                    : t('currencies.format', { val: model.totalCost, currency: data.currencyCode, locale, minimumFractionDigits: 6 })}
                  {model.unpricedCount > 0 && (model.unpricedCount < model.requestCount || model.totalCost !== 0) ? '*' : ''}
                </td>
                <td
                  className='px-2 py-2 text-right tabular-nums'
                  title={
                    model.standardUnpricedCount > 0
                      ? t('channels.todayUsage.standardMissing', { count: model.standardUnpricedCount })
                      : undefined
                  }
                >
                  {model.standardCost === null ? '—' : money(model.standardCost)}
                  {model.standardCost !== null && model.standardUnpricedCount > 0 ? '*' : ''}
                </td>
                <td className='px-2 py-2 text-right tabular-nums'>
                  {model.cacheHitRate === null ? '—' : `${(model.cacheHitRate * 100).toFixed(1)}%`}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className='text-muted-foreground mb-1'>{t('channels.todayUsage.standardHelp')}</p>
      {totals.missing > 0 && (
        <p className='text-muted-foreground mb-2'>{t('channels.todayUsage.standardMissing', { count: totals.missing })}</p>
      )}
    </>
  );
}
