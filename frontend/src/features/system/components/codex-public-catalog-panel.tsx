import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { RefreshCw } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useAuthStore } from '@/stores/authStore';
import { apiRequest } from '@/lib/api-client';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';

type Schedule = { automatic: boolean; minutes: number };
type Catalog = Schedule & {
  blocked: boolean;
  checkedAt: string | null;
  changed: number;
  lastError?: string;
  entries: { slug: string; context: number | null; maximum: number | null; efforts: string[]; defaultEffort: string | null }[];
};
const endpoint = '/admin/codex/public-catalog';

export function CodexPublicCatalogPanel({ canEdit }: { canEdit: boolean }) {
  const { t, i18n } = useTranslation();
  const client = useQueryClient();
  const userId = useAuthStore((state) => state.auth.user?.id);
  const key = ['codex-public-catalog', userId];
  const [expanded, setExpanded] = useState(false);
  const query = useQuery({
    queryKey: key,
    queryFn: ({ signal }) => apiRequest<Catalog>(endpoint, { requireAuth: true, signal }),
    refetchInterval: 60_000,
  });
  const action = useMutation({
    onMutate: () => ({ checkedAt: query.data?.checkedAt }),
    mutationFn: (schedule: Schedule | 'check') =>
      schedule === 'check'
        ? apiRequest<Catalog>(endpoint + '/check', { method: 'POST', requireAuth: true })
        : apiRequest<Catalog>(endpoint, { method: 'PUT', requireAuth: true, body: schedule }),
    onSuccess: (result) => client.setQueryData(key, result),
    onError: () => client.invalidateQueries({ queryKey: key }),
  });
  const data = query.data;
  const saveError = action.variables !== 'check' ? action.error?.message : undefined;
  const checkError = data?.lastError || (action.variables === 'check' && action.context?.checkedAt === data?.checkedAt ? action.error?.message : undefined);
  const disabled = !canEdit || data?.blocked || action.isPending;
  const format = (value: number | null) => (value == null ? '—' : value.toLocaleString(i18n.language));
  return (
    <Card data-testid='codex-public-catalog'>
      <CardHeader>
        <CardTitle>{t('system.codex.public.title')}</CardTitle>
        <CardDescription>{t('system.codex.public.description')}</CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        {!data && (query.isPending ? t('common.loading') : <p role='alert'>{t('system.codex.public.loadFailed')}</p>)}
        {data && (
          <>
            <div className='flex flex-wrap items-center justify-between gap-3 rounded-lg border p-4'>
              <Label htmlFor='public-catalog-auto'>{t('system.codex.public.auto')}</Label>
              <div className='flex items-center gap-3'>
                <Switch
                  id='public-catalog-auto'
                  checked={data.automatic && !data.blocked}
                  disabled={disabled}
                  onCheckedChange={(automatic) => action.mutate({ automatic, minutes: data.minutes })}
                />
                <Select
                  value={String(data.minutes)}
                  disabled={disabled || !data.automatic}
                  onValueChange={(value) => action.mutate({ automatic: true, minutes: Number(value) })}
                >
                  <SelectTrigger aria-label={t('system.codex.public.frequency')}>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {[5, 15, 30, 60].map((minutes) => (
                      <SelectItem key={minutes} value={String(minutes)}>
                        {t('system.codex.public.interval', { minutes })}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
            <div className='flex flex-wrap items-center justify-between gap-3'>
              <div className='space-y-1 text-sm' role='status'>
                <p>
                  {data.checkedAt
                    ? t('system.codex.public.checked', { time: new Date(data.checkedAt).toLocaleString(i18n.language) })
                    : t('system.codex.public.never')}
                </p>
                {data.checkedAt && !checkError && (
                  <p className='text-muted-foreground'>
                    {data.changed ? t('system.codex.public.changed', { count: data.changed }) : t('system.codex.public.unchanged')}
                  </p>
                )}
              </div>
              <Button type='button' variant='outline' disabled={disabled} onClick={() => action.mutate('check')}>
                <RefreshCw className={action.isPending && action.variables === 'check' ? 'animate-spin' : ''} />
                {t(action.isPending && action.variables === 'check' ? 'system.codex.public.checking' : 'system.codex.public.check')}
              </Button>
            </div>
            {data.blocked && <p className='text-muted-foreground text-sm'>{t('system.codex.public.blocked')}</p>}
            {saveError && <p role='alert' className='text-destructive text-sm'>{t('system.codex.public.saveFailed')} {saveError}</p>}
            {checkError && (
              <p role='alert' className='text-destructive text-sm'>
                {t('system.codex.public.checkFailed')} {checkError}
              </p>
            )}
            {data.entries.length > 0 && (
              <div className='divide-y rounded-lg border px-4'>
                {(expanded ? data.entries : data.entries.slice(0, 2)).map((entry) => (
                  <div key={entry.slug} className='space-y-1 py-3 text-sm'>
                    <div className='flex flex-wrap items-baseline gap-x-4'>
                      <strong>{entry.slug}</strong>
                      <span>{t('system.codex.public.windows', { context: format(entry.context), maximum: format(entry.maximum) })}</span>
                    </div>
                    <p className='text-muted-foreground'>
                      {t('system.codex.public.efforts', { levels: entry.efforts.join(' / ') || '—', effort: entry.defaultEffort || '—' })}
                    </p>
                  </div>
                ))}
                {data.entries.length > 2 && (
                  <Button type='button' variant='link' className='px-0' aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>
                    {expanded ? t('system.codex.public.collapse') : t('system.codex.public.expand', { count: data.entries.length })}
                  </Button>
                )}
              </div>
            )}
            <p className='text-muted-foreground text-sm'>{t('system.codex.public.priority')}</p>
          </>
        )}
      </CardContent>
    </Card>
  );
}
