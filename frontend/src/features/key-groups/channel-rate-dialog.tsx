import { useMemo, useState } from 'react';
import { useMutation, useQuery, useInfiniteQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { ChevronLeft, ChevronRight, RotateCcw } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useAuthStore } from '@/stores/authStore';
import { useSelectedProjectId } from '@/stores/projectStore';
import { extractNumberIDAsNumber, buildGUID } from '@/lib/utils';
import { useDebounce } from '@/hooks/use-debounce';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';

type ChannelUpstreamState = {
  channelId: string;
  manualMultiplier: number;
  enabledCredentialCount: number;
  settings: { enabled: boolean; costMode: string };
  keys: {
    keyRef: string;
    label: string;
    remark: string;
    disabled: boolean;
    applied: { value: number; source: string; observedAt: string | null };
  }[];
};
type RateChannel = { channelID: number; name: string; tags: string[]; profiles?: string[]; disabled?: boolean };
type ChannelScope = { activeProfile: string; profiles: string[]; channels: RateChannel[] };
type ChannelNode = { id: string; name: string; tags: string[] | null; status: string };
const pageSize = 25;

type ChannelList = {
  queryChannels: { edges: { node: ChannelNode }[]; pageInfo: { endCursor: string | null; hasNextPage: boolean } };
};
const namesQuery = `query RateChannels($input:QueryChannelInput!){queryChannels(input:$input){edges{node{id name tags status}} pageInfo{hasNextPage endCursor}}}`;
const stateQuery = `query RateChannelNumbers($ids:[ID!]!){channelUpstreamStates(channelIds:$ids){channelId manualMultiplier enabledCredentialCount settings{enabled costMode} keys{keyRef label remark disabled applied{value source observedAt}}}}`;

type Target = { keyID?: number; groupID?: number; name: string };
type Rule = { channelID: number; multiplier: string | number };
type RateChanges = { revision: string; rules: Rule[]; globalRate: string | null };
type Rates = RateChanges & { inherited: Rule[]; inheritedGlobalRate: string | null };
const rateFields = 'rules{channelID multiplier} inherited{channelID multiplier} globalRate inheritedGlobalRate revision';
const keyQuery = `query KeyRatesAndScope($keyID:Int!){
  keyChannelRates(keyID:$keyID){${rateFields}}
  keyRateChannelScope(keyID:$keyID){activeProfile profiles channels{channelID name tags profiles disabled}}
}`;
const groupQuery = `query GroupRates($groupID:Int!){keyChannelRates(groupID:$groupID){${rateFields}}}`;
const groupChannelsQuery = `query GroupRateChannels($groupID:Int!){
  groupRateChannels(groupID:$groupID){channelID name tags disabled}
}`;
const saveRates = `mutation SaveKeyChannelRates($keyID:Int,$groupID:Int,$revision:String!,$rules:[KeyChannelRateInput!]!,$globalRate:KeyGlobalRateInput!){saveKeyChannelRates(keyID:$keyID,groupID:$groupID,revision:$revision,rules:$rules,globalRate:$globalRate)}`;

function rateRange(state: ChannelUpstreamState) {
  const keys = state.keys.filter((k) => !k.disabled);
  const manual = !state.settings.enabled || state.settings.costMode === 'MANUAL';
  const rates = manual || !keys.length ? [Number(state.manualMultiplier)] : keys.map((k) => Number(k.applied.value));
  const low = Math.min(...rates),
    high = Math.max(...rates);
  return {
    keys,
    manual,
    mixed: rates.some((v) => v !== Number(state.manualMultiplier)),
    label: low === high ? low + '×' : low + '×–' + high + '×',
  };
}

function CurrentRate({ state }: { state?: ChannelUpstreamState }) {
  const { t } = useTranslation();
  if (!state) return <span>{t('kg.rate.unavailable')}</span>;
  const { keys, manual, mixed, label } = rateRange(state);
  const count = state.enabledCredentialCount;
  const estimates = keys.filter((k) => k.applied.source === 'ESTIMATED').length || (!manual && !keys.length ? count : 0);
  const sources = !count || manual ? ['MANUAL'] : keys.length ? [...new Set(keys.map((k) => k.applied.source))] : ['ESTIMATED'];
  return (
    <details className='text-xs'>
      <summary className='cursor-pointer'>
        {label} · {sources.map((s) => t('kg.rate.' + s)).join(' / ')}
        <span className='text-muted-foreground block'>
          {count ? t('kg.rate.credentials', { count }) : t('kg.rate.noKeys')}
          {estimates > 0 && ' · ' + t('kg.rate.estimateKeys', { count: estimates })}
          {mixed && ' · ' + t('kg.rate.itemRates')}
        </span>
      </summary>
      {keys.map((k) => (
        <div key={k.keyRef}>
          {k.remark || k.label} · {k.applied.value}× · {t('kg.rate.' + k.applied.source)}{' '}
          {k.applied.observedAt && new Date(k.applied.observedAt).toLocaleString()}
        </div>
      ))}
      <div>
        {t('kg.rate.fixed')} · {state.manualMultiplier}×
      </div>
    </details>
  );
}

function channelRow(node: ChannelNode): RateChannel {
  return { channelID: extractNumberIDAsNumber(node.id), name: node.name, tags: node.tags ?? [], disabled: node.status === 'disabled' };
}

function ChannelLabel({ row }: { row: RateChannel }) {
  const { t } = useTranslation();
  return (
    <div className='flex max-w-64 flex-col gap-0.5'>
      <div className='flex items-baseline gap-1.5'>
        <span className='truncate font-medium' title={row.name}>
          {row.name}
        </span>
        <span className='text-muted-foreground shrink-0 text-xs tabular-nums'>#{row.channelID}</span>
        {row.disabled && <Badge variant='secondary'>{t('kg.rate.disabledChannel')}</Badge>}
      </div>
      {row.tags.length > 0 && (
        <div className='flex items-center gap-1 overflow-hidden' title={row.tags.join(' · ')}>
          {row.tags.slice(0, 2).map((tag) => (
            <Badge key={tag} variant='outline' className='block max-w-24 truncate px-1 py-0 text-xs'>
              {tag}
            </Badge>
          ))}
          {row.tags.length > 2 && <span className='text-muted-foreground text-xs'>+{row.tags.length - 2}</span>}
        </div>
      )}
    </div>
  );
}

function GroupChannelPicker({ project, excluded, onAdd }: { project: string; excluded: Set<number>; onAdd: (row: RateChannel) => void }) {
  const { t } = useTranslation();
  const identity = useAuthStore((s) => s.auth.user?.id);
  const [search, setSearch] = useState<string | null>(null);
  const term = useDebounce(search?.trim() ?? '', 250);
  const list = useInfiniteQuery({
    queryKey: ['rateChannelOptions', identity, project, term],
    enabled: search !== null,
    initialPageParam: null as string | null,
    queryFn: async ({ pageParam, signal }) =>
      (
        await graphqlRequest<ChannelList>(
          namesQuery,
          {
            input: { first: 40, after: pageParam, where: { statusIn: ['enabled', 'disabled'], ...(term && { nameContainsFold: term }) } },
          },
          { 'X-Project-ID': project },
          { signal }
        )
      ).queryChannels,
    getNextPageParam: (page) => (page.pageInfo.hasNextPage ? page.pageInfo.endCursor : undefined),
  });
  return (
    <>
      <Button variant='outline' size='sm' onClick={() => setSearch(search === null ? '' : null)}>
        {t('kg.rate.add')}
      </Button>
      {search !== null && (
        <div className='space-y-1 rounded-md border p-2'>
          <Input
            aria-label={t('kg.rate.search')}
            placeholder={t('kg.rate.search')}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <div className='max-h-40 overflow-auto'>
            {list.data?.pages
              .flatMap((page) => page.edges)
              .map(({ node }) => channelRow(node))
              .filter((row) => !excluded.has(row.channelID))
              .map((row) => (
                <Button
                  key={row.channelID}
                  variant='ghost'
                  className='flex h-auto w-full justify-start py-1 text-left'
                  onClick={() => {
                    onAdd(row);
                    setSearch(null);
                  }}
                >
                  <ChannelLabel row={row} />
                </Button>
              ))}
          </div>
          {list.hasNextPage && (
            <Button variant='ghost' onClick={() => list.fetchNextPage()}>
              {t('kg.rate.more')}
            </Button>
          )}
          {list.error && <p role='alert'>{list.error.message}</p>}
        </div>
      )}
    </>
  );
}

function RateForm({
  initial,
  saving,
  saveError,
  onSave,
  scope,
  channels,
  channelsPending,
  project,
  onClose,
}: {
  initial: Rates;
  saving: boolean;
  saveError: Error | null;
  onSave: (changes: RateChanges) => void;
  scope?: ChannelScope;
  channels?: RateChannel[];
  channelsPending: boolean;
  project: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState(() => new Map(initial.rules.map((r) => [r.channelID, String(r.multiplier)])));
  const [globalRate, setGlobalRate] = useState(() => String(initial.globalRate ?? ''));
  const inherited = useMemo(() => new Map(initial.inherited.map((r) => [r.channelID, String(r.multiplier)])), [initial]);
  const [added, setAdded] = useState<RateChannel[]>([]);
  const [search, setSearch] = useState('');
  const [profileFilter, setProfileFilter] = useState('all');
  const [page, setPage] = useState(0);
  const identity = useAuthStore((s) => s.auth.user?.id);
  const headers = { 'X-Project-ID': project };
  const scopedIDs = useMemo(() => new Set(channels?.map((row) => row.channelID)), [channels]);
  const savedOutside = useMemo(() => initial.rules.map((r) => r.channelID).filter((id) => !scopedIDs.has(id)), [initial, scopedIDs]);
  const savedNames = useQuery({
    queryKey: ['rateSavedChannelNames', identity, project, savedOutside],
    enabled: savedOutside.length > 0 && (profileFilter === 'outside' || (!channels && !channelsPending)),
    queryFn: async ({ signal }) => {
      const rows = new Map<number, RateChannel>();
      for (let i = 0; i < savedOutside.length; i += 100) {
        const ids = savedOutside.slice(i, i + 100).map((id) => buildGUID('Channel', String(id)));
        const names = await graphqlRequest<ChannelList>(namesQuery, { input: { first: ids.length, where: { idIn: ids } } }, headers, {
          signal,
        });
        for (const edge of names.queryChannels.edges) {
          const row = channelRow(edge.node);
          rows.set(row.channelID, row);
        }
      }
      return rows;
    },
  });
  const profileLabel = (name: string) => name || t('kg.defaultProfile');
  const term = search.trim().toLowerCase();
  const savedRows = savedOutside.map((id) => savedNames.data?.get(id) ?? { channelID: id, name: '', tags: [], profiles: [] });
  const outsideRows = [...savedRows, ...added.filter((row) => !scopedIDs.has(row.channelID))];
  const showOutside = profileFilter === 'outside' && outsideRows.length > 0;
  const source = !channels || showOutside ? outsideRows : channels;
  const emptyGroupMessage = channels ? 'kg.rate.noMemberChannels' : 'kg.rate.noSavedRates';
  const rows = source.filter((row) => {
    if (scope && profileFilter.startsWith('profile-') && !row.profiles?.includes(scope.profiles[Number(profileFilter.slice(8))]))
      return false;
    if (!term) return true;
    return (
      row.name.toLowerCase().includes(term) ||
      row.tags.some((tag) => tag.toLowerCase().includes(term)) ||
      ('#' + row.channelID).includes(term)
    );
  });
  const lastPage = Math.max(0, Math.ceil(rows.length / pageSize) - 1);
  const currentPage = Math.min(page, lastPage);
  const visible = rows.slice(currentPage * pageSize, (currentPage + 1) * pageSize);
  const selection = visible.filter((row) => row.name).map((row) => buildGUID('Channel', String(row.channelID)));
  const upstream = useQuery({
    queryKey: ['rateChannelNumbers', identity, project, selection],
    enabled: selection.length > 0,
    refetchInterval: 30000,
    queryFn: async ({ signal }) => {
      const { channelUpstreamStates } = await graphqlRequest<{ channelUpstreamStates: ChannelUpstreamState[] }>(
        stateQuery,
        { ids: selection },
        headers,
        { signal }
      );
      return new Map(channelUpstreamStates.map((state) => [extractNumberIDAsNumber(state.channelId), state]));
    },
  });
  const change = (id: number, value?: string) =>
    setDraft((previous) => {
      const next = new Map(previous);
      if (value === undefined) next.delete(id);
      else next.set(id, value);
      return next;
    });
  return (
    <>
      <div className='space-y-1'>
        <div className='flex flex-wrap items-center gap-2'>
          <label className='flex items-center gap-2 text-sm font-medium'>
            {t(scope ? 'kg.rate.keyGlobal' : 'kg.rate.groupGlobal')}
            <Input
              className='w-24'
              type='number'
              min='0'
              step='any'
              placeholder={t('kg.rate.inherit')}
              value={globalRate}
              disabled={saving}
              onChange={(e) => setGlobalRate(e.target.value)}
            />
            ×
          </label>
          {globalRate !== '' && (
            <Button variant='ghost' size='sm' disabled={saving} onClick={() => setGlobalRate('')}>
              {t('kg.rate.restore')}
            </Button>
          )}
        </div>
        <p className='text-muted-foreground text-xs'>{t('kg.rate.globalHint')}</p>
      </div>
      <div className='flex flex-wrap items-center gap-2'>
        <Input
          className='min-w-40 flex-1'
          aria-label={t('kg.rate.filter')}
          placeholder={t('kg.rate.filter')}
          value={search}
          onChange={(e) => {
            setSearch(e.target.value);
            setPage(0);
          }}
        />
        {channels && (scope || outsideRows.length > 0) && (
          <select
            className='border-input bg-background h-9 max-w-full rounded-md border px-2 text-sm'
            aria-label={t(scope ? 'kg.rate.profiles' : 'kg.rate.channel')}
            value={profileFilter}
            onChange={(e) => {
              setProfileFilter(e.target.value);
              setPage(0);
            }}
          >
            <option value='all'>{t(scope ? 'kg.rate.allProfiles' : 'kg.rate.memberChannels')}</option>
            {scope?.profiles.map((name, i) => (
              <option key={name} value={`profile-${i}`}>
                {profileLabel(name)}
              </option>
            ))}
            {outsideRows.length > 0 && <option value='outside'>{t('kg.rate.outside', { count: outsideRows.length })}</option>}
          </select>
        )}
      </div>
      {!scope && (
        <fieldset disabled={saving} className='min-w-0'>
          <legend className='sr-only'>{t('kg.rate.channel')}</legend>
          <GroupChannelPicker
            project={project}
            excluded={new Set([...scopedIDs, ...outsideRows.map((row) => row.channelID)])}
            onAdd={(row) => {
              setAdded((previous) => [...previous, row]);
              change(row.channelID, '');
              setSearch('');
              setProfileFilter('outside');
              setPage(Math.floor(outsideRows.length / pageSize));
            }}
          />
        </fieldset>
      )}
      <div role='region' aria-label={t('kg.rate.channel')} tabIndex={0} className='min-h-0 overflow-auto'>
        {savedNames.error && <p role='alert'>{savedNames.error.message}</p>}
        {upstream.error && <p role='alert'>{upstream.error.message}</p>}
        <table className='w-full min-w-max text-left text-sm'>
          <thead className='bg-background sticky top-0 z-10'>
            <tr className='text-muted-foreground border-b'>
              <th scope='col' className='p-2'>
                {t('kg.rate.channel')}
              </th>
              {scope && (
                <th scope='col' className='px-2'>
                  {t('kg.rate.profiles')}
                </th>
              )}
              <th scope='col' className='px-2'>
                {t('kg.rate.current')}
              </th>
              {scope && (
                <th scope='col' className='px-2'>
                  {t('kg.rate.group')}
                </th>
              )}
              <th scope='col' className='px-2' colSpan={2}>
                {t(scope ? 'kg.rate.key' : 'kg.rate.group')}
              </th>
            </tr>
          </thead>
          <tbody>
            {visible.map((row) => {
              const id = row.channelID;
              const value = draft.get(id),
                parent = inherited.get(id) ?? initial.inheritedGlobalRate ?? undefined;
              const state = upstream.data?.get(id);
              const parentLabel = parent !== undefined ? parent + '×' : state ? rateRange(state).label : t('kg.rate.unavailable');
              const name = [row.name, '#' + id].filter(Boolean).join(' ');
              return (
                <tr key={id} className='border-b'>
                  <th scope='row' className='p-2 text-left font-normal'>
                    <ChannelLabel row={row} />
                  </th>
                  {scope && (
                    <td className='max-w-40 truncate px-2' title={row.profiles?.map(profileLabel).join('、')}>
                      {row.profiles?.length ? row.profiles.map(profileLabel).join('、') : t('kg.rate.unmatched')}
                    </td>
                  )}
                  <td>
                    <CurrentRate state={state} />
                  </td>
                  {scope && (
                    <td className='px-2'>{parent === undefined ? t('kg.rate.channelDefault') + ' ' + parentLabel : parentLabel}</td>
                  )}
                  <td>
                    {value === undefined ? (
                      <Button variant='ghost' size='sm' disabled={saving} onClick={() => change(id, globalRate || parent || '')}>
                        {globalRate ? t('kg.rate.useGlobal', { rate: globalRate }) : t('kg.rate.inherit') + ' ' + parentLabel}
                      </Button>
                    ) : (
                      <label className='flex items-center gap-1'>
                        <Input
                          className='w-24'
                          aria-label={name + ' ' + t('kg.rate.value')}
                          type='number'
                          disabled={saving}
                          min='0'
                          step='any'
                          value={value}
                          onChange={(e) => change(id, e.target.value)}
                        />
                        ×
                      </label>
                    )}
                  </td>
                  <td>
                    {value !== undefined && (
                      <Button
                        size='icon'
                        variant='ghost'
                        disabled={saving}
                        title={t('kg.rate.restore')}
                        aria-label={t('kg.rate.restore') + ' ' + name}
                        onClick={() => change(id)}
                      >
                        <RotateCcw className='size-4' />
                      </Button>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
        {rows.length === 0 && !channelsPending && (
          <p className='text-muted-foreground p-2 text-sm'>
            {t(!scope && !term && !showOutside ? emptyGroupMessage : 'kg.rate.noMatches')}
          </p>
        )}
        {saveError && <p role='alert'>{saveError.message}</p>}
      </div>
      {lastPage > 0 && (
        <div className='flex items-center justify-end gap-2 text-sm'>
          <Button
            variant='outline'
            size='icon'
            aria-label={t('pagination.previousPage')}
            disabled={currentPage === 0}
            onClick={() => setPage(currentPage - 1)}
          >
            <ChevronLeft />
          </Button>
          <span>
            {currentPage + 1} / {lastPage + 1}
          </span>
          <Button
            variant='outline'
            size='icon'
            aria-label={t('pagination.nextPage')}
            disabled={currentPage === lastPage}
            onClick={() => setPage(currentPage + 1)}
          >
            <ChevronRight />
          </Button>
        </div>
      )}
      <DialogFooter>
        <Button variant='outline' disabled={saving} onClick={onClose}>
          {t('common.buttons.cancel')}
        </Button>
        <Button
          disabled={
            saving ||
            [...(globalRate ? [globalRate] : []), ...draft.values()].some((v) => !v.trim() || !Number.isFinite(Number(v)) || Number(v) < 0)
          }
          onClick={() =>
            onSave({
              revision: initial.revision,
              globalRate: globalRate || null,
              rules: [...draft].map(([channelID, multiplier]) => ({ channelID, multiplier })),
            })
          }
        >
          {t(saving ? 'common.buttons.saving' : 'common.buttons.save')}
        </Button>
      </DialogFooter>
    </>
  );
}

export function ChannelRateDialog({ target, onClose }: { target: Target; onClose: () => void }) {
  const { t } = useTranslation();
  const project = useSelectedProjectId();
  const identity = useAuthStore((s) => s.auth.user?.id);
  const query = useQuery({
    queryKey: ['keyChannelRates', identity, project, target.groupID, target.keyID],
    gcTime: 0,
    staleTime: Infinity,
    enabled: !!project,
    queryFn: ({ signal }) =>
      graphqlRequest<{ keyChannelRates: Rates; keyRateChannelScope?: ChannelScope }>(
        target.keyID ? keyQuery : groupQuery,
        { keyID: target.keyID, groupID: target.groupID },
        { 'X-Project-ID': project! },
        { signal }
      ),
  });
  const groupChannels = useQuery({
    queryKey: ['groupRateChannels', identity, project, target.groupID],
    enabled: !!project && target.groupID !== undefined,
    gcTime: 0,
    staleTime: Infinity,
    retry: false,
    queryFn: async ({ signal }) => {
      const { groupRateChannels } = await graphqlRequest<{ groupRateChannels: RateChannel[] }>(
        groupChannelsQuery,
        { groupID: target.groupID },
        { 'X-Project-ID': project! },
        { signal }
      );
      return groupRateChannels;
    },
  });
  const channelsPending = target.groupID !== undefined && groupChannels.isPending;
  const save = useMutation({
    // 离线时直接反馈保存失败，保留草稿供用户重试。
    networkMode: 'always',
    mutationFn: ({ revision, rules, globalRate }: RateChanges) =>
      graphqlRequest(
        saveRates,
        { keyID: target.keyID, groupID: target.groupID, revision, rules, globalRate: { multiplier: globalRate } },
        { 'X-Project-ID': project! }
      ),
    onSuccess: onClose,
  });
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !save.isPending) onClose();
      }}
    >
      <DialogContent className='flex max-h-[85vh] flex-col sm:max-w-3xl' showCloseButton={!save.isPending}>
        <DialogHeader>
          <DialogTitle>{t(target.keyID ? 'kg.rate.keyTitle' : 'kg.rate.groupTitle')}</DialogTitle>
          <DialogDescription>
            {target.name} ·{' '}
            {query.data?.keyRateChannelScope
              ? t('kg.rate.activeProfile', { name: query.data.keyRateChannelScope.activeProfile || t('kg.defaultProfile') })
              : t(target.groupID ? 'kg.rate.groupHint' : 'kg.rate.hint')}
          </DialogDescription>
        </DialogHeader>
        {query.error && <p role='alert'>{query.error.message}</p>}
        {query.isLoading && <p>{t('kg.loading')}</p>}
        {query.data && channelsPending && (
          <p role='status' className='text-muted-foreground text-sm'>
            {t('kg.rate.channelsLoading')}
          </p>
        )}
        {query.data && groupChannels.isError && (
          <div role='alert' className='flex items-center justify-between gap-2 text-sm'>
            <span>{t('kg.rate.channelsFailed')}</span>
            <Button
              variant='outline'
              size='sm'
              disabled={save.isPending || groupChannels.isFetching}
              onClick={() => void groupChannels.refetch()}
            >
              {t('common.buttons.retry')}
            </Button>
          </div>
        )}
        {query.data && project && (
          <RateForm
            key={`${project}-${target.keyID ?? target.groupID}`}
            saving={save.isPending}
            saveError={save.error}
            onSave={save.mutate}
            scope={query.data.keyRateChannelScope}
            channels={query.data.keyRateChannelScope?.channels ?? groupChannels.data}
            channelsPending={channelsPending}
            initial={query.data.keyChannelRates}
            project={project}
            onClose={onClose}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}
