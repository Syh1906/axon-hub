import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { useAllChannelTags } from '../data/channels';
import { readDefaultTags, saveDefaultTags } from '../utils/default-tags';

export function ChannelsDefaultTags({ onApply }: { onApply: (tags: string[]) => void }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<string[]>([]);
  const [search, setSearch] = useState('');
  const tags = useAllChannelTags();
  return (
    <>
      <Button
        variant='outline'
        size='sm'
        onClick={() => {
          setDraft(readDefaultTags());
          setSearch('');
          setOpen(true);
        }}
      >
        {t('channels.defaultTags.title')}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className='sm:max-w-md'>
          <DialogHeader>
            <DialogTitle>{t('channels.defaultTags.title')}</DialogTitle>
            <DialogDescription>{t('channels.defaultTags.help')}</DialogDescription>
          </DialogHeader>
          <div className='flex flex-wrap gap-1'>
            {draft.map((tag) => (
              <Button key={tag} size='sm' variant='secondary' onClick={() => setDraft(draft.filter((x) => x !== tag))}>
                {tag} ×
              </Button>
            ))}
          </div>
          <Input
            aria-label={t('channels.defaultTags.search')}
            placeholder={t('channels.defaultTags.search')}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <div className='flex max-h-60 flex-col gap-2 overflow-auto'>
            {tags.error ? (
              <p role='alert'>{t('common.loadError')}</p>
            ) : (
              (tags.data ?? [])
                .filter((tag) => tag.toLowerCase().includes(search.toLowerCase()))
                .map((tag) => (
                  <label key={tag} className='flex min-h-9 items-center gap-2'>
                    <Checkbox
                      checked={draft.includes(tag)}
                      onCheckedChange={(checked) => setDraft(checked ? [...draft, tag] : draft.filter((x) => x !== tag))}
                    />
                    {tag}
                  </label>
                ))
            )}
          </div>
          <DialogFooter>
            <Button variant='ghost' onClick={() => setDraft([])}>
              {t('channels.defaultTags.clear')}
            </Button>
            <Button variant='outline' onClick={() => setOpen(false)}>
              {t('common.buttons.cancel')}
            </Button>
            <Button
              onClick={() => {
                try {
                  saveDefaultTags(draft);
                  onApply(draft);
                  setOpen(false);
                } catch {
                  toast.error(t('channels.sort.saveFailed'));
                }
              }}
            >
              {t('channels.sort.saveAndApply')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
