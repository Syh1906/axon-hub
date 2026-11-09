import { useEffect, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { ThemeSwitch } from '@/components/theme-switch';
import type { SelfUsageMeta } from '../api';

const pageWidths = { standard: 'max-w-7xl', wide: 'max-w-[1600px]', full: 'max-w-none' };
type PageWidth = keyof typeof pageWidths;
const widthStorageKey = 'self-usage-page-width';

function readPageWidth(): PageWidth {
  try {
    const value = localStorage.getItem(widthStorageKey);
    if (value === 'standard' || value === 'wide' || value === 'full') return value;
  } catch {
    // Display preferences remain optional when browser storage is unavailable.
  }
  return 'wide';
}

export function UsagePageShell({ meta, onLogout, children }: { meta?: SelfUsageMeta; onLogout?: () => void; children: ReactNode }) {
  const { t, i18n } = useTranslation();
  const [pageWidth, setPageWidth] = useState(readPageWidth);
  const changeWidth = (value: PageWidth) => {
    setPageWidth(value);
    try {
      localStorage.setItem(widthStorageKey, value);
    } catch {
      // Keep this page usable without persistent browser storage.
    }
  };
  useEffect(() => {
    document.title = `${t('selfUsage.title')} · AxonHub`;
  }, [t, i18n.language]);
  return (
    <main className='bg-background text-foreground min-h-screen'>
      <div className={cn('mx-auto flex flex-col gap-6 px-4 py-8 sm:px-6', pageWidths[pageWidth])}>
        <header className='flex flex-wrap items-center justify-between gap-3 border-b pb-5'>
          <div>
            <h1 className='text-2xl font-semibold'>{t('selfUsage.title')}</h1>
            <p className='text-muted-foreground text-sm'>
              {meta
                ? `${meta.apiKeyName || t('kg.ungrouped')} · ${t('selfUsage.timezone', { timezone: meta.timezone })}`
                : t('selfUsage.subtitle')}
            </p>
          </div>
          <div className='flex flex-wrap items-center gap-2'>
            {meta && (
              <Select value={pageWidth} onValueChange={(value) => changeWidth(value as PageWidth)}>
                <SelectTrigger className='h-9 w-auto' aria-label={t('selfUsage.width.label')}>
                  <SelectValue>{t('selfUsage.width.current', { mode: t(`selfUsage.width.${pageWidth}`) })}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value='standard'>{t('selfUsage.width.standard')}</SelectItem>
                    <SelectItem value='wide'>{t('selfUsage.width.wideDefault')}</SelectItem>
                    <SelectItem value='full'>{t('selfUsage.width.full')}</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            )}
            <Button
              variant='outline'
              size='sm'
              aria-label={t('selfUsage.language')}
              onClick={() => void i18n.changeLanguage(i18n.language.startsWith('zh') ? 'en' : 'zh-CN')}
            >
              {i18n.language.startsWith('zh') ? 'English' : '中文'}
            </Button>
            <ThemeSwitch />
            {onLogout && (
              <Button variant='outline' size='sm' onClick={onLogout}>
                {t('selfUsage.logout')}
              </Button>
            )}
          </div>
        </header>
        {children}
      </div>
    </main>
  );
}
