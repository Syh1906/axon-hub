import { createContext, useCallback, useContext, useEffect, useMemo, useState, useSyncExternalStore, type ReactNode } from 'react';
import { useQueries } from '@tanstack/react-query';
import { createChannelControlStore, type ChannelControlData } from '../data/channel-control-store';
import { channelControlBatches, channelLiveQueryOptions } from '../data/channel-control-query';
import { channelProbeQueryOptions } from '../data/channel-probe';
import { channelUpstreamQueryOptions, useRefreshUpstreamOnPage } from '../data/channel-upstream';
import { channelTodayUsageQueryOptions } from '../data/channel-usage';
import type { Channel } from '../data/schema';

const ControlStoreContext = createContext<ReturnType<typeof createChannelControlStore> | null>(null);

function ChannelControlQueries({ ids, load, health, upstream, usage, canWrite }: {
  ids: string[]; load: boolean; health: boolean; upstream: boolean; usage: boolean; canWrite: boolean;
}) {
  const store = useContext(ControlStoreContext)!;
  const batches = useMemo(() => channelControlBatches(ids), [ids]);
  const live = useQueries({ queries: batches.map((batch) => channelLiveQueryOptions(batch, load)) });
  const probes = useQueries({ queries: batches.map((batch) => ({ ...channelProbeQueryOptions(batch), enabled: health })) });
  const upstreams = useQueries({ queries: batches.map((batch) => ({ ...channelUpstreamQueryOptions(batch), enabled: upstream })) });
  const usages = useQueries({ queries: batches.map((batch) => ({ ...channelTodayUsageQueryOptions(batch), enabled: usage })) });
  const readyUpstreams = upstreams.every((result) => result.isSuccess);
  useRefreshUpstreamOnPage(ids, canWrite && upstream && readyUpstreams);
  useEffect(() => {
    store.retain(ids);
    live.forEach((result) => result.data?.forEach((row) => store.publish(row.id, row)));
    probes.forEach((result) => result.data?.forEach((row) => store.publish(row.channelID, { probePoints: row.points })));
    upstreams.forEach((result, index) => {
      result.data?.forEach((state) => store.publish(state.channelId, { upstreamState: state }));
      batches[index].forEach((id) => store.publish(id, { upstreamLoadError: result.isError }));
    });
    usages.forEach((result, index) => {
      result.data?.forEach((todayUsage) => store.publish(todayUsage.channelId, { todayUsage }));
      batches[index].forEach((id) => store.publish(id, { todayUsageError: result.isError }));
    });
  }, [store, ids, batches, live, probes, upstreams, usages]);
  return null;
}

export function ChannelControlsProvider({ ids, visibility, canWrite, children }: {
  ids: string[]; visibility: Record<string, boolean>; canWrite: boolean; children: ReactNode;
}) {
  const [store] = useState(createChannelControlStore);
  return (
    <ControlStoreContext.Provider value={store}>
      <ChannelControlQueries ids={ids} load={visibility.load !== false} health={visibility.health !== false}
        upstream={visibility.upstreamBalance !== false || visibility.costMultiplier !== false}
        usage={visibility.todayUsage !== false} canWrite={canWrite} />
      {children}
    </ControlStoreContext.Provider>
  );
}

const noSubscription = () => () => {};
export function useChannelControlData(channel: Channel, fields: readonly (keyof ChannelControlData)[]) {
  const store = useContext(ControlStoreContext);
  const subscribe = useCallback((notify: () => void) => store?.subscribe(channel.id, notify) ?? noSubscription(), [store, channel.id]);
  const fieldKey = fields.join('|');
  const getSnapshot = useMemo(() => {
    const selectedFields = fieldKey.split('|') as (keyof ChannelControlData)[];
    let previous: ChannelControlData | undefined;
    let selected: ChannelControlData | undefined;
    return () => {
      const next = store?.get(channel.id);
      if (!selected || selectedFields.some((field) => previous?.[field] !== next?.[field])) {
        selected = Object.fromEntries(selectedFields.filter((field) => next && field in next).map((field) => [field, next![field]]));
      }
      previous = next;
      return selected;
    };
  }, [store, channel.id, fieldKey]);
  const state = useSyncExternalStore(subscribe, getSnapshot);
  return useMemo(() => ({ ...channel, ...state }), [channel, state]);
}
