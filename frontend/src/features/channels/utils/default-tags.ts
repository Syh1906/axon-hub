const key = 'channels-default-tags-v1';
export function readDefaultTags(): string[] {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(key) ?? '[]');
    return Array.isArray(value) && value.every((item) => typeof item === 'string') ? [...new Set(value)] : [];
  } catch {
    return [];
  }
}
export function saveDefaultTags(tags: string[]) {
  localStorage.setItem(key, JSON.stringify(tags));
}

// An empty ID list is not a false predicate in Ent. Do not dispatch that query.
export function resolveChannelTagSelection(tags: string[], channels?: { id: string; tags?: string[] | null }[]) {
  const ids =
    tags.length > 1
      ? (channels ?? []).filter((channel) => channel.tags?.some((tag) => tags.includes(tag))).map((channel) => channel.id)
      : undefined;
  return { ids, disableQuery: ids !== undefined && ids.length === 0 };
}
