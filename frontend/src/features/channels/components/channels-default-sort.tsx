import { useState } from 'react';
import type { SortingState } from '@tanstack/react-table';
import { ArrowDown, ArrowUp, RotateCcw } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Label } from '@/components/ui/label';
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group';
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { CHANNEL_SORT_FIELDS, DEFAULT_CHANNEL_SORT, readDefaultChannelSort, saveDefaultChannelSort } from '../utils/channel-sort';
import { RefreshAllUpstreamsButton } from './channel-upstream-cell';

export function ChannelsDefaultSort({ sorting, onApply }: { sorting: SortingState; onApply: (value: SortingState) => void }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(readDefaultChannelSort);
  const [container, setContainer] = useState<HTMLDivElement | null>(null);
  const current = sorting[0] ?? DEFAULT_CHANNEL_SORT[0];
  const fieldLabel = (id: string) => t(`channels.sort.fields.${id}`);
  const save = () => {
    try {
      saveDefaultChannelSort(draft);
    } catch {
      toast.error(t('channels.sort.saveFailed'));
      return;
    }
    onApply(draft);
    setOpen(false);
    toast.success(t('channels.sort.saved'));
  };
  return (
    <>
      <div className='bg-muted/40 mt-3 flex flex-wrap items-center justify-between gap-2 rounded-lg px-3 py-2 text-sm'>
        <div className='flex items-center gap-2'>
          <span className='text-muted-foreground'>{t('channels.sort.current')}</span>
          <span>{fieldLabel(current.id)}</span>
          {current.desc ? <ArrowDown className='size-4' /> : <ArrowUp className='size-4' />}
          <span className='text-muted-foreground text-xs'>{t(current.desc ? 'channels.sort.descending' : 'channels.sort.ascending')}</span>
        </div>
        <div className='flex items-center gap-2'>
          <RefreshAllUpstreamsButton />
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => {
              setDraft(readDefaultChannelSort());
              setOpen(true);
            }}
          >
            <RotateCcw />
            {t('channels.sort.button')}
          </Button>
        </div>
      </div>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent ref={setContainer} className='sm:max-w-md'>
          <DialogHeader>
            <DialogTitle>{t('channels.sort.title')}</DialogTitle>
            <DialogDescription>{t('channels.sort.description')}</DialogDescription>
          </DialogHeader>
          <div className='flex flex-col gap-4'>
            <div className='flex flex-col gap-2'>
              <Label htmlFor='channel-default-sort-field'>{t('channels.sort.field')}</Label>
              <Select value={draft[0].id} onValueChange={(id) => setDraft([{ ...draft[0], id }])}>
                <SelectTrigger className='w-full' id='channel-default-sort-field'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent container={container}>
                  <SelectGroup>
                    {CHANNEL_SORT_FIELDS.map((id) => (
                      <SelectItem key={id} value={id}>
                        {fieldLabel(id)}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
            <div className='flex flex-col gap-3'>
              <Label>{t('channels.sort.direction')}</Label>
              <RadioGroup
                value={draft[0].desc ? 'desc' : 'asc'}
                onValueChange={(direction) => setDraft([{ ...draft[0], desc: direction === 'desc' }])}
              >
                <div className='flex items-center gap-2'>
                  <RadioGroupItem value='asc' id='channel-sort-asc' />
                  <Label htmlFor='channel-sort-asc'>{t('channels.sort.ascending')}</Label>
                </div>
                <div className='flex items-center gap-2'>
                  <RadioGroupItem value='desc' id='channel-sort-desc' />
                  <Label htmlFor='channel-sort-desc'>{t('channels.sort.descending')}</Label>
                </div>
              </RadioGroup>
            </div>
            <p className='bg-muted rounded-md px-3 py-2 text-sm'>
              {t('channels.sort.summary', {
                field: fieldLabel(draft[0].id),
                direction: t(draft[0].desc ? 'channels.sort.desc' : 'channels.sort.asc'),
              })}
            </p>
            <div className='text-muted-foreground flex flex-col gap-1 text-xs'>
              <p>{t('channels.sort.priorityHint')}</p>
              <p>{t('channels.sort.displayOnly')}</p>
            </div>
          </div>
          <DialogFooter className='gap-2 sm:justify-between'>
            <Button type='button' variant='ghost' onClick={() => setDraft(DEFAULT_CHANNEL_SORT)}>
              {t('channels.sort.reset')}
            </Button>
            <div className='flex justify-end gap-2'>
              <Button type='button' variant='outline' onClick={() => setOpen(false)}>
                {t('common.buttons.cancel')}
              </Button>
              <Button type='button' onClick={save}>
                {t('channels.sort.saveAndApply')}
              </Button>
            </div>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
