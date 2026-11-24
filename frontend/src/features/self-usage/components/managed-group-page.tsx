import { useCallback, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useTranslation } from 'react-i18next';
import { useAuthStore } from '@/stores/authStore';
import { GroupCostComparison } from './group-cost-comparison';
import { usePermissions } from '@/hooks/usePermissions';
import { Button } from '@/components/ui/button';
import { BudgetTable } from '@/features/budgets/budget-table';
import { useBudget, hasBudgetLimits } from '@/features/budgets/data';
import { DatePicker, Summary, Trend, ModelTable } from '@/features/self-usage';
import { statsQuery, requestsQuery, type SelfUsageMeta, type SelfUsageStats, type RequestPage } from '@/features/self-usage/api';
import { PageError } from '@/features/self-usage/components/page-error';
import { presetRange, type DateRange } from '@/features/self-usage/utils/date-range';
import { RequestLog } from './request-log';
import { UsagePageShell } from './page-shell';

const managedStats = statsQuery
  .replace('SelfUsageStats(', 'ManagedGroupStats(')
  .replace('$group: Boolean!', '$groupRef: Int!')
  .replace('selfUsageStats(', 'managedGroupUsageStats(')
  .replace('group: $group', 'groupRef: $groupRef');
const managedRequests = requestsQuery
  .replace('SelfUsageRequests(', 'ManagedGroupRequests(')
  .replace('$group: Boolean!', '$groupRef: Int!')
  .replace('$status: SelfUsageRequestStatus', '$status: String')
  .replace('$page: Int, $pageSize: Int,', '$page: Int!, $pageSize: Int!,')
  .replace('selfUsageRequests(', 'managedGroupUsageRequests(')
  .replace('group: $group', 'groupRef: $groupRef');

function UsageContent({ groupRef, meta, project, identity }: { groupRef: number; meta: SelfUsageMeta; project: string; identity: string }) {
  const { t } = useTranslation();
  const [view,setView]=useState<'overview'|'comparison'>('overview');
  const { hasScope, hasSystemScope, isProjectOwner } = usePermissions(project);
  const canCompare=(isProjectOwner || hasSystemScope('write_channels')) && hasSystemScope('read_channels') && hasScope('read_requests') && groupRef>0;
  const comparing=canCompare && view==='comparison';
  const currencyDisplayDecimals = meta.currencyDisplayDecimals;
  const [range, setRange] = useState<DateRange>(() => presetRange('last7', meta.timezone));
  const [sessionID, setSessionID] = useState('');
  const [revision, setRevision] = useState(0);
  const refresh = useCallback(() => setRevision((v) => v + 1), []);
  const queryScope = ['managedGroupUsage', identity, project, groupRef];
  const stats = useQuery({
    queryKey: [...queryScope, 'stats', range, revision, sessionID],
    queryFn: async ({ signal }) =>
      (
        await graphqlRequest<{ managedGroupUsageStats: SelfUsageStats }>(
          managedStats,
          { groupRef, ...range, sessionID: sessionID || undefined },
          { 'X-Project-ID': project },
          { signal }
        )
      ).managedGroupUsageStats,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    staleTime: 0,
  });
  const budget = useBudget({ kind: 'group', ownerID: groupRef, name: meta.apiKeyName }, groupRef > 0, project);
  return (
    <div className='space-y-6'>
      <Button aria-pressed='true'>
        {t('apikeys.groups.scope')}: {meta.groupName || t('kg.ungrouped')}
      </Button>
      {canCompare && <div className='flex gap-2'>
        <Button variant={comparing?'outline':'default'} onClick={()=>setView('overview')}>{t('kg.rate.overview')}</Button>
        <Button variant={comparing?'default':'outline'} onClick={()=>setView('comparison')}>{t('kg.rate.compare')}</Button>
      </div>}
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <DatePicker meta={meta} onChange={setRange} />
        {!comparing && sessionID && <span className='text-muted-foreground break-all text-sm'>{t('requests.session.selected', { id: sessionID })}</span>}
        <span className='text-muted-foreground text-xs'>{t('requests.filters.timezone', { timezone: meta.timezone })}</span>
      </div>
      {comparing ? <GroupCostComparison groupID={groupRef} project={project} identity={identity} range={range} meta={meta}/> : <>
      {budget.error && <PageError error={budget.error} retry={() => void budget.refetch()} />}
      {budget.data && hasBudgetLimits(budget.data) && (
        <section className='rounded-lg border p-4'>
          <h2 className='mb-3 font-semibold'>{t('budgets.groupBudget')}</h2>
          <BudgetTable view={budget.data} currency={meta.currencyCode} onlyLimited currencyDisplayDecimals={currencyDisplayDecimals} />
          <p className='text-muted-foreground mt-2 text-xs'>{t('budgets.statsIndependent')}</p>
        </section>
      )}
      {stats.error && <PageError error={stats.error} retry={() => void stats.refetch()} />}
      {stats.isPending && <p role='status'>{t('selfUsage.loading')}</p>}
      {stats.data && (
        <>
          <Summary usage={stats.data.totals} meta={meta} models={stats.data.models} />
          <Trend daily={stats.data.daily} meta={meta} />
          <ModelTable models={stats.data.models} meta={meta} />
        </>
      )}
      <RequestLog
        key={`${range.start}:${range.end}`}
        sessionID={sessionID}
        onSessionFilter={setSessionID}
        keyValue=''
        range={range}
        meta={meta}
        models={stats.data?.models.map((m) => m.model) ?? []}
        onUnauthorized={refresh}
        refreshStats={stats.refetch}
        expectedScope={stats.data}
        scopeRevision={revision}
        onScopeMismatch={refresh}
        queryScope={queryScope}
        loadRequests={async (model, status, page, signal, after) =>
          (
            await graphqlRequest<{ managedGroupUsageRequests: RequestPage }>(
              managedRequests,
              { groupRef, ...range, model, status, page, pageSize: 20, after, sessionID: sessionID || undefined },
              { 'X-Project-ID': project },
              { signal }
            )
          ).managedGroupUsageRequests
        }
      />
      </>}
    </div>
  );
}

export function ManagedGroupPage({ groupRef, project }: { groupRef: number; project: string }) {
  const { t } = useTranslation();
  const identity = useAuthStore((state) => state.auth.user?.id);
  const meta = useQuery({
    queryKey: ['managedGroupUsage', identity, project, groupRef, 'meta'],
    enabled: !!project && !!identity,
    queryFn: async ({ signal }) =>
      (
        await graphqlRequest<{ managedGroupUsageMeta: SelfUsageMeta }>(
          'query GroupUsageMeta($groupRef: Int!) { managedGroupUsageMeta(groupRef: $groupRef) { apiKeyName groupName apiKeyType currencyCode currencyDisplayDecimals timezone maxRangeDays } }',
          { groupRef },
          { 'X-Project-ID': project! },
          { signal }
        )
      ).managedGroupUsageMeta,
  });
  return (
    <UsagePageShell meta={meta.data}>
      {meta.error && <PageError error={meta.error} retry={() => void meta.refetch()} />}
      {meta.isPending && <p role='status'>{t('selfUsage.loading')}</p>}
      {meta.data && project && identity && (
        <UsageContent
          key={`${identity}:${project}:${groupRef}`}
          groupRef={groupRef}
          meta={meta.data}
          project={project}
          identity={identity}
        />
      )}
    </UsagePageShell>
  );
}
