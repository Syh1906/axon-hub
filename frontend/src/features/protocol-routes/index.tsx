import { Fragment, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { useQueryClient } from '@tanstack/react-query';
import { ChevronDown, ChevronRight, RefreshCw } from 'lucide-react';
import { Main } from '@/components/layout/main';
import { PageHeader } from '@/components/layout/page-header';
import { QueryLoadAlert } from '@/components/query-load-alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { useDebounce } from '@/hooks/use-debounce';
import { CLIENT_PROTOCOLS, protocolLabel } from '@/features/channels/data/client-protocols';
import type { FilterCondition } from '@/features/models/data/schema';
import { routeEvidence, useProtocolRouteDetail, useProtocolRoutes } from './data';

function conditionLabel(condition: FilterCondition, t: TFunction): string {
  const prefix = 'models.dialogs.association.conditions';
  if (condition.type === 'group') {
    const logic = condition.logic?.trim().toLowerCase() === 'or' ? 'or' : 'and';
    return `${t(`${prefix}.${logic}`)} (${(condition.conditions ?? []).map((c) => conditionLabel(c, t)).join('; ')})`;
  }
  const field = condition.field ?? '';
  const [base, ...parts] = field.split('.');
  const name = t(`${prefix}.fields.${base}`, { defaultValue: base });
  const operator = t(`${prefix}.operators.${condition.operator}`, { defaultValue: condition.operator ?? '' });
  const value = field === 'request_format' ? protocolLabel(String(condition.value)) : String(condition.value ?? '');
  return `${name}${parts.length ? `.${parts.join('.')}` : ''} ${operator} ${value}`;
}

function RouteDetails({
  protocol,
  modelID,
  showExcluded,
  mode,
}: {
  protocol: string;
  modelID: string;
  showExcluded: boolean;
  mode: string;
}) {
  const { t, i18n } = useTranslation();
  const query = useProtocolRouteDetail(protocol, modelID);
  const routes = query.data?.routes.filter((r) => (showExcluded || r.state !== 'excluded') && (mode === 'all' || r.state === mode)) ?? [];
  const hasProtocolBranches = new Set(query.data?.routes.map((r) => JSON.stringify([r.channelID, r.actualModel]))).size < (query.data?.routes.length ?? 0);
  return (
    <div className='flex max-h-96 flex-col gap-3 overflow-auto px-3 py-4' data-testid='protocol-route-detail'>
      <QueryLoadAlert
        error={query.error}
        hasData={query.data !== undefined}
        retrying={query.isFetching}
        onRetry={() => {
          void query.refetch();
        }}
      />
      {query.isPending ? (
        <Skeleton className='h-32 w-full' />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              {['channel', 'actualModel', 'outbound', 'result', 'reason', 'evidence'].map((key) => (
                <TableHead key={key}>{t(`protocolRoutes.${key}`)}</TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {routes.map((r) => {
              const success = routeEvidence(r, query.data?.evidence ?? []);
              return (
                <TableRow key={JSON.stringify([r.channelID, r.actualModel, r.apiFormat])}>
                  <TableCell className='max-w-52 truncate' title={r.channelName}>
                    {r.channelName}
                  </TableCell>
                  <TableCell className='max-w-52 truncate font-mono text-xs' title={r.actualModel}>
                    {r.actualModel}
                  </TableCell>
                  <TableCell>{r.apiFormat ? protocolLabel(r.apiFormat) : '—'}</TableCell>
                  <TableCell>
                    <Badge variant={r.state === 'excluded' ? 'outline' : 'secondary'}>{t(`protocolRoutes.state.${r.state}`)}</Badge>
                  </TableCell>
                  <TableCell className='max-w-72 whitespace-normal'>
                    {r.condition
                      ? conditionLabel(r.condition, t)
                      : r.reason
                        ? t(`protocolRoutes.reason.${r.reason}`)
                        : r.state === 'converted'
                          ? `${protocolLabel(protocol)} → ${protocolLabel(r.apiFormat)}`
                          : t('protocolRoutes.enabled')}
                  </TableCell>
                  <TableCell className='whitespace-nowrap text-xs'>
                    {r.state === 'excluded'
                      ? '—'
                      : success
                        ? t('protocolRoutes.success', { time: new Date(success).toLocaleString(i18n.language) })
                        : t(query.data?.evidenceReadable ? 'protocolRoutes.unverified' : 'protocolRoutes.evidenceRestricted')}
                  </TableCell>
                </TableRow>
              );
            })}
            {routes.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className='text-muted-foreground py-8 text-center'>
                  {t('protocolRoutes.noRoutes')}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      )}
      {hasProtocolBranches && <p className='text-muted-foreground text-xs'>{t('protocolRoutes.branchHint')}</p>}
      <p className='text-muted-foreground text-xs'>{t('protocolRoutes.evidenceHint')}</p>
    </div>
  );
}

export default function ProtocolRoutesPage() {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [protocol, setProtocol] = useState<string>('openai/responses');
  const [responsesTransport, setResponsesTransport] = useState('http');
  const requestProtocol = protocol === 'openai/responses' && responsesTransport === 'websocket' ? 'openai/responses-ws' : protocol;
  const [search, setSearch] = useState('');
  const debouncedSearch = useDebounce(search, 300);
  const [page, setPage] = useState(0);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [showExcluded, setShowExcluded] = useState(true);
  const [mode, setMode] = useState('all');
  const query = useProtocolRoutes(requestProtocol, debouncedSearch, page);
  const total = query.data?.totalCount ?? 0;
  const pages = Math.max(1, Math.ceil(total / 20));
  const refresh = () => {
    void client.invalidateQueries({ queryKey: ['protocol-routes'] });
    void client.invalidateQueries({ queryKey: ['protocol-route-detail'] });
  };
  return (
    <Main fixed className='flex flex-col gap-3'>
      <PageHeader
        className='!p-0'
        title={t('protocolRoutes.title')}
        description={t('protocolRoutes.description')}
        actions={
          <>
            <Button variant='outline' onClick={refresh} disabled={query.isFetching}>
              <RefreshCw />
              {t('protocolRoutes.refresh')}
            </Button>
            <Badge variant='outline'>{t('protocolRoutes.global')}</Badge>
          </>
        }
      />
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <Tabs
          className='min-w-0 max-w-full'
          value={protocol}
          onValueChange={(value) => {
            setProtocol(value);
            setPage(0);
            setExpanded(null);
          }}
        >
          <TabsList className='max-w-full overflow-x-auto'>
            {CLIENT_PROTOCOLS.map((p) => (
              <TabsTrigger key={p.value} value={p.value}>
                {p.label}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
        <div className='flex flex-wrap items-center gap-3'>
          <p className='text-muted-foreground text-sm'>
            {t('protocolRoutes.endpoint')}{' '}
            <code>
              {protocol === 'openai/responses'
                ? responsesTransport === 'websocket'
                  ? 'WebSocket /v1/responses'
                  : 'POST /v1/responses'
                : CLIENT_PROTOCOLS.find((p) => p.value === protocol)?.path}
            </code>
          </p>
          {protocol === 'openai/responses' && (
            <Select
              value={responsesTransport}
              onValueChange={(v) => {
                setResponsesTransport(v);
                setExpanded(null);
              }}
            >
              <SelectTrigger className='w-40' aria-label={t('protocolRoutes.transport')}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value='http'>HTTP / SSE</SelectItem>
                <SelectItem value='websocket'>WebSocket</SelectItem>
              </SelectContent>
            </Select>
          )}
        </div>
      </div>
      <div className='flex flex-wrap items-center gap-3'>
        <Input
          className='max-w-sm'
          aria-label={t('protocolRoutes.search')}
          placeholder={t('protocolRoutes.search')}
          value={search}
          onChange={(e) => {
            setSearch(e.target.value);
            setPage(0);
            setExpanded(null);
          }}
        />
        <Label>{t('protocolRoutes.detailFilter')}</Label>
        <Select value={mode} onValueChange={setMode}>
          <SelectTrigger className='w-36' aria-label={t('protocolRoutes.detailFilter')}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {['all', 'native', 'converted', 'conditional'].map((v) => (
              <SelectItem key={v} value={v}>
                {t(v === 'all' ? 'protocolRoutes.all' : `protocolRoutes.state.${v}`)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Label className='flex items-center gap-2'>
          <Checkbox checked={showExcluded} onCheckedChange={(v) => setShowExcluded(v === true)} />
          {t('protocolRoutes.showExcluded')}
        </Label>
      </div>
      <QueryLoadAlert
        error={query.error}
        hasData={query.data !== undefined}
        retrying={query.isFetching}
        onRetry={() => {
          void query.refetch();
        }}
      />
      <div className='min-h-0 flex-1 overflow-auto rounded-lg border' data-testid='protocol-routes-table'>
        <Table>
          <TableHeader className='bg-muted/80'>
            <TableRow>
              <TableHead className='w-10' />
              <TableHead>{t('protocolRoutes.model')}</TableHead>
              {['native', 'converted', 'conditional', 'excluded'].map((key) => (
                <TableHead key={key} className='text-center'>
                  {t(`protocolRoutes.count.${key}`)}
                </TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {query.isPending ? (
              <TableRow>
                <TableCell colSpan={6}>
                  <Skeleton className='h-52 w-full' />
                </TableCell>
              </TableRow>
            ) : (
              query.data?.items.map((row) => (
                <Fragment key={row.modelID}>
                  <TableRow className={expanded === row.modelID ? 'bg-primary/5' : undefined}>
                    <TableCell>
                      <Button
                        variant='ghost'
                        size='icon'
                        aria-label={t(expanded === row.modelID ? 'protocolRoutes.collapse' : 'protocolRoutes.expand', {
                          model: row.modelID,
                        })}
                        aria-expanded={expanded === row.modelID}
                        onClick={() => setExpanded(expanded === row.modelID ? null : row.modelID)}
                      >
                        {expanded === row.modelID ? <ChevronDown /> : <ChevronRight />}
                      </Button>
                    </TableCell>
                    <TableCell className='max-w-96 truncate font-medium' title={row.name}>
                      {row.modelID}
                    </TableCell>
                    <TableCell className='text-center'>{row.native}</TableCell>
                    <TableCell className='text-center'>{row.converted}</TableCell>
                    <TableCell className='text-center'>{row.conditional}</TableCell>
                    <TableCell className='text-center'>{row.excluded}</TableCell>
                  </TableRow>
                  {expanded === row.modelID && (
                    <TableRow>
                      <TableCell colSpan={6} className='bg-muted/20 p-0'>
                        <RouteDetails protocol={requestProtocol} modelID={row.modelID} mode={mode} showExcluded={showExcluded} />
                      </TableCell>
                    </TableRow>
                  )}
                </Fragment>
              ))
            )}
            {!query.isPending && !query.error && total === 0 && (
              <TableRow>
                <TableCell colSpan={6} className='text-muted-foreground py-16 text-center'>
                  {t('protocolRoutes.empty')}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
      <div className='flex flex-wrap items-center justify-between gap-2 text-sm'>
        <span className='text-muted-foreground'>{t('protocolRoutes.pagination', { count: total })}</span>
        <div className='flex items-center gap-2'>
          <Button
            variant='outline'
            disabled={page === 0 || query.isFetching}
            onClick={() => {
              setPage(page - 1);
              setExpanded(null);
            }}
          >
            {t('protocolRoutes.previous')}
          </Button>
          <span>
            {page + 1} / {pages}
          </span>
          <Button
            variant='outline'
            disabled={page + 1 >= pages || query.isFetching}
            onClick={() => {
              setPage(page + 1);
              setExpanded(null);
            }}
          >
            {t('protocolRoutes.next')}
          </Button>
        </div>
      </div>
      <p className='text-muted-foreground text-xs'>{t('protocolRoutes.scopeHint')}</p>
    </Main>
  );
}
