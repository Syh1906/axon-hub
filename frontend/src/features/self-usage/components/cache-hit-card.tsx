import { useTranslation } from 'react-i18next';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import type { Usage } from '../api';

export function CacheHitCard({ usage, models }: { usage: Usage; models: { model: string; usage: Usage }[] }) {
  const { t, i18n } = useTranslation();
  const number = new Intl.NumberFormat(i18n.language);
  const rate = (value: Usage) => (value.inputTokens > 0 ? `${((100 * value.cacheReadTokens) / value.inputTokens).toFixed(1)}%` : '—');
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Card tabIndex={0} className='focus-visible:ring-ring cursor-help focus-visible:ring-2'>
          <CardHeader className='pb-1'>
            <CardTitle className='text-muted-foreground text-sm font-medium'>{t('selfUsage.cacheHit.title')}</CardTitle>
          </CardHeader>
          <CardContent>
            <div className='text-2xl font-semibold tabular-nums'>{rate(usage)}</div>
            <p className='text-muted-foreground mt-2 text-xs'>{t('selfUsage.cacheHit.hint')}</p>
          </CardContent>
        </Card>
      </TooltipTrigger>
      <TooltipContent side='bottom' align='end' className='bg-popover text-popover-foreground max-w-[min(480px,90vw)] border p-4 shadow-md'>
        <p className='mb-3 font-semibold'>{t('selfUsage.cacheHit.models')}</p>
        <div className='max-h-64 space-y-2 overflow-auto'>
          {models.map(({ model, usage: item }) => (
            <div key={model} className='flex items-center justify-between gap-4'>
              <span className='max-w-48 truncate' title={model}>
                {model}
              </span>
              <span className='shrink-0 tabular-nums'>
                {rate(item)}{' '}
                <span className='text-muted-foreground'>
                  ({number.format(item.cacheReadTokens)} / {number.format(item.inputTokens)})
                </span>
              </span>
            </div>
          ))}
          {!models.length && <p>{t('selfUsage.cacheHit.empty')}</p>}
        </div>
        <p className='text-muted-foreground mt-3 border-t pt-3'>{t('selfUsage.cacheHit.formula')}</p>
      </TooltipContent>
    </Tooltip>
  );
}
