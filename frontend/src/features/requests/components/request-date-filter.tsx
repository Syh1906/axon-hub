import { useTranslation } from 'react-i18next';
import type { DateTimeRangeValue } from '@/utils/date-range';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { DateRangePicker } from '@/components/date-range-picker';
import type { ConsumptionPeriod } from '@/features/consumption/data/consumption';

export function RequestDateFilter({
  period,
  range,
  timezone,
  onPeriodChange,
  onRangeChange,
}: {
  period: ConsumptionPeriod;
  range?: DateTimeRangeValue;
  timezone: string;
  onPeriodChange: (period: ConsumptionPeriod) => void;
  onRangeChange: (range: DateTimeRangeValue | undefined) => void;
}) {
  const { t } = useTranslation();
  return (
    <div className='mb-4 flex flex-wrap items-center gap-2'>
      <Tabs value={period} onValueChange={(value) => onPeriodChange(value as ConsumptionPeriod)}>
        <TabsList className='consumption-periods h-auto flex-wrap gap-2 bg-transparent p-0' aria-label={t('consumption.timeRange')}>
          {(['TODAY', 'YESTERDAY', 'WEEK', 'MONTH', 'ALL'] as const).map((value) => (
            <TabsTrigger key={value} value={value} className='border-border h-10 rounded-xl border px-4'>
              {t(`consumption.periods.${value}`)}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>
      <DateRangePicker
        value={range}
        onChange={onRangeChange}
        placeholder={t('consumption.periods.CUSTOM')}
        className='max-w-full [&_button#date]:h-10 [&_button#date]:rounded-xl'
      />
      <span className='text-muted-foreground text-xs sm:ml-auto'>{t('requests.filters.timezone', { timezone })}</span>
    </div>
  );
}
