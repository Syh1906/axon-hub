import type { ModelAssociation } from '../data/schema';

export const MAX_ASSOCIATION_PRIORITY = 10;
export function clampAssociationPriority(value: number) {
  return Math.max(0, Math.min(MAX_ASSOCIATION_PRIORITY, Math.trunc(value) || 0));
}

export function sameAssociation(a: ModelAssociation, b: ModelAssociation) {
  if (a.disabled || a.when?.enabled) return false;
  if (a.type !== b.type) return false;
  const excludes = a.regex?.exclude ?? a.modelId?.exclude;
  if (excludes?.some((item) => item.channelIds?.length || item.channelTags?.length || item.channelNamePattern)) return false;
  if (a.type === 'channel_request') return a.channelRequest?.channelId === b.channelRequest?.channelId;
  if (a.type === 'channel_regex')
    return a.channelRegex?.channelId === b.channelRegex?.channelId && a.channelRegex?.pattern === b.channelRegex?.pattern;
  if (a.type === 'model') return a.modelId?.modelId === b.modelId?.modelId;
  if (a.type === 'regex') return a.regex?.pattern === b.regex?.pattern;
  if (a.type === 'channel_tags_regex')
    return (
      a.channelTagsRegex?.pattern === b.channelTagsRegex?.pattern &&
      JSON.stringify([...(a.channelTagsRegex?.channelTags ?? [])].sort()) ===
        JSON.stringify([...(b.channelTagsRegex?.channelTags ?? [])].sort())
    );
  if (a.type === 'channel_model')
    return a.channelModel?.channelId === b.channelModel?.channelId && a.channelModel?.modelId === b.channelModel?.modelId;
  if (a.type === 'channel_tags_model')
    return (
      a.channelTagsModel?.modelId === b.channelTagsModel?.modelId &&
      JSON.stringify([...(a.channelTagsModel?.channelTags ?? [])].sort()) ===
        JSON.stringify([...(b.channelTagsModel?.channelTags ?? [])].sort())
    );
  return false;
}
export function appendAssociations(existing: ModelAssociation[], candidates: ModelAssociation[]) {
  const result = [...existing];
  for (const candidate of candidates) if (!result.some((item) => sameAssociation(item, candidate))) result.push(candidate);
  return result;
}

export const associationTypeLabels = {
  channel_request: 'channelRequest',
  channel_model: 'channelModel',
  channel_regex: 'channelRegex',
  channel_tags_model: 'channelTagsModel',
  channel_tags_regex: 'channelTagsRegex',
  model: 'model',
  regex: 'regex',
} as const;

export function buildBulkAssociations(
  type: ModelAssociation['type'],
  channelIds: number[],
  tags: string[],
  value: string,
  priority: number
): ModelAssociation[] {
  const base = { type, priority, disabled: false };
  const text = value.trim();
  if (type === 'channel_request') return channelIds.map((channelId) => ({ ...base, channelRequest: { channelId } }));
  if (!text) return [];
  switch (type) {
    case 'channel_model':
      return channelIds.map((channelId) => ({ ...base, channelModel: { channelId, modelId: text } }));
    case 'channel_regex':
      return channelIds.map((channelId) => ({ ...base, channelRegex: { channelId, pattern: text } }));
    case 'channel_tags_model':
      return tags.length ? [{ ...base, channelTagsModel: { channelTags: tags, modelId: text } }] : [];
    case 'channel_tags_regex':
      return tags.length ? [{ ...base, channelTagsRegex: { channelTags: tags, pattern: text } }] : [];
    case 'model':
      return [{ ...base, modelId: { modelId: text } }];
    case 'regex':
      return [{ ...base, regex: { pattern: text } }];
  }
}
