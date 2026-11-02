import { memo } from 'react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import type { Channel } from '../data/schema';

interface ChannelLimiterCellProps {
  channel: Channel;
}

export const ChannelLimiterCell = memo(({ channel }: ChannelLimiterCellProps) => {
  const { t, i18n } = useTranslation();
  const usage = channel.liveUsage;
  const limits = channel.settings?.rateLimit;
  const limiter = channel.liveLimiterStats;
  const format = (value: number) => value.toLocaleString(i18n.language);
  const formatLimit = (value: number | null | undefined) =>
    value != null && value > 0 ? format(value) : t('channels.columns.loadTooltip.unlimited');
  const hasConcurrencyLimit = (limits?.maxConcurrent ?? 0) > 0;

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <div
          tabIndex={0}
          className='grid w-full min-w-max cursor-help grid-cols-[max-content_var(--channel-load-value-width,4ch)_var(--channel-load-limit-width,6ch)] justify-center gap-x-2 gap-y-1 text-xs whitespace-nowrap'
        >
          {[
            { label: t('channels.columns.loadTooltip.concurrent'), value: usage?.inFlight, limit: limits?.maxConcurrent },
            { label: 'RPM', value: usage?.rpm, limit: limits?.rpm },
          ].map(({ label, value, limit }) => (
            <div key={label} className='contents'>
              <span className='text-muted-foreground text-left'>{label}</span>
              <span
                className={cn(
                  'text-right font-medium tabular-nums',
                  value != null &&
                    limit != null &&
                    limit > 0 &&
                    (value >= limit ? 'text-red-500' : value >= limit / 2 ? 'text-yellow-600 dark:text-yellow-400' : undefined)
                )}
              >
                {value == null ? '—' : format(value)}
              </span>
              <span className='text-muted-foreground text-left tabular-nums'>
                {limit != null && limit > 0 ? `/ ${format(limit)}` : null}
              </span>
            </div>
          ))}
        </div>
      </TooltipTrigger>
      <TooltipContent>
        <div className='max-w-72 space-y-2 text-xs'>
          <p>{t('channels.columns.loadTooltip.description')}</p>
          {!usage && <p>{t('channels.columns.loadTooltip.unavailable')}</p>}
          <div className='space-y-1'>
            <div>
              {t('channels.columns.loadTooltip.concurrentLimit')} · {formatLimit(limits?.maxConcurrent)}
            </div>
            <div>
              {t('channels.columns.loadTooltip.rpmLimit')} · {formatLimit(limits?.rpm)}
            </div>
            {limits?.tpm != null && limits.tpm > 0 && (
              <div>
                {t('channels.columns.loadTooltip.tpmLimit')} · {format(limits.tpm)}
              </div>
            )}
            {hasConcurrencyLimit && (
              <>
                <div>
                  {t('channels.columns.loadTooltip.waiting')} · {limiter ? format(limiter.waiting) : '—'} / {formatLimit(limits?.queueSize)}
                </div>
                <div>
                  {t('channels.columns.loadTooltip.queueTimeout')} ·{' '}
                  {limits?.queueTimeoutMs != null && limits.queueTimeoutMs > 0
                    ? t('channels.columns.loadTooltip.milliseconds', { value: format(limits.queueTimeoutMs) })
                    : t('channels.columns.loadTooltip.unlimited')}
                </div>
              </>
            )}
          </div>
        </div>
      </TooltipContent>
    </Tooltip>
  );
});

ChannelLimiterCell.displayName = 'ChannelLimiterCell';
