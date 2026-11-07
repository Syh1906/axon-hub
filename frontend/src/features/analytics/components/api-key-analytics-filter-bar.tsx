import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { IconFilter, IconX } from '@tabler/icons-react';
import { Button } from '@/components/ui/button';
import { AnalyticsFacetedFilter } from './analytics-faceted-filter';
import { DateRangePicker, formatDate } from './analytics-calendar-range';
import { getAnalyticsQuickDateRange, type AnalyticsQuickRange } from '../utils/date-range';
import type { AnalyticsAPIKeyTemplate } from '../data/analytics';

export interface AnalyticsDetailFilterOption {
  label: string;
  value: string;
}

interface AnalyticsDetailFilterBarProps {
  earliestDate?: string | null;
  timezone: string;
  startTime: string | null;
  endTime: string | null;
  selectedValues: string[];
  filterTitle: string;
  options: AnalyticsDetailFilterOption[];
  isLoadingOptions: boolean;
  onStartTimeChange: (time: string | null) => void;
  onEndTimeChange: (time: string | null) => void;
  onSelectedValuesChange: (values: string[]) => void;
  onReset: () => void;
}

interface APIKeyAnalyticsFilterBarProps {
  earliestDate?: string | null;
  timezone: string;
  startTime: string | null;
  endTime: string | null;
  templateIDs: string[];
  templates: AnalyticsAPIKeyTemplate[];
  isLoadingTemplates: boolean;
  onStartTimeChange: (time: string | null) => void;
  onEndTimeChange: (time: string | null) => void;
  onTemplateIDsChange: (ids: string[]) => void;
  onReset: () => void;
}

export function AnalyticsDetailFilterBar({
  earliestDate,
  timezone,
  startTime,
  endTime,
  selectedValues,
  filterTitle,
  options,
  isLoadingOptions,
  onStartTimeChange,
  onEndTimeChange,
  onSelectedValuesChange,
  onReset,
}: AnalyticsDetailFilterBarProps) {
  const { t } = useTranslation();

  const applyQuickRange = useCallback(
    (range: AnalyticsQuickRange) => {
      const dates = getAnalyticsQuickDateRange(
        range,
        timezone,
        earliestDate
      );
      onStartTimeChange(dates.startTime);
      onEndTimeChange(dates.endTime);
    },
    [earliestDate, onEndTimeChange, onStartTimeChange, timezone]
  );

  const hasFilters = Boolean(startTime || endTime || selectedValues.length > 0);

  return (
    <div className='space-y-3 rounded-lg border bg-card p-4'>
      <div className='flex flex-wrap items-center gap-2'>
        <div className='flex items-center gap-1.5 text-sm font-medium'>
          <IconFilter className='h-4 w-4 text-muted-foreground' />
          {t('analytics.filter.dateRange')}
        </div>
        <DateRangePicker
          startDate={startTime}
          endDate={endTime}
          onStartChange={(date) => onStartTimeChange(date ? formatDate(date) : null)}
          onEndChange={(date) => onEndTimeChange(date ? formatDate(date) : null)}
        />
        <div className='flex flex-wrap items-center gap-1'>
          <Button
            variant='outline'
            size='sm'
            className='h-8 text-xs'
            onClick={() => applyQuickRange('today')}
          >
            {t('analytics.filter.today')}
          </Button>
          <Button
            variant='outline'
            size='sm'
            className='h-8 text-xs'
            onClick={() => applyQuickRange('yesterday')}
          >
            {t('analytics.filter.yesterday')}
          </Button>
          <Button
            variant='outline'
            size='sm'
            className='h-8 text-xs'
            onClick={() => applyQuickRange('thisWeek')}
          >
            {t('analytics.filter.thisWeek')}
          </Button>
          <Button
            variant='outline'
            size='sm'
            className='h-8 text-xs'
            onClick={() => applyQuickRange('thisMonth')}
          >
            {t('analytics.filter.thisMonth')}
          </Button>
          <Button
            variant='outline'
            size='sm'
            className='h-8 text-xs'
            onClick={() => applyQuickRange('all')}
          >
            {t('analytics.filter.all')}
          </Button>
        </div>
      </div>

      <div className='flex flex-wrap items-center gap-2'>
        <AnalyticsFacetedFilter
          title={filterTitle}
          options={options}
          selectedValues={selectedValues}
          onSelectedValuesChange={onSelectedValuesChange}
          isLoading={isLoadingOptions}
        />
        {hasFilters && (
          <Button
            variant='ghost'
            size='sm'
            className='h-8 text-xs text-muted-foreground'
            onClick={onReset}
          >
            <IconX className='mr-1 h-3 w-3' />
            {t('analytics.filter.reset')}
          </Button>
        )}
      </div>
    </div>
  );
}

export function APIKeyAnalyticsFilterBar({
  earliestDate,
  timezone,
  startTime,
  endTime,
  templateIDs,
  templates,
  isLoadingTemplates,
  onStartTimeChange,
  onEndTimeChange,
  onTemplateIDsChange,
  onReset,
}: APIKeyAnalyticsFilterBarProps) {
  const { t } = useTranslation();

  return (
    <AnalyticsDetailFilterBar
      earliestDate={earliestDate}
      timezone={timezone}
      startTime={startTime}
      endTime={endTime}
      selectedValues={templateIDs}
      filterTitle={t('analytics.apiKeyAnalytics.filter.template')}
      options={templates.map((template) => ({
        label: template.name,
        value: template.id,
      }))}
      isLoadingOptions={isLoadingTemplates}
      onStartTimeChange={onStartTimeChange}
      onEndTimeChange={onEndTimeChange}
      onSelectedValuesChange={onTemplateIDsChange}
      onReset={onReset}
    />
  );
}
