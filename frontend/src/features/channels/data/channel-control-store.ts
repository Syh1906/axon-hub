import { replaceEqualDeep } from '@tanstack/react-query';
import type { Channel, ChannelProbePoint } from './schema';

export type ChannelControlData = Partial<Channel> & { probePoints?: ChannelProbePoint[] };
type Listener = () => void;

// 只有订阅该渠道的动态单元格收到通知；表格行、选中项和编辑草稿不随轮询重建。
export function createChannelControlStore() {
  const values = new Map<string, ChannelControlData>();
  const listeners = new Map<string, Set<Listener>>();
  return {
    get: (id: string) => values.get(id),
    retain(ids: string[]) {
      const active = new Set(ids);
      for (const id of values.keys()) if (!active.has(id)) values.delete(id);
    },
    subscribe(id: string, listener: Listener) {
      const set = listeners.get(id) ?? new Set<Listener>();
      listeners.set(id, set);
      set.add(listener);
      return () => {
        set.delete(listener);
        if (!set.size) listeners.delete(id);
      };
    },
    publish(id: string, patch: ChannelControlData) {
      const previous = values.get(id);
      const next = replaceEqualDeep(previous, { ...previous, ...patch });
      if (next === previous) return;
      values.set(id, next);
      listeners.get(id)?.forEach((notify) => notify());
    },
  };
}
