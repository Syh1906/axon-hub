import { IconLanguage } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { useLanguage } from '@/hooks/useLanguage';
import { Button } from '@/components/ui/button';
import { DropdownMenu, DropdownMenuContent, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu';
import { normalizeLanguagePreference } from '@/lib/language-preference';

export function LanguageSwitch({ browserOnly = false }: { browserOnly?: boolean } = {}) {
  const { preference, changeLanguage, isUpdating } = useLanguage({ browserOnly });
  const { t } = useTranslation();

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant='ghost' size='sm' className='h-8 w-8 px-0' disabled={isUpdating}>
          <IconLanguage className='h-4 w-4' />
          <span className='sr-only'>{t('language.toggle')}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align='end'>
        <DropdownMenuRadioGroup value={preference} onValueChange={(value) => void changeLanguage(normalizeLanguagePreference(value))}>
          <DropdownMenuRadioItem value='auto' disabled={isUpdating}>{t('language.auto')}</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value='zh' disabled={isUpdating}>中文</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value='en' disabled={isUpdating}>English</DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
