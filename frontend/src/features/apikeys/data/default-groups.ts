export function defaultGroupsKey(user: string, project: string) {
  return `apikeys-default-groups-v1:${user}:${project}`;
}
export function readDefaultGroups(key: string): number[] {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(key) ?? '[]');
    return Array.isArray(value) && value.every((id) => Number.isSafeInteger(id) && id >= 0) ? [...new Set(value)] : [];
  } catch {
    return [];
  }
}
export function saveDefaultGroups(key: string, ids: number[]) {
  localStorage.setItem(key, JSON.stringify(ids));
}

// Keep removed saved choices visible until the user explicitly removes them.
export function selectedGroupOptions(ids: number[], options: { id: number; name: string }[], unavailable: (id: number) => string) {
  const byID = new Map(options.map((option) => [option.id, option]));
  return ids.map((id) => byID.get(id) ?? { id, name: unavailable(id) });
}
export function groupFilter(ids: number[]) {
  if (!ids.length) return undefined;
  const groups = ids.filter((id) => id > 0);
  const conditions = [...(groups.length ? [{ groupRefIn: groups }] : []), ...(ids.includes(0) ? [{ groupRefIsNil: true }] : [])];
  return { or: conditions };
}
