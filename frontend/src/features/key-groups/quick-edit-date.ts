// Keep datetime-local serialization and parsing on the same browser offset basis.
export function localExpiry(value: string | null): string {
  if (!value) return '';
  const date = new Date(value);
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
}

export function expiryInstant(value: string, original?: string | null): Date {
  // Unchanged input must retain seconds and the original occurrence of a repeated DST hour.
  if (original && localExpiry(original) === value) return new Date(original);
  const wallTime = new Date(value + 'Z');
  if (!Number.isFinite(wallTime.getTime())) return new Date(NaN);
  const first = new Date(wallTime.getTime() + wallTime.getTimezoneOffset() * 60000);
  // The target date can fall on the other side of a daylight-saving transition.
  const result = new Date(wallTime.getTime() + first.getTimezoneOffset() * 60000);
  // A skipped local hour cannot be saved without changing what the user entered.
  return localExpiry(result.toISOString()) === value ? result : new Date(NaN);
}
