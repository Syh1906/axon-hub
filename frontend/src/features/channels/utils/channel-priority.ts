import type { Channel } from '../data/schema';

// Without a list filter, show the smallest configured priority on the channel's
// own tags. This is a display default, not an override of request tag scope.
export function channelDisplayPriority(channel: Pick<Channel, 'tags' | 'settings'>, tag = '') {
  const tags = tag ? [tag] : (channel.tags ?? []);
  const entries = (channel.settings?.tagPriorities ?? []).filter((entry) => tags.includes(entry.tag) && channel.tags?.includes(entry.tag));
  const entry = entries.reduce<(typeof entries)[number] | undefined>(
    (best, item) => (!best || item.priority < best.priority ? item : best),
    undefined
  );
  return { tag: tag || entry?.tag || tags[0] || '', priority: entry?.priority };
}
