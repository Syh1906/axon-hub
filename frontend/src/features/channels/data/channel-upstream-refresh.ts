import type { QueryClient } from '@tanstack/react-query';
import type { ChannelUpstreamState, UpstreamKey } from './channel-upstream';

export function hasUsableUpstreamRate(key: UpstreamKey, now = Date.now()) {
  const { rate, rateState } = key.snapshot;
  return Boolean(
    rate &&
    (rateState === 'SUCCESS' || rateState === 'ERROR') &&
    now - Date.parse(rate.syncedAt) <= 30 * 60_000 &&
    now - Date.parse(rate.observedAt) <= 30 * 60_000 &&
    (!rate.validUntil || now < Date.parse(rate.validUntil))
  );
}

export async function publishUpstreamStates(client: QueryClient, channels: ChannelUpstreamState[]) {
  const states = new Map(channels.map((state) => [state.channelId, state]));
  const filters = {
    queryKey: ['channel-upstream'],
    predicate: (query: { queryKey: readonly unknown[] }) => (query.queryKey[1] as string[]).some((id) => states.has(id)),
  };
  // 先取消旧读取（包括后台重取），再发布刷新结果。
  await client.cancelQueries(filters);
  client.setQueriesData<ChannelUpstreamState[]>(filters, (old) => old?.map((state) => states.get(state.channelId) ?? state));
  // 初次读取被取消时也能补齐缓存；这里读取的是已经提交的新快照。
  await client.invalidateQueries(filters);
}

export type UpstreamRefreshOptions = {
  channelIds: string[];
  force?: boolean;
  includeDisabled?: boolean;
  keyRef?: string;
  silent?: boolean;
};
type RefreshResult = { channels: ChannelUpstreamState[]; refreshed: number; skipped: number; failed: number };

export async function refreshUpstreamChannels(
  states: ChannelUpstreamState[],
  options: UpstreamRefreshOptions,
  refreshKey: (channelId: string, keyRef: string) => Promise<RefreshResult>
): Promise<RefreshResult> {
  const result: RefreshResult = { channels: [], refreshed: 0, skipped: 0, failed: 0 };
  let next = 0;
  const worker = async () => {
    while (next < states.length) {
      const state = states[next++];
      let latest = state,
        refreshed = false,
        failed = false;
      const keys = state.keys.filter((key) => (options.keyRef ? key.keyRef === options.keyRef : !key.disabled));
      if (options.keyRef && !keys.length) throw new Error('Upstream key no longer exists');
      if (state.settings.enabled || options.includeDisabled) {
        for (const key of keys) {
          if (!options.includeDisabled && key.snapshot.balanceState === 'UNSUPPORTED' && key.snapshot.rateState === 'UNSUPPORTED') continue;
          try {
            const response = await refreshKey(state.channelId, key.keyRef);
            latest = response.channels[0];
            refreshed ||= response.refreshed > 0;
            failed ||= response.failed > 0;
          } catch {
            // 单 Key 失败不撤销其他 Key 的成功结果，也不阻止后续 Key。
            failed = true;
          }
        }
      }
      result.channels.push(latest);
      if (refreshed) result.refreshed++;
      if (failed) result.failed++;
      if (!refreshed && !failed) result.skipped++;
    }
  };
  await Promise.all(Array.from({ length: Math.min(3, states.length) }, worker));
  return result;
}
