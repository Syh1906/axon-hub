import type { Channel } from '../data/schema';

// Without a filter, show the global value. Existing channels without a global
// value retain their smallest configured tag value as a list-only default.
export function channelDisplayPriority(channel: Pick<Channel, 'tags' | 'settings'>, tag = '') {
  const globalPriority = channel.settings?.globalPriority;
  if (!tag && globalPriority != null) return { tag: '', priority: globalPriority };
  const tags = tag ? [tag] : (channel.tags ?? []);
  const entries = (channel.settings?.tagPriorities ?? []).filter((entry) => tags.includes(entry.tag) && channel.tags?.includes(entry.tag));
  const entry = entries.reduce<(typeof entries)[number] | undefined>(
    (best, item) => (!best || item.priority < best.priority ? item : best),
    undefined
  );
  return { tag: tag || entry?.tag || tags[0] || '', priority: entry?.priority ?? globalPriority ?? undefined };
}

// null restores inheritance; undefined is an invalid draft, including overflow.
export function parseChannelPriority(value: string): number | null | undefined {
  if (!value.trim()) return null;
  const priority = Number(value);
  return Number.isInteger(priority) && priority >= -2147483648 && priority <= 2147483647 ? priority : undefined;
}
