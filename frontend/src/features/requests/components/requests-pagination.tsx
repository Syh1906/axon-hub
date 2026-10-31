import { useEffect, useState } from 'react';
import { ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';

export interface RequestsPaginationProps {
  page: number;
  pageSize: number;
  totalCount?: number;
  loading: boolean;
  onPageChange: (page: number) => void;
  onPageSizeChange: (pageSize: number) => void;
}

export function RequestsPagination({ page, pageSize, totalCount, loading, onPageChange, onPageSizeChange }: RequestsPaginationProps) {
  const { t } = useTranslation();
  const [target, setTarget] = useState(String(page));
  useEffect(() => setTarget(String(page)), [page]);
  const pages = Math.max(1, Math.ceil((totalCount ?? 0) / pageSize));
  const go = (value: number) => {
    if (Number.isSafeInteger(value) && value >= 1 && value <= pages) onPageChange(value);
  };
  return (
    <div className='flex shrink-0 flex-wrap items-center justify-between gap-3 pt-3 text-sm' data-testid='requests-pagination'>
      <div className='flex items-center gap-2'>
        <span className='text-muted-foreground'>{t('pagination.rowsPerPage')}</span>
        <Select value={String(pageSize)} onValueChange={(value) => onPageSizeChange(Number(value))}>
          <SelectTrigger className='h-8 w-20' aria-label={t('pagination.rowsPerPage')}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              {[10, 20, 30, 40, 50, 100].map((size) => (
                <SelectItem key={size} value={String(size)}>
                  {size}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
      </div>
      <div className='flex flex-wrap items-center gap-2'>
        <span>{totalCount === undefined ? t('common.loading') : t('pagination.currentPage', { current: page, total: pages })}</span>
        <Button
          size='icon-sm'
          variant='outline'
          disabled={loading || page <= 1}
          onClick={() => go(1)}
          aria-label={t('pagination.firstPage')}
        >
          <ChevronsLeft />
        </Button>
        <Button
          size='icon-sm'
          variant='outline'
          disabled={loading || page <= 1}
          onClick={() => go(page - 1)}
          aria-label={t('pagination.previousPage')}
        >
          <ChevronLeft />
        </Button>
        <Button
          size='icon-sm'
          variant='outline'
          disabled={loading || page >= pages}
          onClick={() => go(page + 1)}
          aria-label={t('pagination.nextPage')}
        >
          <ChevronRight />
        </Button>
        <Button
          size='icon-sm'
          variant='outline'
          disabled={loading || page >= pages}
          onClick={() => go(pages)}
          aria-label={t('pagination.lastPage')}
        >
          <ChevronsRight />
        </Button>
        <form
          className='flex items-center gap-2'
          onSubmit={(event) => {
            event.preventDefault();
            go(Number(target));
          }}
        >
          <Input
            type='number'
            min={1}
            max={pages}
            step={1}
            value={target}
            onChange={(event) => setTarget(event.target.value)}
            className='h-8 w-20'
            aria-label={t('requests.pagination.target')}
          />
          <Button type='submit' size='sm' variant='outline' disabled={loading}>
            {t('requests.pagination.go')}
          </Button>
        </form>
      </div>
    </div>
  );
}
