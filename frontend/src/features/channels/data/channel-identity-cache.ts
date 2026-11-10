import type { QueryClient } from '@tanstack/react-query';

// Refresh current labels after channel edits. The server reuses its usage
// aggregate cache; unrelated leaderboard dimensions stay untouched.
export function invalidateChannelIdentityQueries(queryClient: QueryClient) {
  return queryClient.invalidateQueries({
    queryKey: ['consumption', 'leaderboard'],
    predicate: (query) => (query.queryKey[4] as { dimension?: string } | undefined)?.dimension === 'CHANNEL',
  });
}
