import { QueryClient } from '@tanstack/react-query';
import { SelfUsageError } from './api';

export const selfUsageQueryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (count, error) => {
        if (
          error instanceof SelfUsageError &&
          (error.status === 400 || error.status === 401 || error.status === 403 || error.status === 408 || error.status === 429 || error.code === 'BAD_USER_INPUT')
        )
          return false;
        return count < 2;
      },
      staleTime: 30_000,
      refetchOnWindowFocus: false,
    },
  },
});
