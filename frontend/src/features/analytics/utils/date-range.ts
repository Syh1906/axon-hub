export type AnalyticsQuickRange =
  | 'today'
  | 'yesterday'
  | 'thisWeek'
  | 'thisMonth'
  | 'all';

export interface AnalyticsDateRange {
  startTime: string | null;
  endTime: string | null;
}

function calendarDateInTimezone(date: Date, timezone: string): Date {
  let formatter: Intl.DateTimeFormat;
  try {
    formatter = new Intl.DateTimeFormat('en-US', {
      timeZone: timezone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    });
  } catch {
    formatter = new Intl.DateTimeFormat('en-US', {
      timeZone: 'UTC',
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    });
  }

  const parts = Object.fromEntries(
    formatter
      .formatToParts(date)
      .filter((part) => part.type !== 'literal')
      .map((part) => [part.type, part.value])
  );
  return new Date(
    Date.UTC(Number(parts.year), Number(parts.month) - 1, Number(parts.day))
  );
}

function formatCalendarDate(date: Date): string {
  const year = date.getUTCFullYear();
  const month = String(date.getUTCMonth() + 1).padStart(2, '0');
  const day = String(date.getUTCDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
}

export function getAnalyticsQuickDateRange(
  range: AnalyticsQuickRange,
  timezone: string,
  earliestDate: string | null = null,
  now: Date = new Date()
): AnalyticsDateRange {
  const today = calendarDateInTimezone(now, timezone);
  const endTime = formatCalendarDate(today);

  if (range === 'all') {
    return {
      startTime: earliestDate,
      endTime: earliestDate ? endTime : null,
    };
  }

  const start = new Date(today);
  switch (range) {
    case 'yesterday':
      start.setUTCDate(start.getUTCDate() - 1);
      return {
        startTime: formatCalendarDate(start),
        endTime: formatCalendarDate(start),
      };
    case 'thisWeek': {
      const daysSinceMonday = (start.getUTCDay() + 6) % 7;
      start.setUTCDate(start.getUTCDate() - daysSinceMonday);
      break;
    }
    case 'thisMonth':
      start.setUTCDate(1);
      break;
    case 'today':
      break;
  }

  return {
    startTime: formatCalendarDate(start),
    endTime,
  };
}
import { buildDateRangeWhereClause, DEFAULT_END_TIME, DEFAULT_START_TIME, type DateTimeRangeValue, type TimeValue } from '@/utils/date-range';

interface RangeBounds {
  range: DateTimeRangeValue | undefined;
  /**
   * Original timestamps. The picker only edits local wall-clock fields, which
   * cannot distinguish the two instants of a fall-back DST hour and drops the
   * sub-second part, so an untouched bound is reused verbatim instead of being
   * rebuilt.
   */
  startTime?: string;
  endTime?: string;
}

function toTimeValue(date: Date): TimeValue {
  const pad = (value: number) => String(value).padStart(2, '0');
  return { hh: pad(date.getHours()), mm: pad(date.getMinutes()), ss: pad(date.getSeconds()) };
}

function parseTimestamp(value?: string | null): Date | undefined {
  if (!value) return undefined;
  const date = new Date(value);

  return Number.isNaN(date.getTime()) ? undefined : date;
}

function sameTimeValue(left: TimeValue, right: TimeValue): boolean {
  return left.hh === right.hh && left.mm === right.mm && left.ss === right.ss;
}

/** Local midnight of the given day, used by the quick range presets. */
export function startOfLocalDay(date: Date): Date {
  const day = new Date(date);
  day.setHours(0, 0, 0, 0);

  return day;
}

/**
 * Converts the filter timestamps into the value shape used by the shared
 * date range picker, so the selected range can be edited with second precision.
 * The sub-second component is returned alongside because the picker only edits
 * whole seconds; dropping it would silently widen the stored range on confirm.
 */
export function timestampsToDateTimeRange(startTime?: string | null, endTime?: string | null): RangeBounds {
  const start = parseTimestamp(startTime);
  const end = parseTimestamp(endTime);

  if (!start && !end) return { range: undefined };

  // Keep each side independent: filling a missing bound with the other one
  // would turn an open-ended filter into a single-day range on the next confirm.
  return {
    range: {
      from: start ? startOfLocalDay(start) : undefined,
      to: end ? startOfLocalDay(end) : undefined,
      startTime: start ? toTimeValue(start) : DEFAULT_START_TIME,
      endTime: end ? toTimeValue(end) : DEFAULT_END_TIME,
    },
    startTime: start?.toISOString(),
    endTime: end?.toISOString(),
  };
}

/**
 * Converts a picked date range into RFC3339 timestamps for the API.
 * The start is inclusive at the first millisecond of the selected second and
 * the end is inclusive at its last millisecond.
 *
 * When the picked range still matches the stored one, the stored sub-second
 * components are reused so confirming an untouched range is a no-op. A side
 * whose time was edited falls back to the `.000`/`.999` boundaries.
 */
export function dateTimeRangeToTimestamps(
  range: DateTimeRangeValue | undefined,
  previous?: RangeBounds
): {
  startTime: string | null;
  endTime: string | null;
} {
  const { createdAtGTE, createdAtLTE } = buildDateRangeWhereClause(range);

  const previousRange = previous?.range;

  // A side counts as untouched when its day and its whole-second time still
  // match; only then is its stored sub-second component carried over.
  const isUnchanged = (
    previousDay: Date | undefined,
    day: Date | undefined,
    previousTime: TimeValue | undefined,
    time: TimeValue | undefined
  ): boolean => !!previousDay && !!day && !!previousTime && !!time && previousDay.getTime() === day.getTime() && sameTimeValue(previousTime, time);

  const startUnchanged = isUnchanged(previousRange?.from, range?.from, previousRange?.startTime, range?.startTime);

  const endUnchanged = isUnchanged(previousRange?.to, range?.to, previousRange?.endTime, range?.endTime);

  return {
    startTime: startUnchanged ? (previous?.startTime ?? null) : (createdAtGTE ?? null),
    endTime: endUnchanged ? (previous?.endTime ?? null) : (createdAtLTE ?? null),
  };
}

// 日历筛选仍按系统时区解释，转换为 beta12 接口要求的精确时间。
export function calendarRangeToTimestamps(start: string | null, end: string | null, timezone: string) {
 const boundary = (day: string, nextDay: boolean): string => {
  const [year, month, date] = day.split('-').map(Number);
  const target = Date.UTC(year, month - 1, date + (nextDay ? 1 : 0));
  const formatter = new Intl.DateTimeFormat('en-CA', { timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit' });
  const dateNumber = (ms: number) => {
   const parts = Object.fromEntries(formatter.formatToParts(new Date(ms)).map(({type,value}) => [type,value]));
   return Date.UTC(Number(parts.year),Number(parts.month)-1,Number(parts.day));
  };
  // 找到该时区目标日期的第一个毫秒，兼容午夜跳时及 23/25 小时的日期。
  let lo = target - 36 * 3600000, hi = target + 36 * 3600000;
  while (lo < hi) { const mid = Math.floor((lo + hi) / 2); if (dateNumber(mid) < target) lo = mid + 1; else hi = mid; }
  return new Date(lo - (nextDay ? 1 : 0)).toISOString();
 };
 return { startTime: start ? boundary(start, false) : null, endTime: end ? boundary(end, true) : null };
}

export function analyticsCalendarDate(timestamp: string | null | undefined, timezone: string): string | null {
 return timestamp ? formatCalendarDate(calendarDateInTimezone(new Date(timestamp), timezone)) : null;
}
