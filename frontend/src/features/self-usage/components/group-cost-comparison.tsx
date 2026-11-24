import { Fragment, useState } from 'react';
import { useQuery, useInfiniteQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useTranslation } from 'react-i18next';
import { currencyFractionDigits } from '@/utils/currency-display';
import { Button } from '@/components/ui/button';
import type { SelfUsageMeta } from '../api';
import type { DateRange } from '../utils/date-range';

type Row = {
  keyID: number;
  keyName: string;
  channelID: number | null;
  channelName: string | null;
  requests: number;
  channelCost: number | null;
  billedCost: number | null;
  missingChannel: number;
  missingBilled: number;
  estimated: number;
};
type Report = { total: Row; keys: Row[]; channels: Row[] };
const fields = 'keyID keyName channelID channelName requests channelCost billedCost missingChannel missingBilled estimated';
const query = `query GroupCostComparison($groupID:Int!,$start:String!,$end:String!){groupCostComparison(groupID:$groupID,start:$start,end:$end){total{${fields}} keys{${fields}} channels{${fields}}}}`;

export function GroupCostComparison({
  groupID,
  project,
  identity,
  range,
  meta,
}: {
  groupID: number;
  project: string;
  identity: string;
  range: DateRange;
  meta: SelfUsageMeta;
}) {
  const { t, i18n } = useTranslation();
  const [details, setDetails] = useState<number | null>(null);
  const [expanded, setExpanded] = useState<number | null>(null);
  const result = useQuery({
    queryKey: ['managedGroupCostComparison', identity, project, groupID, range],
    queryFn: ({ signal }) =>
      graphqlRequest<{ groupCostComparison: Report }>(query, { groupID, ...range }, { 'X-Project-ID': project }, { signal }),
  });
  if (result.error)
    return (
      <p role='alert'>
        {result.error.message} <Button onClick={() => result.refetch()}>{t('kg.reload')}</Button>
      </p>
    );
  if (!result.data) return <p role='status'>{t('kg.loading')}</p>;
  const report = result.data.groupCostComparison;
  const money = (value: number | null) =>
    value == null
      ? '—'
      : t('currencies.format', {
          val: value,
          currency: meta.currencyCode,
          locale: i18n.language,
          ...currencyFractionDigits(meta.currencyDisplayDecimals, 2, 8),
        });
  const amount = (row: Row, channel: boolean) => {
    const missing = channel ? row.missingChannel : row.missingBilled;
    return (
      <>
        {money(channel ? row.channelCost : row.billedCost)}
        {missing > 0 && <small className='text-muted-foreground block'>{t('kg.rate.missing', { count: missing })}</small>}
        {channel && row.estimated > 0 && (
          <small className='text-muted-foreground block'>{t('kg.rate.estimatedCount', { count: row.estimated })}</small>
        )}
      </>
    );
  };
  const cells = (r: Row) => (
    <>
      <td className='p-2'>{r.requests}</td>
      <td>{amount(r, true)}</td>
      <td>{amount(r, false)}</td>
    </>
  );
  return (
    <section className='space-y-4'>
      <div>
        <h2 className='font-semibold'>{t('kg.rate.compare')}</h2>
        <p className='text-muted-foreground text-sm'>{t('kg.rate.history')}</p>
      </div>
      <div className='grid gap-3 sm:grid-cols-2'>
        {[true, false].map((channel) => (
          <div key={String(channel)} className='rounded-lg border p-4'>
            <h3>{t(channel ? 'kg.rate.cost' : 'kg.rate.billed')}</h3>
            <div className='mt-2 text-2xl font-semibold tabular-nums'>{amount(report.total, channel)}</div>
          </div>
        ))}
      </div>
      <div className='overflow-auto rounded-lg border'>
        <table className='w-full text-left text-sm tabular-nums'>
          <thead>
            <tr className='border-b'>
              <th className='p-3'>{t('kg.rate.byKey')}</th>
              <th>{t('kg.rate.requests')}</th>
              <th>{t('kg.rate.cost')}</th>
              <th>{t('kg.rate.billed')}</th>
            </tr>
          </thead>
          <tbody>
            {report.keys.map((r) => (
              <Fragment key={r.keyID}>
                <tr className='border-b'>
                  <td className='p-2'>
                    <Button
                      size='sm'
                      variant='ghost'
                      aria-expanded={expanded === r.keyID}
                      onClick={() => {
                        setExpanded(expanded === r.keyID ? null : r.keyID);
                        setDetails(null);
                      }}
                    >
                      {expanded === r.keyID ? '▾' : '▸'} {r.keyName}
                    </Button>
                  </td>
                  {cells(r)}
                </tr>
                {expanded === r.keyID &&
                  report.channels
                    .filter((c) => c.keyID === r.keyID)
                    .map((c) => (
                      <Fragment key={c.channelID}>
                        <tr className='bg-muted/40'>
                          <td className='py-2 pl-10'>
                            <Button
                              size='sm'
                              variant='link'
                              aria-expanded={details === c.channelID}
                              onClick={() => setDetails(details === c.channelID ? null : c.channelID)}
                            >
                              {c.channelName || '#' + c.channelID}
                            </Button>
                          </td>
                          {cells(c)}
                        </tr>
                        {details === c.channelID && c.channelID !== null && (
                          <tr>
                            <td colSpan={4} className='px-8 py-2'>
                              <CostEntries
                                project={project}
                                identity={identity}
                                groupID={groupID}
                                keyID={r.keyID}
                                channelID={c.channelID}
                                range={range}
                                money={money}
                              />
                            </td>
                          </tr>
                        )}
                      </Fragment>
                    ))}
              </Fragment>
            ))}
          </tbody>
          <tfoot>
            <tr className='border-t font-semibold'>
              <td className='p-3'>{t('kg.rate.total')}</td>
              {cells(report.total)}
            </tr>
          </tfoot>
        </table>
      </div>
    </section>
  );
}

type Item = {
  code: string;
  variant: string;
  channelRate: number | null;
  channelSource: string;
  channelAmount: number | null;
  billedRate: number | null;
  billedSource: string;
  billedAmount: number | null;
};
type Entry = {
  items: Item[];
  usageID: number;
  requestID: number;
  channelCost: number | null;
  billedCost: number | null;
  multiplier: number | null;
  source: string;
  groupName: string;
  archived: boolean;
  mixed: boolean;
};
const entryQuery = `query GroupCostEntries($groupID:Int!,$keyID:Int!,$channelID:Int!,$start:String!,$end:String!,$before:Int){groupCostEntries(groupID:$groupID,keyID:$keyID,channelID:$channelID,start:$start,end:$end,before:$before){usageID requestID channelCost billedCost multiplier source groupName archived mixed items{code variant channelRate channelSource channelAmount billedRate billedSource billedAmount}}}`;
function CostEntries({
  project,
  identity,
  groupID,
  keyID,
  channelID,
  range,
  money,
}: {
  project: string;
  identity: string;
  groupID: number;
  keyID: number;
  channelID: number;
  range: DateRange;
  money: (v: number | null) => string;
}) {
  const { t } = useTranslation();
  const page = useInfiniteQuery({
    queryKey: ['groupCostEntries', identity, project, groupID, keyID, channelID, range],
    initialPageParam: null as number | null,
    queryFn: async ({ pageParam, signal }) =>
      (
        await graphqlRequest<{ groupCostEntries: Entry[] }>(
          entryQuery,
          { groupID, keyID, channelID, ...range, before: pageParam },
          { 'X-Project-ID': project },
          { signal }
        )
      ).groupCostEntries,
    getNextPageParam: (rows) => (rows.length === 20 ? rows.at(-1)?.usageID : undefined),
  });
  if (page.error) return <p role='alert'>{page.error.message}</p>;
  return (
    <div className='space-y-2'>
      <h3>{t('kg.rate.entries')}</h3>
      {page.isLoading && <p>{t('kg.loading')}</p>}
      <table className='w-full text-left'>
        <thead>
          <tr>
            <th>ID</th>
            <th>{t('kg.rate.value')}</th>
            <th>{t('kg.rate.cost')}</th>
            <th>{t('kg.rate.billed')}</th>
          </tr>
        </thead>
        <tbody>
          {page.data?.pages.flat().map((e) => (
            <Fragment key={e.usageID}>
              <tr className='border-t'>
                <td>
                  #{e.requestID}
                  {e.archived && <small className='text-muted-foreground block'>{t('kg.rate.archived')}</small>}
                </td>
                <td>
                  {e.mixed ? t('kg.rate.mixed') : e.multiplier == null ? '—' : e.multiplier + '×'}{' '}
                  {e.source && t('kg.rate.source.' + e.source)} {e.groupName}
                </td>
                <td>{money(e.channelCost)}</td>
                <td>{money(e.billedCost)}</td>
              </tr>
              {e.items.length > 0 && (
                <tr>
                  <td colSpan={4} className='pb-2'>
                    <details>
                      <summary className='text-muted-foreground cursor-pointer'>{t('kg.rate.itemRates')}</summary>
                      <table className='mt-2 w-full text-left text-xs'>
                        <thead>
                          <tr>
                            <th>{t('kg.rate.item')}</th>
                            <th>{t('kg.rate.cost')}</th>
                            <th>{t('kg.rate.billed')}</th>
                          </tr>
                        </thead>
                        <tbody>
                          {e.items.map((item, index) => (
                            <tr key={index} className='align-top'>
                              <td>
                                {item.code === 'flat_fee'
                                  ? t('price.mode_flat_fee')
                                  : item.code === 'image'
                                    ? t('price.image.output')
                                    : t('price.itemCodes.' + item.code, { defaultValue: item.code })}{' '}
                                {item.variant}
                              </td>
                              <td>
                                {item.channelRate == null ? '—' : item.channelRate + '×'}{' '}
                                {item.channelSource && t('kg.rate.' + item.channelSource)}
                                <div>{money(item.channelAmount)}</div>
                              </td>
                              <td>
                                {item.billedRate == null ? '—' : item.billedRate + '×'}{' '}
                                {item.billedSource && t('kg.rate.source.' + item.billedSource)}
                                <div>{money(item.billedAmount)}</div>
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </details>
                  </td>
                </tr>
              )}
            </Fragment>
          ))}
        </tbody>
      </table>
      {page.hasNextPage && (
        <Button size='sm' variant='outline' disabled={page.isFetchingNextPage} onClick={() => page.fetchNextPage()}>
          {t('kg.rate.more')}
        </Button>
      )}
    </div>
  );
}
