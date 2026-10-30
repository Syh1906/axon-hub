import { useTranslation } from 'react-i18next';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import type { Channel } from '../data/schema';

export function ChannelTodayUsageCell({ channel }: { channel: Channel }) {
  const { t, i18n } = useTranslation();
  const usage = channel.todayUsage;
  if (!usage)
    return <span className='text-muted-foreground text-xs'>{channel.todayUsageError ? t('channels.todayUsage.loadFailed') : '…'}</span>;
  const allUnpriced = usage.requestCount > 0 && usage.unpricedCount === usage.requestCount;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <div tabIndex={0} className='flex flex-col gap-1 text-xs whitespace-nowrap'>
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
        </div>
      </TooltipTrigger>
      <TooltipContent>
        <p>{t('channels.todayUsage.timezone', { timezone: usage.timezone })}</p>
        {usage.unpricedCount > 0 && <p>{t('channels.todayUsage.partial', { count: usage.unpricedCount })}</p>}
      </TooltipContent>
    </Tooltip>
  );
}
