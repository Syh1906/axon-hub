import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';

type ListMode = 'infinite' | 'pages';
type RefreshMode = 'steady' | 'sequential';

export function useRequestListPreference<T extends string>(key: string, initial: T, alternative: T) {
  const [value, setValue] = useState<T>(() => {
    try {
      return localStorage.getItem(key) === alternative ? alternative : initial;
    } catch {
      return initial;
    }
  });
  function select(next: T) {
    setValue(next);
    try {
      localStorage.setItem(key, next);
    } catch {
      // Storage is optional for display preferences.
    }
  }
  return [value, select] as const;
}

export function RequestListModeControls({
  listMode,
  onListModeChange,
  refreshMode,
  onRefreshModeChange,
  paused,
}: {
  listMode: ListMode;
  onListModeChange: (mode: ListMode) => void;
  refreshMode: RefreshMode;
  onRefreshModeChange: (mode: RefreshMode) => void;
  paused: boolean;
}) {
  const { t } = useTranslation();
  return (
    <>
      <Select value={listMode} onValueChange={onListModeChange}>
        <SelectTrigger className='h-8 w-28' aria-label={t('requests.pagination.mode')}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <SelectItem value='infinite'>{t('requests.pagination.infinite')}</SelectItem>
            <SelectItem value='pages'>{t('requests.pagination.pages')}</SelectItem>
          </SelectGroup>
        </SelectContent>
      </Select>
      {paused && <span className='text-muted-foreground text-xs'>{t('requests.refresh.paused')}</span>}
      <Select value={refreshMode} onValueChange={onRefreshModeChange}>
        <SelectTrigger className='h-8 w-28' aria-label={t('requests.refresh.mode')}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <SelectItem value='steady'>{t('requests.refresh.steady')}</SelectItem>
            <SelectItem value='sequential'>{t('requests.refresh.sequential')}</SelectItem>
          </SelectGroup>
        </SelectContent>
      </Select>
    </>
  );
}
