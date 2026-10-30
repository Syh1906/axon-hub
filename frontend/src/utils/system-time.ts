export type TimePattern =
  | 'yyyy-MM-dd'
  | 'yyyy-MM-dd HH:mm'
  | 'yyyy-MM-dd HH:mm:ss'
  | 'MM-dd HH:mm'
  | 'HH:mm'
  | 'HH:mm:ss.SSS'
  | 'yyyy'
  | 'PP'
  | 'PPpp';

export function formatSystemTime(value: Date | string | number, timezone: string, pattern: TimePattern, locale: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  if (pattern === 'PP' || pattern === 'PPpp') {
    return new Intl.DateTimeFormat(locale, {
      timeZone: timezone,
      dateStyle: 'medium',
      ...(pattern === 'PPpp' ? { timeStyle: 'medium' } : {}),
    }).format(date);
  }
  const parts = new Intl.DateTimeFormat('en-GB', {
    timeZone: timezone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23',
  }).formatToParts(date);
  const fields = Object.fromEntries(parts.map(({ type, value }) => [type, value]));
  const tokens: Record<string, string> = {
    yyyy: fields.year,
    MM: fields.month,
    dd: fields.day,
    HH: fields.hour,
    mm: fields.minute,
    ss: fields.second,
    SSS: String(date.getUTCMilliseconds()).padStart(3, '0'),
  };
  return pattern.replace(/yyyy|MM|dd|HH|mm|ss|SSS/g, (token) => tokens[token]);
}
