import type { BudgetView } from './data';

// Use the server's clock for the interval and the query's local timestamp
// only for elapsed time. This works even when the browser clock is ahead.
export function grantRefreshDelay(serverTime: string, deadline: string | null, updatedAt: number): number | false {
  if (!deadline) return false;
  const delay = Date.parse(deadline) - Date.parse(serverTime) - (Date.now() - updatedAt) + 1;
  return Math.min(2_147_483_647, Math.max(1, delay));
}

export function budgetGrantRefresh(views: BudgetView[], updatedAt: number): number | false {
  let next: number | false = false;
  for (const view of views) {
    for (const window of view.windows) {
      const delay = grantRefreshDelay(view.serverTime, window.temporaryUntil, updatedAt);
      if (delay !== false && (next === false || delay < next)) next = delay;
    }
  }
  return next;
}
