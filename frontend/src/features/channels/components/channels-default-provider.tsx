import { useState } from 'react';
import { Globe } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group';
import { CHANNEL_TYPE_TO_PROVIDER } from '../data/config_channels';
import { PROVIDER_CONFIGS } from '../data/config_providers';
import { readDefaultProvider, resolveChannelProviderSelection, saveDefaultProvider } from '../utils/default-provider';
import { IMAGE_PRIMARY_API_FORMAT, type ChannelTypeGroup } from '../utils/group-channel-types';

export function ChannelsDefaultProvider({ groups, onApply }: { groups: ChannelTypeGroup[]; onApply: (provider: string) => void }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState('all');
  const [search, setSearch] = useState('');
  const selection = resolveChannelProviderSelection(draft, CHANNEL_TYPE_TO_PROVIDER);
  // Keep a saved selection available even when no channels currently use it.
  const choices =
    selection && !groups.some((group) => group.key === draft)
      ? [...groups, { key: draft, ...selection, totalCount: 0, primaryApiFormat: selection.isImage ? IMAGE_PRIMARY_API_FORMAT : undefined }]
      : groups;
  const options = [
    { key: 'all', label: t('channels.tabs.all'), icon: Globe },
    ...choices.map((group) => {
      const label = t(`channels.providers.${group.provider}`, { defaultValue: group.provider });
      return {
        key: group.key,
        label: group.primaryApiFormat ? `${label} · ${t('channels.tabs.imageSuffix')}` : label,
        icon: PROVIDER_CONFIGS[group.provider]?.icon,
      };
    }),
  ].filter((option) => option.label.toLowerCase().includes(search.trim().toLowerCase()));

  return (
    <>
      <Button
        variant='outline'
        size='sm'
        onClick={() => {
          setDraft(readDefaultProvider(CHANNEL_TYPE_TO_PROVIDER));
          setSearch('');
          setOpen(true);
        }}
      >
        {t('channels.defaultProvider.title')}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className='sm:max-w-md'>
          <DialogHeader>
            <DialogTitle>{t('channels.defaultProvider.title')}</DialogTitle>
            <DialogDescription>{t('channels.defaultProvider.help')}</DialogDescription>
          </DialogHeader>
          <Input
            aria-label={t('channels.defaultProvider.search')}
            placeholder={t('channels.defaultProvider.search')}
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
          <RadioGroup
            value={draft}
            onValueChange={setDraft}
            aria-label={t('channels.defaultProvider.title')}
            className='max-h-60 overflow-y-auto'
          >
            {options.map(({ key, label, icon: Icon }) => (
              <Label key={key} htmlFor={`default-provider-${key}`} className='flex min-h-9 cursor-pointer items-center gap-3 py-2'>
                <RadioGroupItem id={`default-provider-${key}`} value={key} />
                {Icon && <Icon size={18} />}
                {label}
              </Label>
            ))}
            {options.length === 0 && <p className='text-muted-foreground py-2 text-sm'>{t('channels.defaultProvider.empty')}</p>}
          </RadioGroup>
          <DialogFooter>
            <Button
              variant='ghost'
              onClick={() => {
                setDraft('all');
                setSearch('');
              }}
            >
              {t('channels.defaultTags.clear')}
            </Button>
            <Button variant='outline' onClick={() => setOpen(false)}>
              {t('common.buttons.cancel')}
            </Button>
            <Button
              onClick={() => {
                try {
                  saveDefaultProvider(draft);
                } catch {
                  toast.error(t('channels.defaultProvider.saveFailed'));
                  return;
                }
                onApply(draft);
                setOpen(false);
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
