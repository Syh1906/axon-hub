import { useEffect } from 'react';
import { z } from 'zod';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Form, FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { useUpdateChannelSettings } from '../data/channels';
import { type Channel } from '../data/schema';

const timeoutField = z.union([z.number().int().nonnegative(), z.literal('')]);
const requestSettingsSchema = z.object({
  streamFirstEventTimeoutSeconds: timeoutField,
  nonStreamResponseTimeoutSeconds: timeoutField,
  disableSessionReuse: z.boolean(),
});
type RequestSettings = z.infer<typeof requestSettingsSchema>;

function valuesFromChannel(channel: Channel): RequestSettings {
  return {
    streamFirstEventTimeoutSeconds: channel.settings?.streamFirstEventTimeoutSeconds ?? '',
    nonStreamResponseTimeoutSeconds: channel.settings?.nonStreamResponseTimeoutSeconds ?? '',
    disableSessionReuse: channel.settings?.disableSessionReuse ?? false,
  };
}

export function ChannelsRequestSettingsDialog({
  open,
  onOpenChange,
  currentRow,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  currentRow: Channel;
}) {
  const { t } = useTranslation();
  const updateSettings = useUpdateChannelSettings();
  const form = useForm<RequestSettings>({ resolver: zodResolver(requestSettingsSchema), defaultValues: valuesFromChannel(currentRow) });
  useEffect(() => {
    if (open) form.reset(valuesFromChannel(currentRow));
  }, [open, currentRow, form]);
  const onSubmit = async (values: RequestSettings) => {
    try {
      await updateSettings.mutateAsync({
        id: currentRow.id,
        patch: {
          streamFirstEventTimeoutSeconds: values.streamFirstEventTimeoutSeconds === '' ? null : values.streamFirstEventTimeoutSeconds,
          nonStreamResponseTimeoutSeconds: values.nonStreamResponseTimeoutSeconds === '' ? null : values.nonStreamResponseTimeoutSeconds,
          disableSessionReuse: values.disableSessionReuse,
        },
      });
      toast.success(t('channels.messages.updateSuccess'));
      onOpenChange(false);
    } catch {
      // The mutation displays the server error and keeps this form available.
    }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='max-h-[90vh] overflow-y-auto sm:max-w-xl'>
        <DialogHeader className='text-left'>
          <DialogTitle>{t('channels.requestSettings.title')}</DialogTitle>
          <DialogDescription>{t('channels.requestSettings.description', { name: currentRow.name })}</DialogDescription>
        </DialogHeader>
        <Form {...form}>
          <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-6'>
            <div className='space-y-4 rounded-md border p-4'>
              {(['streamFirstEventTimeoutSeconds', 'nonStreamResponseTimeoutSeconds'] as const).map((name) => (
                <FormField
                  key={name}
                  control={form.control}
                  name={name}
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t(`channels.requestSettings.${name}`)}</FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min='0'
                          step='1'
                          placeholder={t('channels.requestSettings.inherit')}
                          value={field.value}
                          onChange={(event) => field.onChange(event.target.value === '' ? '' : Number(event.target.value))}
                        />
                      </FormControl>
                      <FormDescription>{t(`channels.requestSettings.${name}Help`)}</FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              ))}
              <p className='text-muted-foreground text-xs'>{t('channels.requestSettings.timeoutHelp')}</p>
            </div>
            <FormField
              control={form.control}
              name='disableSessionReuse'
              render={({ field }) => (
                <FormItem className='rounded-md border p-4'>
                  <div className='flex items-center justify-between gap-4'>
                    <FormLabel>{t('channels.requestSettings.disableSessionReuse')}</FormLabel>
                    <FormControl>
                      <Switch checked={field.value} onCheckedChange={field.onChange} />
                    </FormControl>
                  </div>
                  <FormDescription>{t('channels.requestSettings.sessionHelp')}</FormDescription>
                </FormItem>
              )}
            />
            <DialogFooter>
              <Button type='button' variant='outline' onClick={() => onOpenChange(false)}>
                {t('common.buttons.cancel')}
              </Button>
              <Button type='submit' disabled={updateSettings.isPending}>
                {t('common.buttons.save')}
              </Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  );
}
