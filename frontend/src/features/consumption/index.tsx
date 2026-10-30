import { useState } from 'react';
import { BarChart3, Loader2, Search } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useSelectedProjectId } from '@/stores/projectStore';
import { extractNumberID } from '@/lib/utils';
import { useDebounce } from '@/hooks/use-debounce';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Header } from '@/components/layout/header';
import { Main } from '@/components/layout/main';
import { ConsumptionTable } from './components/consumption-table';
import { ConsumptionDateFilter } from './components/date-filter';
import {
  useConsumption,
  useConsumptionProjects,
  type ConsumptionDateRange,
  type ConsumptionDimension,
  type ConsumptionMetric,
} from './data/consumption';

export default function ConsumptionPage({ view = 'leaderboard' }: { view?: 'leaderboard' | 'usage' }) {
  const { t, i18n } = useTranslation();
  const isLeaderboard = view === 'leaderboard';
  const [dimension, setDimension] = useState<ConsumptionDimension>(isLeaderboard ? 'USER' : 'API_KEY_GROUP');
  const [metric, setMetric] = useState<ConsumptionMetric>('COST');
  const [range, setRange] = useState<ConsumptionDateRange>({ period: 'TODAY' });
  const [projectID, setProjectID] = useState('all');
  const [search, setSearch] = useState('');
  const debouncedSearch = useDebounce(search, 300);
  const selectedProjectID = useSelectedProjectId();
  const scope = isLeaderboard ? projectID : selectedProjectID;
  const [groupSelection, setGroupSelection] = useState<{ scope: string | null | undefined; id: number } | null>(null);
  const groupRef = groupSelection?.scope === scope ? groupSelection?.id : undefined;
  const isKeyDimension = dimension === 'API_KEY' || dimension === 'API_KEY_GROUP';
  const filter = {
    ...range,
    dimension,
    metric,
    groupRef: isKeyDimension ? groupRef : undefined,
    search: debouncedSearch || undefined,
    projectID: isLeaderboard && projectID !== 'all' ? projectID : undefined,
  };
  const { data, isLoading, isFetching, error, refetch } = useConsumption(filter, view, selectedProjectID);
  const projects = useConsumptionProjects(isLeaderboard);
  const groupOptions = useConsumption(
    { ...filter, dimension: 'API_KEY_GROUP', groupRef: undefined, search: undefined },
    view,
    selectedProjectID,
    isKeyDimension
  );
  const dimensions: ConsumptionDimension[] = isLeaderboard
    ? ['USER', 'API_KEY', 'API_KEY_GROUP', 'CHANNEL']
    : ['USER', 'API_KEY', 'API_KEY_GROUP'];
  const timezone = data?.timezone ?? 'UTC';
  const title = isLeaderboard ? t('consumption.title') : t('sidebar.items.usageStats');
  const formatDate = (value: string | Date) =>
    new Intl.DateTimeFormat(i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US', {
      timeZone: timezone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    }).format(new Date(value));
  const dateLabel = data?.rangeStart
    ? `${formatDate(data.rangeStart)} — ${formatDate(new Date(new Date(data.rangeEnd).getTime() - 1))}`
    : t('consumption.periods.ALL');

  return (
    <div className='flex flex-1 flex-col overflow-hidden'>
      <Header fixed>
        <div className='flex flex-col gap-1'>
          <h2 className='text-xl font-bold tracking-tight'>{title}</h2>
          <p className='text-muted-foreground text-sm'>{t(isLeaderboard ? 'consumption.description' : 'consumption.usageDescription')}</p>
        </div>
      </Header>
      <Main fixed className='flex min-h-0 flex-col gap-5'>
        <div className='flex shrink-0 flex-wrap items-center justify-between gap-3'>
          <div className='flex flex-wrap items-center gap-3'>
            <ConsumptionDateFilter value={range} onChange={setRange} timezone={timezone} />
            {data && (
              <span className='text-muted-foreground text-xs'>
                {dateLabel} · {timezone}
              </span>
            )}
          </div>
          {isLeaderboard && (
            <Select value={projectID} onValueChange={setProjectID} disabled={projects.isLoading}>
              <SelectTrigger className='w-48' aria-label={t('consumption.project')}>
                <SelectValue placeholder={t('consumption.allProjects')} />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem value='all'>{t('consumption.allProjects')}</SelectItem>
                  {projects.data?.map((project) => (
                    <SelectItem key={project.id} value={project.id}>
                      {project.name}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          )}
        </div>
        {projects.error && (
          <Alert variant='destructive'>
            <AlertDescription>
              {t('consumption.projectLoadError')}{' '}
              <Button variant='link' onClick={() => projects.refetch()}>
                {t('common.buttons.retry')}
              </Button>
            </AlertDescription>
          </Alert>
        )}
        <div className='bg-card flex min-h-0 flex-1 flex-col gap-4 rounded-2xl p-3 sm:p-4'>
          <Tabs value={dimension} onValueChange={(value) => setDimension(value as ConsumptionDimension)}>
            <TabsList className='consumption-dimensions h-11 w-full justify-start gap-7 rounded-none border-b bg-transparent p-0'>
              {dimensions.map((value) => (
                <TabsTrigger
                  key={value}
                  value={value}
                  className='h-full flex-none rounded-none border-0 border-b-2 border-transparent px-1'
                >
                  {t(`consumption.dimensions.${value}`)}
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
          <div className='flex shrink-0 flex-wrap items-center justify-between gap-3'>
            {isLeaderboard ? (
              <Tabs value={metric} onValueChange={(value) => setMetric(value as ConsumptionMetric)}>
                <TabsList aria-label={t('consumption.metric')}>
                  <TabsTrigger value='COST'>{t('consumption.costRank')}</TabsTrigger>
                  <TabsTrigger value='CACHE'>{t('consumption.cacheRank')}</TabsTrigger>
                </TabsList>
              </Tabs>
            ) : (
              <span className='text-muted-foreground text-sm'>{t('consumption.currentProject')}</span>
            )}
            {isKeyDimension && (
              <Select
                value={String(groupRef ?? 'all')}
                onValueChange={(value) => setGroupSelection(value === 'all' ? null : { scope, id: Number(value) })}
              >
                <SelectTrigger className='w-48' aria-label={t('apikeys.groups.title')}>
                  <SelectValue placeholder={t('consumption.allGroups')} />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value='all'>{t('consumption.allGroups')}</SelectItem>
                    {groupOptions.data?.entries.map((entry) => (
                      <SelectItem key={entry.id} value={String(extractNumberID(entry.id))}>
                        {entry.name}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            )}
            <div className='relative w-full sm:w-60'>
              <Search className='text-muted-foreground absolute top-2.5 left-3 size-4' aria-hidden />
              <Input
                type='search'
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                className='pl-9'
                placeholder={t('consumption.search')}
                aria-label={t('consumption.search')}
              />
            </div>
          </div>
          {error ? (
            <Alert variant='destructive'>
              <AlertTitle>{t('common.loadError')}</AlertTitle>
              <AlertDescription>
                {error.message}
                <Button variant='outline' size='sm' onClick={() => refetch()}>
                  {t('common.buttons.retry')}
                </Button>
              </AlertDescription>
            </Alert>
          ) : (
            <div className='border-border/70 relative min-h-0 flex-1 overflow-auto rounded-xl border' aria-busy={isFetching}>
              {isLoading ? (
                <div className='flex flex-col gap-4 p-5'>
                  <Skeleton className='h-9 w-full' />
                  {[0, 1, 2, 3].map((row) => (
                    <Skeleton key={row} className='h-14 w-full' />
                  ))}
                </div>
              ) : data?.entries.length ? (
                <ConsumptionTable
                  entries={data.entries}
                  dimension={dimension}
                  metric={metric}
                  currencyCode={data.currencyCode}
                  expandable
                />
              ) : (
                <div className='text-muted-foreground flex min-h-64 flex-col items-center justify-center gap-3 p-6'>
                  <BarChart3 className='size-8' aria-hidden />
                  <p>{t(search ? 'common.noResults' : 'consumption.empty')}</p>
                </div>
              )}
              {isFetching && !isLoading && (
                <div
                  className='bg-background absolute top-3 right-3 rounded-full p-2 shadow-sm'
                  role='status'
                  aria-label={t('common.loading')}
                >
                  <Loader2 className='size-4 animate-spin' />
                </div>
              )}
            </div>
          )}
          <div className='text-muted-foreground flex shrink-0 flex-wrap items-center justify-between gap-2 text-xs'>
            <span>{metric === 'CACHE' ? t('consumption.cacheNote') : t('consumption.costNote')}</span>
            {data && (
              <span>
                {t('consumption.updatedAt', {
                  time: new Intl.DateTimeFormat(i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US', {
                    timeZone: timezone,
                    hour: '2-digit',
                    minute: '2-digit',
                    second: '2-digit',
                  }).format(new Date(data.generatedAt)),
                })}
              </span>
            )}
          </div>
        </div>
      </Main>
    </div>
  );
}
