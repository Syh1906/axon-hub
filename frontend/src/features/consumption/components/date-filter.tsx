import { useState } from 'react';
import { format } from 'date-fns';
import { enUS, zhCN } from 'date-fns/locale';
import { CalendarDays } from 'lucide-react';
import type { DateRange } from 'react-day-picker';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Calendar } from '@/components/ui/calendar';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import type { ConsumptionDateRange, ConsumptionPeriod } from '../data/consumption';

const QUICK_PERIODS: ConsumptionPeriod[] = ['TODAY', 'YESTERDAY', 'WEEK', 'MONTH', 'ALL'];

export function ConsumptionDateFilter({
  value,
  onChange,
  timezone,
}: {
  value: ConsumptionDateRange;
  onChange: (value: ConsumptionDateRange) => void;
  timezone: string;
}) {
  const { t, i18n } = useTranslation();
  const [open, setOpen] = useState(false);
  const [start, setStart] = useState('');
  const [end, setEnd] = useState('');
  const valid = !!start && !!end && start <= end;
  const selected: DateRange = {
    from: start ? new Date(`${start}T12:00:00`) : undefined,
    to: end ? new Date(`${end}T12:00:00`) : undefined,
  };

  const changeOpen = (next: boolean) => {
    if (next) {
      const parts = new Intl.DateTimeFormat('en-CA', {
        timeZone: timezone,
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
      }).formatToParts(new Date());
      const part = (type: string) => parts.find((item) => item.type === type)?.value;
      const today = `${part('year')}-${part('month')}-${part('day')}`;
      setStart(value.startDate ?? today);
      setEnd(value.endDate ?? today);
    }
    setOpen(next);
  };

  return (
    <div className='flex flex-wrap items-center gap-2'>
      <Tabs value={value.period} onValueChange={(period) => onChange({ period: period as ConsumptionPeriod })}>
        <TabsList className='consumption-periods h-auto flex-wrap gap-2 bg-transparent p-0' aria-label={t('consumption.timeRange')}>
          {QUICK_PERIODS.map((period) => (
            <TabsTrigger key={period} value={period} className='border-border h-10 rounded-xl border px-4'>
              {t(`consumption.periods.${period}`)}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>
      <Popover open={open} onOpenChange={changeOpen}>
        <PopoverTrigger asChild>
          <Button variant={value.period === 'CUSTOM' ? 'default' : 'outline'} className='h-10 rounded-xl'>
            <CalendarDays data-icon='inline-start' />
            {t('consumption.periods.CUSTOM')}
          </Button>
        </PopoverTrigger>
        <PopoverContent align='start' className='w-auto max-w-[calc(100vw-2rem)] p-3' aria-label={t('consumption.periods.CUSTOM')}>
          <div className='flex flex-wrap gap-3'>
            <div className='flex min-w-0 flex-1 flex-col gap-1.5'>
              <Label htmlFor='consumption-start-date'>{t('consumption.startDate')}</Label>
              <Input
                id='consumption-start-date'
                type='date'
                value={start}
                max={end || undefined}
                onChange={(event) => setStart(event.target.value)}
              />
            </div>
            <div className='flex min-w-0 flex-1 flex-col gap-1.5'>
              <Label htmlFor='consumption-end-date'>{t('consumption.endDate')}</Label>
              <Input
                id='consumption-end-date'
                type='date'
                value={end}
                min={start || undefined}
                onChange={(event) => setEnd(event.target.value)}
              />
            </div>
          </div>
          <Calendar
            className='relative'
            mode='range'
            weekStartsOn={1}
            locale={i18n.language.startsWith('zh') ? zhCN : enUS}
            labels={{ labelPrevious: () => t('consumption.previousMonth'), labelNext: () => t('consumption.nextMonth') }}
            selected={selected}
            defaultMonth={selected.from}
            onSelect={(range) => {
              setStart(range?.from ? format(range.from, 'yyyy-MM-dd') : '');
              setEnd(range?.to ? format(range.to, 'yyyy-MM-dd') : '');
            }}
          />
          <div className='flex items-center justify-between gap-3 border-t pt-3'>
            <span className='text-muted-foreground text-xs'>{timezone}</span>
            <div className='flex gap-2'>
              <Button variant='ghost' size='sm' onClick={() => setOpen(false)}>
                {t('common.buttons.cancel')}
              </Button>
              <Button
                size='sm'
                disabled={!valid}
                onClick={() => {
                  onChange({ period: 'CUSTOM', startDate: start, endDate: end });
                  setOpen(false);
                }}
              >
                {t('consumption.apply')}
              </Button>
            </div>
          </div>
        </PopoverContent>
      </Popover>
    </div>
  );
}
