import type { QueryClient } from '@tanstack/react-query';

// Refresh current labels after channel edits. The server reuses its usage
// aggregate cache; unrelated leaderboard dimensions stay untouched.
export function invalidateChannelIdentityQueries(queryClient: QueryClient) {
  return queryClient.invalidateQueries({
    predicate: (query) => query.meta?.channelLeaderboard === true,
  });
}
