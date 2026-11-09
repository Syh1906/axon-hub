import { SystemTime } from '@/components/system-time';
import { memo } from 'react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { formatDuration } from '@/utils/format-duration';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import type { Channel } from '../data/schema';
import { useChannelControlData } from '../context/channel-controls-context';

interface ChannelHealthCellProps {
  channel: Channel;
}

export const ChannelHealthCell = memo(({ channel }: ChannelHealthCellProps) => {
  const { probePoints: points } = useChannelControlData(channel, ['probePoints']);
  const { t } = useTranslation();

  if (!points || points.length === 0) {
    return <span className='text-muted-foreground text-xs'>-</span>;
  }

  const maxBars = 15;
  const displayPoints = points.slice(-maxBars);

  return (
    <div className='flex items-center gap-0.5'>
      {displayPoints.map((point, index) => {
        const hasRequests = point.totalRequestCount > 0;
        const successRate = hasRequests
          ? point.successRequestCount / point.totalRequestCount
          : 0;

        const isHealthy = hasRequests && successRate >= 0.9;
        const isWarning = hasRequests && successRate >= 0.5 && successRate < 0.9;
        const isError = hasRequests && successRate < 0.5;
        const isIdle = !hasRequests;

        const probeTime = <SystemTime value={new Date(point.timestamp * 1000)} pattern="MM-dd HH:mm" />;

        return (
          <Tooltip key={`${point.timestamp}-${index}`}>
            <TooltipTrigger asChild>
              <div
                className={cn(
                  'h-8 w-1.5 cursor-help rounded-sm',
                  isHealthy && 'bg-green-500',
                  isWarning && 'bg-yellow-500',
                  isError && 'bg-red-500',
                  isIdle && 'bg-gray-200'
                )}
              />
            </TooltipTrigger>
            <TooltipContent>
              <div className='space-y-1 text-xs'>
                <div>{t('channels.columns.healthTooltip.probeTime')}: {probeTime}</div>
                <div>{t('channels.columns.healthTooltip.successRate')}: {point.successRequestCount}/{point.totalRequestCount}</div>
                <div>{t('channels.columns.healthTooltip.firstTokenLatency')}: {point.avgTimeToFirstTokenMs != null ? formatDuration(point.avgTimeToFirstTokenMs) : '-'}</div>
                <div>{t('channels.columns.healthTooltip.tokensPerSecond')}: {point.avgTokensPerSecond != null ? point.avgTokensPerSecond.toFixed(1) : '-'}</div>
              </div>
            </TooltipContent>
          </Tooltip>
        );
      })}
    </div>
  );
});

ChannelHealthCell.displayName = 'ChannelHealthCell';
