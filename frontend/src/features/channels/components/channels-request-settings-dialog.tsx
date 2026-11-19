import { useEffect } from 'react';
import { z } from 'zod';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Form, FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group';
import { Switch } from '@/components/ui/switch';
import { useUpdateChannelSettings } from '../data/channels';
import { responsesServiceTierOverrideSchema, responsesTierSupportSchema, serviceTierBasisSchema, type Channel } from '../data/schema';

const timeoutField = z.union([z.number().int().nonnegative(), z.literal('')]);
const requestSettingsSchema = z.object({
  responsesServiceTierOverride: responsesServiceTierOverrideSchema,
  responsesTierSupport: responsesTierSupportSchema,
  billingBasis: serviceTierBasisSchema,
  streamFirstEventTimeoutSeconds: timeoutField,
  nonStreamResponseTimeoutSeconds: timeoutField,
  disableSessionReuse: z.boolean(),
  allowImageGenerationTool: z.boolean(),
});
type RequestSettings = z.infer<typeof requestSettingsSchema>;

function valuesFromChannel(channel: Channel): RequestSettings {
  const storedMode = channel.settings?.responsesServiceTierOverride ?? 'INHERIT';
  const legacy = storedMode === 'INHERIT';
  const billing = channel.settings?.serviceTierBilling;
  return {
    responsesServiceTierOverride: legacy ? 'SUPPORTED' : storedMode,
    responsesTierSupport: legacy ? { fast: true, ultrafast: true, allowDowngrade: true } : channel.settings?.responsesTierSupport ?? { fast: false, ultrafast: false, allowDowngrade: false },
    billingBasis: billing?.enabled ? billing.basis : 'fixed_standard',
    streamFirstEventTimeoutSeconds: channel.settings?.streamFirstEventTimeoutSeconds ?? '',
    nonStreamResponseTimeoutSeconds: channel.settings?.nonStreamResponseTimeoutSeconds ?? '',
    disableSessionReuse: channel.settings?.disableSessionReuse ?? false,
    allowImageGenerationTool: channel.settings?.allowImageGenerationTool ?? true,
  };
}

function TierChoices({ label, value, onChange, options }: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: { value: string; label: string }[];
}) {
  return (
    <fieldset className='space-y-2'>
      <legend className='text-sm font-medium'>{label}</legend>
      <RadioGroup aria-label={label} className='flex flex-wrap gap-x-5 gap-y-2' value={value} onValueChange={onChange}>
        {options.map((option) => (
          <label key={option.value} className='flex items-center gap-2 text-sm'>
            <RadioGroupItem value={option.value} />{option.label}
          </label>
        ))}
      </RadioGroup>
    </fieldset>
  );
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
  const [mode, support, basis] = form.watch(['responsesServiceTierOverride', 'responsesTierSupport', 'billingBasis']);
  const legacy = !currentRow.settings?.responsesServiceTierOverride || currentRow.settings.responsesServiceTierOverride === 'INHERIT';
  const fixedBilling = basis.startsWith('fixed_');
  const tiers = ['standard', 'fast', 'ultrafast'] as const;
  const tierOptions = tiers.map((tier) => ({ value: tier, label: t(`serviceTier.mode.${tier}`) }));
  const onSubmit = async (values: RequestSettings) => {
    try {
      await updateSettings.mutateAsync({
        id: currentRow.id,
        patch: {
          responsesServiceTierOverride: values.responsesServiceTierOverride,
          serviceTierBilling: { enabled: true, basis: values.billingBasis },
          ...(values.responsesServiceTierOverride === 'SUPPORTED' && { responsesTierSupport: values.responsesTierSupport }),
          streamFirstEventTimeoutSeconds: values.streamFirstEventTimeoutSeconds === '' ? null : values.streamFirstEventTimeoutSeconds,
          nonStreamResponseTimeoutSeconds: values.nonStreamResponseTimeoutSeconds === '' ? null : values.nonStreamResponseTimeoutSeconds,
          disableSessionReuse: values.disableSessionReuse,
          allowImageGenerationTool: values.allowImageGenerationTool,
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
      <DialogContent className='flex max-h-[90vh] flex-col overflow-hidden sm:max-w-xl'>
        <DialogHeader className='text-left'>
          <DialogTitle>{t('channels.requestSettings.title')}</DialogTitle>
          <DialogDescription>{t('channels.requestSettings.description', { name: currentRow.name })}</DialogDescription>
        </DialogHeader>
        <Form {...form}>
          <form onSubmit={form.handleSubmit(onSubmit)} className='flex min-h-0 flex-col gap-6'>
            <div className='min-h-0 space-y-6 overflow-y-auto'>
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
              <section className='space-y-4 rounded-md border p-4'>
                <TierChoices
                  label={t('channels.requestSettings.requestTier')}
                  value={mode === 'SUPPORTED' ? 'supported' : 'forced'}
                  options={[
                    { value: 'supported', label: t('channels.requestSettings.responsesServiceTier.SUPPORTED') },
                    { value: 'forced', label: t('channels.requestSettings.forceTier') },
                  ]}
                  onChange={(choice) => form.setValue('responsesServiceTierOverride', choice === 'supported' ? 'SUPPORTED' : 'STANDARD')}
                />
                {legacy && <p className='text-muted-foreground text-xs'>{t('channels.requestSettings.legacyTier')}</p>}
                {mode === 'SUPPORTED' ? (
                  <>
                    <fieldset className='space-y-2'>
                      <legend className='text-sm font-medium'>{t('channels.requestSettings.supportedTiers')}</legend>
                      <div className='flex flex-wrap gap-6'>
                        {(['fast', 'ultrafast'] as const).map((tier) => (
                          <FormField key={tier} control={form.control} name={`responsesTierSupport.${tier}`} render={({ field }) => (
                            <FormItem className='flex items-center gap-2'>
                              <FormControl><Checkbox checked={field.value} onCheckedChange={(value) => field.onChange(value === true)} /></FormControl>
                              <FormLabel>{t(`serviceTier.mode.${tier}`)}</FormLabel>
                            </FormItem>
                          )} />
                        ))}
                      </div>
                      {!support.fast && !support.ultrafast && <p className='text-muted-foreground text-xs'>{t('channels.requestSettings.standardOnly')}</p>}
                    </fieldset>
                    <FormField control={form.control} name='responsesTierSupport.allowDowngrade' render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('channels.requestSettings.unsupportedTier')}</FormLabel>
                        <Select value={field.value ? 'downgrade' : 'skip'} onValueChange={(value) => field.onChange(value === 'downgrade')}>
                          <FormControl><SelectTrigger className='w-full'><SelectValue /></SelectTrigger></FormControl>
                          <SelectContent>
                            <SelectItem value='skip'>{t('channels.requestSettings.skipChannel')}</SelectItem>
                            <SelectItem value='downgrade'>{t('channels.requestSettings.allowDowngrade')}</SelectItem>
                          </SelectContent>
                        </Select>
                        {field.value && <FormDescription>{t('channels.requestSettings.downgradeHelp')}</FormDescription>}
                      </FormItem>
                    )} />
                  </>
                ) : (
                  <TierChoices label={t('channels.requestSettings.sendTier')} value={mode.toLowerCase()} options={tierOptions}
                    onChange={(tier) => form.setValue('responsesServiceTierOverride', responsesServiceTierOverrideSchema.parse(tier.toUpperCase()))} />
                )}
              </section>
              <section className='space-y-3 rounded-md border p-4'>
                <TierChoices label={t('channels.requestSettings.billingPolicy')} value={fixedBilling ? 'fixed' : basis}
                  options={['upstream', 'client', 'fixed'].map((value) => ({ value, label: t(`serviceTier.settings.${value}`) }))}
                  onChange={(value) => form.setValue('billingBasis', serviceTierBasisSchema.parse(value === 'fixed' ? 'fixed_standard' : value))} />
                {fixedBilling && (
                  <TierChoices label={t('channels.requestSettings.fixedBillingTier')} value={basis.slice(6)} options={tierOptions}
                    onChange={(tier) => form.setValue('billingBasis', serviceTierBasisSchema.parse(`fixed_${tier}`))} />
                )}
                <p className='text-muted-foreground text-sm'>
                  {t(fixedBilling ? 'channels.requestSettings.fixedBillingHelp' : `channels.requestSettings.${basis}BillingHelp`)}
                </p>
              </section>
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
              <FormField
                control={form.control}
                name='allowImageGenerationTool'
                render={({ field }) => (
                  <FormItem className='rounded-md border p-4'>
                    <div className='flex items-center justify-between gap-4'>
                      <div className='flex flex-wrap items-center gap-2'>
                        <FormLabel>{t('channels.requestSettings.allowImageGenerationTool')}</FormLabel>
                        <Badge variant='secondary'>{t('channels.requestSettings.defaultEnabled')}</Badge>
                      </div>
                      <FormControl>
                        <Switch checked={field.value} onCheckedChange={field.onChange} />
                      </FormControl>
                    </div>
                    <FormDescription className='whitespace-pre-line'>
                      {t('channels.requestSettings.imageGenerationToolHelp')}
                    </FormDescription>
                    <p className='text-muted-foreground text-xs'>{t('channels.requestSettings.imageGenerationToolScope')}</p>
                  </FormItem>
                )}
              />
            </div>
            <DialogFooter className='shrink-0'>
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
