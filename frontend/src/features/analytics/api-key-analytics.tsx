import { useMemo, useState } from 'react';
import { calendarRangeToTimestamps, analyticsCalendarDate } from './utils/date-range';
import { useNavigate } from '@tanstack/react-router';
import { ArrowLeft } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Header } from '@/components/layout/header';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { useGeneralSettings } from '@/features/system/data/system';
import {
  useAnalyticsAPIKeyStats,
  useAnalyticsAPIKeyTemplates,
  useAnalyticsMetadata,
  type AnalyticsFilter,
} from './data/analytics';
import { APIKeyAnalyticsFilterBar } from './components/api-key-analytics-filter-bar';
import { APIKeyAnalyticsTable } from './components/api-key-analytics-table';

export default function APIKeyAnalyticsPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [startTime, setStartTime] = useState<string | null>(null);
  const [endTime, setEndTime] = useState<string | null>(null);
  const [templateIDs, setTemplateIDs] = useState<string[]>([]);
  const { data: generalSettings } = useGeneralSettings();
  const { data: metadata } = useAnalyticsMetadata();
  const {
    data: templates = [],
    isLoading: isTemplatesLoading,
    error: templatesError,
  } = useAnalyticsAPIKeyTemplates();
  const filter = useMemo<AnalyticsFilter>(
    () => ({
      ...calendarRangeToTimestamps(startTime, endTime, generalSettings?.timezone || 'UTC'),
      templateIDs: templateIDs.length > 0 ? templateIDs : undefined,
    }),
    [endTime, startTime, templateIDs, generalSettings?.timezone]
  );
  const {
    data: stats = [],
    isLoading: isStatsLoading,
    error: statsError,
  } = useAnalyticsAPIKeyStats(filter);

  const handleReset = () => {
    setStartTime(null);
    setEndTime(null);
    setTemplateIDs([]);
  };

  const error = templatesError || statsError;
  const currencyCode = generalSettings?.currencyCode || 'USD';

  return (
    <div className='flex-1 space-y-6 p-8 pt-6'>
      <Header>
        <h1 className='text-2xl font-bold tracking-tight'>
          {t('analytics.apiKeyAnalytics.title')}
        </h1>
      </Header>

      <Button onClick={() => navigate({ to: '/' })} variant='outline'>
        <ArrowLeft className='h-4 w-4' />
        {t('dashboard.channelSuccessRates.backToDashboard')}
      </Button>

      <p className='text-sm text-muted-foreground'>
        {t('analytics.apiKeyAnalytics.description')}
      </p>

      <APIKeyAnalyticsFilterBar
        earliestDate={analyticsCalendarDate(metadata?.earliestTime, generalSettings?.timezone || 'UTC')}
        timezone={generalSettings?.timezone || 'UTC'}
        startTime={startTime}
        endTime={endTime}
        templateIDs={templateIDs}
        templates={templates}
        isLoadingTemplates={isTemplatesLoading}
        onStartTimeChange={setStartTime}
        onEndTimeChange={setEndTime}
        onTemplateIDsChange={setTemplateIDs}
        onReset={handleReset}
      />

      {error ? (
        <div
          className={cn(
            'rounded-lg border border-destructive/40 bg-destructive/5',
            'p-4 text-sm text-destructive'
          )}
        >
          {t('common.loadError')} {error.message}
        </div>
      ) : (
        <APIKeyAnalyticsTable
          data={stats}
          isLoading={isStatsLoading}
          currencyCode={currencyCode}
        />
      )}
    </div>
  );
}
