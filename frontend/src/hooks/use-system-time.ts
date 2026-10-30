import { useCallback } from 'react';
import { useQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useTranslation } from 'react-i18next';
import { formatSystemTime, type TimePattern } from '@/utils/system-time';

export function useSystemTime() {
  const { i18n } = useTranslation();
  const { data: timezone } = useQuery({
    queryKey: ['system-display-timezone'],
    staleTime: 300_000,
    queryFn: async ({ signal }) => {
      const data = await graphqlRequest<{ systemDisplayTimezone: string }>('query { systemDisplayTimezone }', {}, undefined, { signal });
      return data.systemDisplayTimezone;
    },
  });
  const locale = i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US';
  const format = useCallback(
    (value: Date | string | number, pattern: TimePattern = 'yyyy-MM-dd HH:mm:ss') =>
      timezone ? formatSystemTime(value, timezone, pattern, locale) : '—',
    [timezone, locale]
  );
  return { timezone, format };
}
