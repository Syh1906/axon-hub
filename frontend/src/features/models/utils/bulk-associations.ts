import type { ModelAssociation } from '../data/schema';

export const MAX_ASSOCIATION_PRIORITY = 10;
export function clampAssociationPriority(value: number) {
  return Math.max(0, Math.min(MAX_ASSOCIATION_PRIORITY, Math.trunc(value) || 0));
}

export function sameAssociation(a: ModelAssociation, b: ModelAssociation) {
  if (a.disabled || a.when?.enabled) return false;
  if (a.type !== b.type) return false;
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
