import { extractNumberIDAsNumber } from '@/lib/utils';

export type SelectableChannel = { id: string | number; name: string; type?: string; tags?: string[] | null };

// Labels may change with names/tags; the selected value always remains the ID.
export function buildChannelSelectOptions(channels: SelectableChannel[], typeLabel: (type: string) => string = (type) => type) {
  const names = new Map<string, number>();
  for (const channel of channels) names.set(channel.name, (names.get(channel.name) ?? 0) + 1);
  return channels.map((channel) => {
    const id = typeof channel.id === 'number' ? channel.id : extractNumberIDAsNumber(channel.id);
    const label =
      names.get(channel.name)! > 1
        ? [channel.name, ...(channel.tags ?? []), channel.type && typeLabel(channel.type), `#${id}`].filter(Boolean).join(' · ')
        : channel.name;
    return { id, label, keywords: [channel.name, ...(channel.tags ?? []), channel.type ?? '', `#${id}`] };
  });
}
