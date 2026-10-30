// Calendar shortcuts preserve local wall-clock time and clamp the target day.
// The datetime-local editor explicitly labels this browser timezone.
export function extendBudgetExpiry(expiry: string, months: number, now = new Date()): Date {
  const current = expiry ? new Date(expiry) : now;
  const base = new Date(Math.max(now.getTime(), current.getTime()));
  const day = base.getDate();
  base.setDate(1);
  base.setMonth(base.getMonth() + months);
  const lastDay = new Date(base.getFullYear(), base.getMonth() + 1, 0).getDate();
  base.setDate(Math.min(day, lastDay));
  return base;
}
