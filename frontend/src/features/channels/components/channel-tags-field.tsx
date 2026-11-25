import type { Ref } from 'react';
import { FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form';
import { TagsAutocompleteInput } from '@/components/ui/tags-autocomplete-input';
import type { useChannelActionDialog } from '../hooks/use-channel-action-dialog';

export function ChannelTagsField({ dialog, ref }: { dialog: ReturnType<typeof useChannelActionDialog>; ref: Ref<HTMLDivElement> }) {
  const { form, t, allTags, isLoadingTags } = dialog;
  const label = t('channels.dialogs.fields.tags.label');

  return (
    <FormField
      control={form.control}
      name='tags'
      render={({ field }) => (
        <FormItem className='grid grid-cols-1 items-start gap-x-6 gap-y-2 md:grid-cols-8'>
          <FormLabel className='pt-2 font-medium md:col-span-2 md:text-right'>{label}</FormLabel>
          <div className='space-y-1 md:col-span-6'>
            <TagsAutocompleteInput
              ref={ref}
              reorderable
              inputLabel={label}
              value={field.value ?? []}
              onChange={field.onChange}
              placeholder={t('channels.dialogs.fields.tags.placeholder')}
              suggestions={allTags}
              isLoading={isLoadingTags}
            />
            <p className='text-muted-foreground text-xs'>{t('channels.dialogs.fields.tags.description')}</p>
            <FormMessage />
          </div>
        </FormItem>
      )}
    />
  );
}
