export type DatePreset = 'today' | 'yesterday' | 'last7' | 'last30' | 'thisMonth';
export type DateRange = { start: string; end: string };

export function calendarDay(now: Date, timezone: string): string {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: timezone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).formatToParts(now);
  const value = (type: string) => parts.find((part) => part.type === type)?.value;
  return `${value('year')}-${value('month')}-${value('day')}`;
}

function dayNumber(value: string): number | null {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return null;
  const [year, month, day] = value.split('-').map(Number);
  const timestamp = Date.UTC(year, month - 1, day);
  const date = new Date(timestamp);
  if (date.getUTCFullYear() !== year || date.getUTCMonth() !== month - 1 || date.getUTCDate() !== day) return null;
  return timestamp / 86_400_000;
}

function fromDay(day: number): string {
  return new Date(day * 86_400_000).toISOString().slice(0, 10);
}

export function presetRange(preset: DatePreset, timezone: string, now: Date = new Date()): DateRange {
  const today = calendarDay(now, timezone);
  const day = dayNumber(today)!;
  if (preset === 'yesterday') return { start: fromDay(day - 1), end: fromDay(day - 1) };
  if (preset === 'last7') return { start: fromDay(day - 6), end: today };
  if (preset === 'last30') return { start: fromDay(day - 29), end: today };
  if (preset === 'thisMonth') return { start: `${today.slice(0, 7)}-01`, end: today };
  return { start: today, end: today };
}

export function validRange(range: DateRange, maxDays = 90): boolean {
  const start = dayNumber(range.start);
  const end = dayNumber(range.end);
  return start !== null && end !== null && end >= start && end - start + 1 <= Math.min(maxDays, 90);
}

export function chartDay(value: string, locale: string): string {
  const day = dayNumber(value);
  if (day === null) return value;
  return new Intl.DateTimeFormat(locale, { month: '2-digit', day: '2-digit', timeZone: 'UTC' }).format(new Date(day * 86_400_000));
}
