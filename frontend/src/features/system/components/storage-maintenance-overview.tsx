import { RefreshCw } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { useStorageOverview } from '../data/maintenance';

export function StorageMaintenanceOverview() {
  const { t } = useTranslation();
  const query = useStorageOverview();
  const data = query.data;
  const bytes = (value: number | null | undefined) => {
    if (value == null) return t('system.maintenance.unknown');
    const unit = value >= 1024 ** 3 ? 3 : value >= 1024 ** 2 ? 2 : value >= 1024 ? 1 : 0;
    return `${(value / 1024 ** unit).toLocaleString(undefined, { maximumFractionDigits: 2 })} ${['B', 'KiB', 'MiB', 'GiB'][unit]}`;
  };
  return (
    <Card>
      <CardHeader className='flex flex-row flex-wrap items-start justify-between gap-3'>
        <div className='flex flex-col gap-2'>
          <CardTitle>{t('system.maintenance.overview')}</CardTitle>
          <CardDescription>{t('system.maintenance.capacityHint')}</CardDescription>
        </div>
        <Button variant='outline' size='sm' disabled={query.isFetching} onClick={() => query.refetch()}>
          <RefreshCw className={query.isFetching ? 'animate-spin' : ''} />
          {t('system.maintenance.refresh')}
        </Button>
      </CardHeader>
      <CardContent className='flex flex-col gap-4'>
        {query.isPending ? (
          <Skeleton className='h-20 w-full' />
        ) : (
          <div className='grid gap-4 sm:grid-cols-3'>
            <div>
              <p className='text-muted-foreground text-sm'>
                {t('system.maintenance.database')} {data?.dialect}
              </p>
              <p className='text-xl font-semibold tabular-nums'>{bytes(data?.databaseBytes)}</p>
            </div>
            <div>
              <p className='text-muted-foreground text-sm'>{t('system.maintenance.freeDisk')}</p>
              <p className='text-xl font-semibold tabular-nums'>
                {bytes(data?.diskFreeBytes)} / {bytes(data?.diskTotalBytes)}
              </p>
            </div>
            <div>
              <p className='text-muted-foreground text-sm'>{t('system.maintenance.reserve')}</p>
              <p className='text-xl font-semibold tabular-nums'>{bytes(data?.reserveBytes)}</p>
              <Badge variant={data?.level === 'critical' ? 'destructive' : 'secondary'}>
                {t(`system.maintenance.level.${data?.level ?? 'unknown'}`)}
              </Badge>
            </div>
          </div>
        )}
        <p className='text-muted-foreground text-xs'>
          {t(data?.diskSource === 'configured' ? 'system.maintenance.diskConfigured' : 'system.maintenance.diskApplication')}
        </p>
        {!!data?.tables.length && (
          <details className='rounded-md border p-3 text-sm'>
            <summary className='cursor-pointer'>{t('system.maintenance.tableSizes')}</summary>
            <div className='mt-3 grid gap-2 sm:grid-cols-2'>
              {data.tables.map((table) => (
                <div key={table.name} className='flex justify-between gap-3'>
                  <span className='truncate'>{table.name}</span>
                  <span className='shrink-0 tabular-nums'>{bytes(table.bytes)}</span>
                </div>
              ))}
            </div>
          </details>
        )}
        {query.isError && (
          <Alert variant='destructive'>
            <AlertDescription>{t('system.maintenance.capacityError')}</AlertDescription>
          </Alert>
        )}
      </CardContent>
    </Card>
  );
}
