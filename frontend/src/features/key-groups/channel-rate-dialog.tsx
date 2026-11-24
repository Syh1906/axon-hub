import { useMemo, useState } from 'react';
import { useMutation, useQuery, useInfiniteQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { RotateCcw } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useAuthStore } from '@/stores/authStore';
import { useSelectedProjectId } from '@/stores/projectStore';
import { extractNumberIDAsNumber, buildGUID } from '@/lib/utils';
import { useDebounce } from '@/hooks/use-debounce';
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
type ChannelList = {
  queryChannels: { edges: { node: { id: string; name: string } }[]; pageInfo: { endCursor: string | null; hasNextPage: boolean } };
};
const namesQuery = `query RateChannels($input:QueryChannelInput!){queryChannels(input:$input){edges{node{id name}} pageInfo{hasNextPage endCursor}}}`;
const stateQuery = `query RateChannelNumbers($ids:[ID!]!){channelUpstreamStates(channelIds:$ids){channelId manualMultiplier enabledCredentialCount settings{enabled costMode} keys{keyRef label remark disabled applied{value source observedAt}}}}`;

type Target = { keyID?: number; groupID?: number; name: string };
type Rule = { channelID: number; multiplier: string | number };
type Rates = { rules: Rule[]; inherited: Rule[]; revision: string };
const selectRates = `query KeyChannelRates($keyID:Int,$groupID:Int){keyChannelRates(keyID:$keyID,groupID:$groupID){rules{channelID multiplier} inherited{channelID multiplier} revision}}`;
const saveRates = `mutation SaveKeyChannelRates($keyID:Int,$groupID:Int,$revision:String!,$rules:[KeyChannelRateInput!]!){saveKeyChannelRates(keyID:$keyID,groupID:$groupID,revision:$revision,rules:$rules)}`;

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

function RateForm({ target, initial, project, onClose }: { target: Target; initial: Rates; project: string; onClose: () => void }) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState(() => new Map(initial.rules.map((r) => [r.channelID, String(r.multiplier)])));
  const inherited = useMemo(() => new Map(initial.inherited.map((r) => [r.channelID, String(r.multiplier)])), [initial]);
  const [ids, setIDs] = useState(() => [...new Set([...draft.keys(), ...inherited.keys()])]);
  const identity = useAuthStore((s) => s.auth.user?.id);
  const [search, setSearch] = useState<string | null>(null);
  const term = useDebounce(search ?? '', 250);
  const selection = ids.map((id) => buildGUID('Channel', String(id)));
  const headers = { 'X-Project-ID': project };
  const list = useInfiniteQuery({
    queryKey: ['rateChannelOptions', identity, project, term],
    enabled: search !== null,
    initialPageParam: null as string | null,
    queryFn: async ({ pageParam, signal }) =>
      (
        await graphqlRequest<ChannelList>(
          namesQuery,
          { input: { first: 40, after: pageParam, where: term ? { nameContainsFold: term } : {} } },
          headers,
          { signal }
        )
      ).queryChannels,
    getNextPageParam: (page) => (page.pageInfo.hasNextPage ? page.pageInfo.endCursor : undefined),
  });
  const channels =
    list.data?.pages.flatMap((page) => page.edges.map((e) => ({ id: extractNumberIDAsNumber(e.node.id), name: e.node.name }))) ?? [];
  const upstream = useQuery({
    queryKey: ['rateChannelNumbers', identity, project, selection],
    enabled: selection.length > 0,
    refetchInterval: 30000,
    queryFn: async ({ signal }) => {
      const values = new Map<number, { name?: string; state?: ChannelUpstreamState }>();
      for (let i = 0; i < selection.length; i += 100) {
        const chunk = selection.slice(i, i + 100);
        const names = await graphqlRequest<ChannelList>(namesQuery, { input: { first: chunk.length, where: { idIn: chunk } } }, headers, {
          signal,
        });
        for (const { node } of names.queryChannels.edges) values.set(extractNumberIDAsNumber(node.id), { name: node.name });
        const data = await graphqlRequest<{ channelUpstreamStates: ChannelUpstreamState[] }>(
          stateQuery,
          { ids: names.queryChannels.edges.map((e) => e.node.id) },
          headers,
          { signal }
        );
        for (const state of data.channelUpstreamStates) {
          const id = extractNumberIDAsNumber(state.channelId);
          values.set(id, { ...values.get(id), state });
        }
      }
      return values;
    },
  });
  const change = (id: number, value?: string) =>
    setDraft((previous) => {
      const next = new Map(previous);
      if (value === undefined) next.delete(id);
      else next.set(id, value);
      return next;
    });
  const save = useMutation({
    mutationFn: () =>
      graphqlRequest(
        saveRates,
        {
          keyID: target.keyID,
          groupID: target.groupID,
          revision: initial.revision,
          rules: [...draft].map(([channelID, multiplier]) => ({ channelID, multiplier })),
        },
        { 'X-Project-ID': project }
      ),
    onSuccess: onClose,
  });
  return (
    <>
      <div className='min-h-0 space-y-3 overflow-auto'>
        <Button variant='outline' size='sm' onClick={() => setSearch(search === null ? '' : null)}>
          {t('kg.rate.add')}
        </Button>
        {search !== null && (
          <div className='rounded-md border p-2'>
            <Input
              aria-label={t('kg.rate.search')}
              placeholder={t('kg.rate.search')}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
            <div className='mt-1 max-h-40 overflow-auto'>
              {channels
                .filter((c) => !ids.includes(c.id) && c.name.toLowerCase().includes(search.toLowerCase()))
                .map((c) => (
                  <Button
                    key={c.id}
                    variant='ghost'
                    className='block'
                    onClick={() => {
                      setIDs((previous) => [...previous, c.id]);
                      change(c.id, '');
                      setSearch(null);
                    }}
                  >
                    {c.name}
                  </Button>
                ))}
            </div>
            {list.hasNextPage && (
              <Button variant='ghost' onClick={() => list.fetchNextPage()}>
                {t('kg.rate.more')}
              </Button>
            )}
          </div>
        )}
        {list.error && <p role='alert'>{list.error.message}</p>}
        {upstream.error && <p role='alert'>{upstream.error.message}</p>}
        <table className='w-full text-left text-sm'>
          <thead>
            <tr className='text-muted-foreground border-b'>
              <th className='p-2'>{t('kg.rate.channel')}</th>
              <th>{t('kg.rate.current')}</th>
              {target.keyID && <th>{t('kg.rate.group')}</th>}
              <th>{t(target.keyID ? 'kg.rate.key' : 'kg.rate.group')}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {ids.map((id) => {
              const value = draft.get(id),
                parent = inherited.get(id);
              const state = upstream.data?.get(id)?.state;
              const parentLabel = parent !== undefined ? parent + '×' : state ? rateRange(state).label : t('kg.rate.unavailable');
              const name = upstream.data?.get(id)?.name ?? channels.find((c) => c.id === id)?.name ?? '#' + id;
              return (
                <tr key={id} className='border-b'>
                  <td className='p-2'>{name}</td>
                  <td>
                    <CurrentRate state={upstream.data?.get(id)?.state} />
                  </td>
                  {target.keyID && <td>{parent === undefined ? t('kg.rate.channelDefault') + ' ' + parentLabel : parentLabel}</td>}
                  <td>
                    {value === undefined ? (
                      <Button variant='ghost' size='sm' onClick={() => change(id, parent ?? '')}>
                        {t('kg.rate.inherit')} {parentLabel}
                      </Button>
                    ) : (
                      <label className='flex items-center gap-1'>
                        <Input
                          className='w-24'
                          aria-label={name + ' ' + t('kg.rate.value')}
                          type='number'
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
        {ids.length === 0 && <p className='text-muted-foreground text-sm'>{t('kg.rate.empty')}</p>}
        {save.error && <p role='alert'>{save.error.message}</p>}
      </div>
      <DialogFooter>
        <Button variant='outline' onClick={onClose}>
          {t('common.buttons.cancel')}
        </Button>
        <Button
          disabled={save.isPending || [...draft.values()].some((v) => !v.trim() || !Number.isFinite(Number(v)) || Number(v) < 0)}
          onClick={() => save.mutate()}
        >
          {t('common.buttons.save')}
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
      graphqlRequest<{ keyChannelRates: Rates }>(
        selectRates,
        { keyID: target.keyID, groupID: target.groupID },
        { 'X-Project-ID': project! },
        { signal }
      ),
  });
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent className='flex max-h-[85vh] flex-col sm:max-w-3xl'>
        <DialogHeader>
          <DialogTitle>{t(target.keyID ? 'kg.rate.keyTitle' : 'kg.rate.groupTitle')}</DialogTitle>
          <DialogDescription>
            {target.name} · {t('kg.rate.hint')}
          </DialogDescription>
        </DialogHeader>
        {query.error && <p role='alert'>{query.error.message}</p>}
        {query.isLoading && <p>{t('kg.loading')}</p>}
        {query.data && project && (
          <RateForm key={project} target={target} initial={query.data.keyChannelRates} project={project} onClose={onClose} />
        )}
      </DialogContent>
    </Dialog>
  );
}
