import { useEffect, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { ThemeSwitch } from '@/components/theme-switch';
import type { SelfUsageMeta } from '../api';

export function UsagePageShell({ meta, onLogout, children }: { meta?: SelfUsageMeta; onLogout?: () => void; children: ReactNode }) {
  const { t, i18n } = useTranslation();
  useEffect(() => {
    document.title = `${t('selfUsage.title')} · AxonHub`;
  }, [t, i18n.language]);
  return (
    <main className='bg-background text-foreground min-h-screen'>
      <div className='mx-auto max-w-7xl space-y-6 px-4 py-8 sm:px-6'>
        <header className='flex flex-wrap items-center justify-between gap-3 border-b pb-5'>
          <div>
            <h1 className='text-2xl font-semibold'>{t('selfUsage.title')}</h1>
            <p className='text-muted-foreground text-sm'>
              {meta
                ? `${meta.apiKeyName || t('kg.ungrouped')} · ${t('selfUsage.timezone', { timezone: meta.timezone })}`
                : t('selfUsage.subtitle')}
            </p>
          </div>
          <div className='flex items-center gap-2'>
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
