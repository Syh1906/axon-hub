import type { SortingState } from '@tanstack/react-table';

export const CHANNEL_SORT_FIELDS = [
  'priority',
  'orderingWeight',
  'costMultiplier',
  'name',
  'provider',
  'status',
  'createdAt',
  'updatedAt',
] as const;
export const DEFAULT_CHANNEL_SORT: SortingState = [{ id: 'createdAt', desc: true }];
const STORAGE_KEY = 'channels-table-default-sorting-v1';

export function readDefaultChannelSort(): SortingState {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? localStorage.getItem('channels-table-sorting') ?? 'null');
    if (Array.isArray(value) && value.length === 1 && CHANNEL_SORT_FIELDS.includes(value[0]?.id) && typeof value[0]?.desc === 'boolean') {
      return [{ id: value[0].id, desc: value[0].desc }];
    }
  } catch {
    /* A missing or invalid preference uses the existing creation-time order. */
  }
  return DEFAULT_CHANNEL_SORT;
}

export function saveDefaultChannelSort(value: SortingState) {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(value));
}
