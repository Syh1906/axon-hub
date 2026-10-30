import type { ManagementRow } from './data';

export type PendingBatch = { id: string; rows: ManagementRow[] };
const memoryPending = new Map<string, PendingBatch | null>();
const recoveryListeners = new Set<() => void>();
export function subscribePendingRecovery(listener: () => void) {
  recoveryListeners.add(listener);
  return () => {
    recoveryListeners.delete(listener);
  };
}
export function notifyPendingRecovery() {
  recoveryListeners.forEach((listener) => listener());
}
export function pendingKey(project: string | null, user?: string) {
  return `key-management-pending-v1:${user}:${project}`;
}
export function readPending(key: string): PendingBatch | null {
  if (memoryPending.has(key)) return memoryPending.get(key) ?? null;
  try {
    const value = JSON.parse(sessionStorage.getItem(key) ?? 'null');
    return value &&
      typeof value.id === 'string' &&
      Array.isArray(value.rows) &&
      value.rows.every((r: ManagementRow) => typeof r.rowID === 'string')
      ? value
      : null;
  } catch {
    return null;
  }
}
export function savePending(key: string, value: PendingBatch | null) {
  // 浏览器禁用存储时仍在当前页面生命周期保留原批次，允许收起弹窗后恢复。
  memoryPending.set(key, value);
  try {
    if (value) sessionStorage.setItem(key, JSON.stringify(value));
    else sessionStorage.removeItem(key);
  } catch {
    /* 当前页面的内存副本仍可恢复。 */
  }
}
