import { useState } from 'react';
import { Search, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';

export function RequestSessionFilter({ value, onChange }: { value: string; onChange: (id: string) => void }) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState(value);
  return (
    <form
      className='flex min-w-0 items-center gap-1'
      role='search'
      onSubmit={(event) => {
        event.preventDefault();
        onChange(draft.trim());
      }}
    >
      <Input
        className='h-8 w-40 sm:w-48'
        aria-label={t('requests.session.id')}
        placeholder={t('requests.session.placeholder')}
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
      />
      <Button
        type='submit'
        variant='outline'
        size='icon'
        className='size-8'
        aria-label={t('requests.session.filter')}
        title={t('requests.session.filter')}
      >
        <Search className='size-4' />
      </Button>
      {value && (
        <Button
          type='button'
          variant='ghost'
          size='icon'
          className='size-8'
          aria-label={t('requests.session.clear')}
          onClick={() => {
            setDraft('');
            onChange('');
          }}
        >
          <X className='size-4' />
        </Button>
      )}
    </form>
  );
}
