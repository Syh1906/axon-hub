import { ArrowUp, Loader2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';

export function IncrementalListFooter({
  loaded,
  total,
  loading,
  hasMore,
  error,
  atTop,
  onLoadMore,
  onBackToTop,
}: {
  loaded: number;
  total?: number;
  loading: boolean;
  hasMore: boolean;
  error: boolean;
  atTop: boolean;
  onLoadMore: () => void;
  onBackToTop: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div className='text-muted-foreground flex min-h-10 shrink-0 items-center justify-between gap-3 pt-2 text-xs'>
      <span>{t('common.incremental.loaded', { count: loaded, total: total ?? loaded })}</span>
      <div className='flex items-center gap-2'>
        {loading ? (
          <span className='flex items-center gap-1'>
            <Loader2 className='size-3 animate-spin' />
            {t('common.loading')}
          </span>
        ) : error ? (
          <Button variant='ghost' size='sm' onClick={onLoadMore}>
            {t('common.incremental.retry')}
          </Button>
        ) : (
          <span>{t(hasMore ? 'common.incremental.scrollMore' : 'common.incremental.end')}</span>
        )}
        {!atTop && (
          <Button
            size='icon'
            variant='outline'
            className='fixed right-6 bottom-6 z-30 rounded-full shadow-md'
            onClick={onBackToTop}
            aria-label={t('common.incremental.backToTop')}
            title={t('common.incremental.backToTop')}
          >
            <ArrowUp className='size-4' />
          </Button>
        )}
      </div>
    </div>
  );
}
