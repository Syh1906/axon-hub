import { DEVELOPER_IDS } from './constants';

// Only built-in IDs are case-insensitive; custom developer names stay intact.
export function normalizeModelDeveloper(developer: string): string {
  const value = developer.trim().toLowerCase();
  return DEVELOPER_IDS.find((id) => id === value) ?? developer;
}

export function resolveModelDeveloper(developer: string, options: { value: string; label: string }[]): string {
  const value = developer.trim().toLowerCase();
  const option = options.find((item) => item.value.toLowerCase() === value || item.label.toLowerCase() === value);
  return option?.value ?? normalizeModelDeveloper(developer);
}
