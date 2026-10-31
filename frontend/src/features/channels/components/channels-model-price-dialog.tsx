import { Switch } from '@/components/ui/switch';
import { appliedModelPrice } from '@/features/models/data/service-tier';
import { serviceTierSettingsSchema, type ServiceTierSettings } from '../data/schema';
import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { z } from 'zod';
import { useFieldArray, useForm, useWatch, type Control } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { IconCopy, IconDownload, IconPlus, IconTrash, IconUpload } from '@tabler/icons-react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { useDebounce } from '@/hooks/use-debounce';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { AutoCompleteSelect } from '@/components/auto-complete-select';
import { CompleteModelPriceEditor } from '@/features/models/components/models-price-editor';
import { fetchModelStandardPrices, useModelStandardPrices, type ModelStandardPrice } from '@/features/models/data/models';
import { createPriceValidationSchema, isValidPriceMultiplier } from '@/features/models/data/pricing';
import { useChannels } from '../context/channels-context';
import { useChannelModelPrices, useSaveChannelModelPrices } from '../data/channels';
import {
  modelPriceInputSchema,
  saveChannelModelPriceInputSchema,
  type Channel,
  type ModelPrice,
  type SaveChannelModelPriceInput,
} from '../data/schema';

const multiplierSchema = z.string().trim().refine(isValidPriceMultiplier);
const importSchema = z.union([
 z.object({ version: z.literal(3), multiplier: multiplierSchema, serviceTierBilling: serviceTierSettingsSchema, prices: z.array(saveChannelModelPriceInputSchema) }),
  z.array(saveChannelModelPriceInputSchema),
  z.object({ version: z.literal(2), multiplier: multiplierSchema, prices: z.array(saveChannelModelPriceInputSchema) }),
]);

function createFormSchema(t: (key: string) => string) {
  const priceSchema = createPriceValidationSchema(t);
  return z
    .object({
      serviceTierBilling: serviceTierSettingsSchema,
      multiplier: z.string().trim().refine(isValidPriceMultiplier, t('price.validation.multiplier')),
      prices: z.array(
        saveChannelModelPriceInputSchema.extend({
          modelId: z.string().min(1, t('price.validation.modelRequired')),
          price: modelPriceInputSchema.superRefine((price, ctx) => {
            const result = priceSchema.safeParse(price);
            if (!result.success) result.error.issues.forEach((issue) => ctx.addIssue(issue));
            else if (!result.data) ctx.addIssue({ code: 'custom', message: t('price.validation.itemsRequired'), path: ['items'] });
          }),
        })
      ),
    })
    .superRefine(({ prices }, ctx) => {
      const seen = new Set<string>();
      prices.forEach((price, index) => {
        if (seen.has(price.modelId))
          ctx.addIssue({ code: 'custom', message: t('price.validation.duplicateModel'), path: ['prices', index, 'modelId'] });
        seen.add(price.modelId);
      });
    });
}
type PriceFormData = { serviceTierBilling: ServiceTierSettings; multiplier: string; prices: SaveChannelModelPriceInput[] };

const PriceCard = memo(function PriceCard({
  control,
  index,
  availableModels,
  portalContainer,
  multiplier,
  busy,
  onModelSelected,
  onDuplicate,
  onRemove,
}: {
  control: Control<PriceFormData>;
  index: number;
  availableModels: string[];
  portalContainer: HTMLDivElement | null;
  multiplier: string;
  busy: boolean;
  onModelSelected: (index: number, modelId: string) => void;
  onDuplicate: (index: number) => void;
  onRemove: (index: number) => void;
}) {
  const { t } = useTranslation();
  return (
    <Card className='gap-0 overflow-hidden py-0'>
      <CardContent className='space-y-3 p-3'>
        <div className='flex items-start gap-2'>
          <FormField
            control={control}
            name={`prices.${index}.modelId`}
            render={({ field }) => (
              <FormItem className='min-w-0 flex-1'>
                <Select
                  value={field.value}
                  disabled={busy}
                  onValueChange={(value) => {
                    field.onChange(value);
                    onModelSelected(index, value);
                  }}
                >
                  <FormControl>
                    <SelectTrigger size='sm' className='h-8 w-full' title={field.value}>
                      <SelectValue placeholder={t('price.model')} />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent>
                    {availableModels.map((model) => (
                      <SelectItem key={model} value={model}>
                        {model}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <FormMessage />
              </FormItem>
            )}
          />
          <Button
            type='button'
            variant='ghost'
            size='icon-sm'
            disabled={busy}
            onClick={() => onDuplicate(index)}
            aria-label={t('common.actions.duplicate')}
          >
            <IconCopy size={14} />
          </Button>
          <Button
            type='button'
            variant='ghost'
            size='icon-sm'
            disabled={busy}
            className='text-destructive'
            onClick={() => onRemove(index)}
            aria-label={t('price.removePrice')}
          >
            <IconTrash size={14} />
          </Button>
        </div>
        <fieldset disabled={busy} className='min-w-0'>
          <CompleteModelPriceEditor pricePath={`prices.${index}.price`} portalContainer={portalContainer} multiplier={multiplier} />
        </fieldset>
      </CardContent>
    </Card>
  );
});

export function ChannelsModelPriceDialog() {
  const { open, setOpen, currentRow } = useChannels();
  if (open !== 'price' || !currentRow) return null;
  return <ChannelPriceDialog key={currentRow.id} channel={currentRow} onClose={() => setOpen(null)} />;
}

function ChannelPriceDialog({ channel, onClose }: { channel: Channel; onClose: () => void }) {
  const { t } = useTranslation();
  const currentPricing = useChannelModelPrices(channel.id);
  const savePrices = useSaveChannelModelPrices();
  const [dialogContent, setDialogContent] = useState<HTMLDivElement | null>(null);
  const [initialized, setInitialized] = useState(false);
  const [referenceBusy, setReferenceBusy] = useState(false);
  const [targetModel, setTargetModel] = useState('');
  const [reference, setReference] = useState<ModelStandardPrice | null>(null);
  const [search, setSearch] = useState('');
  const debouncedSearch = useDebounce(search, 300);
  const standardPrices = useModelStandardPrices(debouncedSearch.trim(), true);
  const activeRef = useRef(true);
  useEffect(() => {
    activeRef.current = true;
    return () => {
      activeRef.current = false;
    };
  }, []);

  const schema = useMemo(() => createFormSchema(t), [t]);
  const form = useForm<PriceFormData>({ resolver: zodResolver(schema), mode: 'onChange', defaultValues: { serviceTierBilling: { enabled: false, basis: 'upstream' }, multiplier: '1', prices: [] } });
  const { control, getValues, reset } = form;
  const { fields, append, remove, update } = useFieldArray({ control, name: 'prices' });
  const fieldsRef = useRef(fields);
  fieldsRef.current = fields;
  const multiplier = useWatch({ control, name: 'multiplier' });
  const selectedModels = useWatch({ control, name: 'prices', compute: (prices) => prices.map((price) => price.modelId) });
  const supportedModels = useMemo(() => [...new Set(channel.supportedModels)], [channel.supportedModels]);
  const availableModels = useMemo(
    () =>
      selectedModels.map((selected, index) => {
        const otherModels = new Set(selectedModels.filter((_, i) => i !== index));
        const models = supportedModels.filter((id) => !otherModels.has(id));
        if (selected && !models.includes(selected)) models.push(selected);
        return models;
      }),
    [selectedModels, supportedModels]
  );
  const options = useMemo(() => {
    const models = standardPrices.data?.pages.flatMap((page) => page.edges.map(({ node }) => node)) ?? [];
    if (reference && !models.some((model) => model.id === reference.id)) models.push(reference);
    return models;
  }, [standardPrices.data, reference]);

  const priceListRef = useRef<HTMLDivElement>(null);
  const rowVirtualizer = useVirtualizer({
    count: fields.length,
    getScrollElement: () => priceListRef.current,
    estimateSize: () => 450,
    overscan: 2,
    getItemKey: (index) => fields[index]?.id ?? index,
  });
  const pendingScroll = useRef(false);
  useEffect(() => {
    if (pendingScroll.current && fields.length) {
      pendingScroll.current = false;
      rowVirtualizer.scrollToIndex(fields.length - 1, { align: 'start' });
    }
  }, [fields.length, rowVirtualizer]);
  useEffect(() => {
    if (!initialized && currentPricing.isSuccess && currentPricing.data && !currentPricing.isFetching) {
      reset({
        multiplier: currentPricing.data.multiplier,
        serviceTierBilling: currentPricing.data.serviceTierBilling,
        prices: currentPricing.data.prices.map((price) => ({ modelId: price.modelID, price: modelPriceInputSchema.parse(price.price) })),
      });
      setInitialized(true);
    }
  }, [initialized, currentPricing.data, currentPricing.isFetching, currentPricing.isSuccess, reset]);

  const handleClose = useCallback(() => {
    activeRef.current = false;
    onClose();
  }, [onClose]);
  const replacePrice = useCallback(
    (index: number, price: ModelPrice) => {
      update(index, { modelId: getValues(`prices.${index}.modelId`), price: modelPriceInputSchema.parse(price) });
    },
    [getValues, update]
  );
  const applyReference = () => {
    if (!reference || !targetModel) return;
    const price = reference.modelCard.price ? appliedModelPrice(reference.modelCard.price, reference.modelID) : null;
    if (!price) {
      toast.warning(t('price.apply.notFound', { modelId: reference.modelID }));
      return;
    }
    const index = getValues('prices').findIndex((entry) => entry.modelId === targetModel);
    if (index >= 0) replacePrice(index, price);
    else {
      pendingScroll.current = true;
      append({ modelId: targetModel, price: modelPriceInputSchema.parse(price) });
    }
    toast.success(t('price.apply.applied', { modelId: targetModel }));
  };
  const onModelSelected = useCallback(
    async (index: number, modelId: string) => {
      const rowId = fieldsRef.current[index]?.id;
      setReferenceBusy(true);
      try {
        const models = await fetchModelStandardPrices([modelId]);
        if (!activeRef.current || fieldsRef.current[index]?.id !== rowId || getValues(`prices.${index}.modelId`) !== modelId) return;
        const matched = models.find((match) => match.requestModelID === modelId)?.model;
        const price = matched?.modelCard.price;
        if (!price) toast.warning(t('price.apply.notFound', { modelId }));
        else replacePrice(index, appliedModelPrice(price, matched.modelID));
      } catch {
        if (activeRef.current) toast.error(t('price.apply.loadFailed'));
      } finally {
        if (activeRef.current) setReferenceBusy(false);
      }
    },
    [getValues, replacePrice, t]
  );
  const matchAndFill = async () => {
    setReferenceBusy(true);
    try {
      const models = await fetchModelStandardPrices(supportedModels);
      if (!activeRef.current) return;
      const byId = new Map(models.map((match) => [match.requestModelID, match.model]));
      const prices = getValues('prices');
      const existing = new Map(prices.map((price, index) => [price.modelId, index]));
      let applied = 0;
      let added = 0;
      const missed: string[] = [];
      const additions: PriceFormData['prices'] = [];
      for (const modelId of supportedModels) {
        const matched = byId.get(modelId);
        const referencePrice = matched?.modelCard.price;
        const price = referencePrice ? appliedModelPrice(referencePrice, matched.modelID) : null;
        if (!price) {
          missed.push(modelId);
          continue;
        }
        const index = existing.get(modelId);
        if (index !== undefined) {
          replacePrice(index, price);
          applied++;
        } else {
          additions.push({ modelId, price: modelPriceInputSchema.parse(price) });
          added++;
        }
      }
      if (additions.length) {
        pendingScroll.current = true;
        append(additions);
      }
      if (applied || added) toast.success(t('price.apply.bulkSuccess', { applied, added }));
      if (missed.length) toast.warning(t('price.apply.bulkMissed', { missed: missed.length }), { description: missed.join(', ') });
    } catch {
      if (activeRef.current) toast.error(t('price.apply.loadFailed'));
    } finally {
      if (activeRef.current) setReferenceBusy(false);
    }
  };

  const fileInputRef = useRef<HTMLInputElement>(null);
  const handleExport = async () => {
    if (!(await form.trigger()) || !activeRef.current) return;
    const values = schema.parse(getValues());
    const payload = { version: 3, serviceTierBilling: values.serviceTierBilling, multiplier: values.multiplier, prices: values.prices };
    const url = URL.createObjectURL(new Blob([`${JSON.stringify(payload, null, 2)}\n`], { type: 'application/json' }));
    const anchor = document.createElement('a');
    const safeName =
      channel.name
        .trim()
        .replace(/[^\p{L}\p{N}._-]+/gu, '-')
        .replace(/^-+|-+$/g, '') || 'channel';
    anchor.href = url;
    anchor.download = `${safeName}-model-prices.json`;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(url);
    toast.success(t('price.export.success', { name: channel.name }));
  };
  const handleImport = async (file?: File) => {
    if (!file) return;
    if (file.size > 1024 * 1024) {
      toast.error(t('price.import.fileTooLarge'));
      return;
    }
    try {
      const raw = await file.text();
      if (!activeRef.current) return;
      const imported = importSchema.parse(JSON.parse(raw));
      const prices = Array.isArray(imported) ? imported : imported.prices;
      const importedMultiplier = Array.isArray(imported) ? '1' : imported.multiplier;
      const seen = new Set<string>();
      for (const price of prices) {
        if (seen.has(price.modelId)) {
          toast.error(t('price.import.duplicateModel', { modelId: price.modelId }));
          return;
        }
        seen.add(price.modelId);
      }
      const supported = new Set(supportedModels);
      const filtered = prices.filter((price) => supported.has(price.modelId));
      if (prices.length && !filtered.length) {
        toast.error(t('price.import.noSupportedModels'));
        return;
      }
      reset({ multiplier: importedMultiplier, prices: filtered, serviceTierBilling: !Array.isArray(imported) && imported.version === 3 ? imported.serviceTierBilling : { enabled: false, basis: 'upstream' } });
      priceListRef.current?.scrollTo({ top: 0 });
      const skipped = prices.length - filtered.length;
      toast.success(t(skipped ? 'price.import.successSkipped' : 'price.import.success', { count: filtered.length, skipped }));
    } catch {
      if (activeRef.current) toast.error(t('price.import.invalidFile'));
    }
  };
  const onSubmit = async (data: PriceFormData) => {
    try {
      await savePrices.mutateAsync({ channelId: channel.id, input: data.prices, multiplier: data.multiplier, serviceTierBilling: data.serviceTierBilling });
      if (activeRef.current) handleClose();
    } catch {
      /* The mutation displays the server error. */
    }
  };
  const addPrice = () => {
    pendingScroll.current = true;
    append({ modelId: '', price: { items: [{ itemCode: 'prompt_tokens', pricing: { mode: 'usage_per_unit', usagePerUnit: '' } }] } });
  };
  const duplicatePrice = useCallback(
    (index: number) => {
      pendingScroll.current = true;
      append({ modelId: '', price: structuredClone(getValues(`prices.${index}.price`)) });
    },
    [append, getValues]
  );
  const removePrice = useCallback((index: number) => remove(index), [remove]);
  const busy = !initialized || referenceBusy || savePrices.isPending;

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) handleClose();
      }}
    >
      <DialogContent
        ref={setDialogContent}
        className='flex h-[95dvh] max-h-[95dvh] w-[96vw] flex-col gap-3 overflow-hidden p-4 sm:max-w-6xl'
      >
        <DialogHeader className='shrink-0'>
          <DialogTitle>{t('price.title')}</DialogTitle>
          <DialogDescription>{t('price.description', { name: channel.name })}</DialogDescription>
        </DialogHeader>
        <Form {...form}>
          <form
            onSubmit={form.handleSubmit(onSubmit, (errors) => {
              const index = errors.prices?.findIndex?.((error) => Boolean(error)) ?? -1;
              if (index >= 0) rowVirtualizer.scrollToIndex(index, { align: 'start' });
              toast.error(t('price.validation.checkFields'));
            })}
            className='flex min-h-0 flex-1 flex-col gap-3'
          >
            <fieldset
              disabled={busy}
              className='grid max-h-[30dvh] shrink-0 grid-cols-1 gap-2 overflow-y-auto rounded-md border p-3 md:grid-cols-[minmax(0,1fr)_minmax(0,1.5fr)_8rem_auto] md:items-start'
            >
              <div className='min-w-0 space-y-1'>
                <FormLabel>{t('price.apply.target')}</FormLabel>
                <Select value={targetModel} onValueChange={setTargetModel} disabled={busy}>
                  <SelectTrigger className='h-8 w-full'>
                    <SelectValue placeholder={t('price.model')} />
                  </SelectTrigger>
                  <SelectContent>
                    {supportedModels.map((model) => (
                      <SelectItem key={model} value={model}>
                        {model}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className='min-w-0 space-y-1'>
                <FormLabel>{t('price.apply.model')}</FormLabel>
                <AutoCompleteSelect
                  selectedValue={reference?.id ?? ''}
                  onSelectedValueChange={(id) => {
                    const model = options.find((model) => model.id === id) ?? null;
                    setReference(model);
                    if (!targetModel && model && supportedModels.includes(model.modelID)) setTargetModel(model.modelID);
                  }}
                  items={options.map((model) => ({
                    value: model.id,
                    label: `${model.name} (${model.modelID})${model.modelCard.price ? '' : ` · ${t('price.apply.noStandardPrice')}`}`,
                  }))}
                  searchValue={search}
                  onSearchValueChange={setSearch}
                  isLoading={standardPrices.isFetching && !standardPrices.isFetchingNextPage}
                  hasMore={standardPrices.hasNextPage}
                  isLoadingMore={standardPrices.isFetchingNextPage}
                  onLoadMore={standardPrices.fetchNextPage}
                  placeholder={t('price.apply.modelPlaceholder')}
                  emptyMessage={t(standardPrices.isError ? 'price.apply.loadFailed' : 'price.apply.empty')}
                  portalContainer={dialogContent}
                  inputClassName='h-8'
                />
              </div>
              <FormField
                control={control}
                name='multiplier'
                render={({ field }) => (
                  <FormItem className='space-y-1'>
                    <FormLabel>{t('price.apply.multiplier')}</FormLabel>
                    <FormControl>
                      <Input {...field} inputMode='decimal' className='h-8' />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <div className='flex flex-wrap gap-2 md:pt-6'>
                <Button type='button' variant='outline' size='sm' disabled={busy || !reference || !targetModel} onClick={applyReference}>
                  {t('price.apply.useStandard')}
                </Button>
                <Button type='button' variant='outline' size='sm' disabled={busy || !supportedModels.length} onClick={matchAndFill}>
                  {t('price.apply.bulk')}
                </Button>
              </div>
              <p className='text-muted-foreground text-xs md:col-span-4'>{t('price.apply.referenceHint')}</p>
            </fieldset>
            <fieldset disabled={busy} className='flex flex-wrap items-start gap-3 rounded-md border p-3'>
              <FormField control={control} name='serviceTierBilling.enabled' render={({ field }) => (
                <FormItem className='flex items-center gap-2'><FormControl><Switch checked={field.value} onCheckedChange={field.onChange} aria-label={t('serviceTier.settings.enabled')} /></FormControl><FormLabel>{t('serviceTier.settings.enabled')}</FormLabel></FormItem>
              )} />
              <FormField control={control} name='serviceTierBilling.basis' render={({ field }) => (
                <FormItem><FormLabel>{t('serviceTier.settings.basis')}</FormLabel><Select value={field.value} onValueChange={field.onChange}><FormControl><SelectTrigger><SelectValue /></SelectTrigger></FormControl><SelectContent container={dialogContent}><SelectItem value='upstream'>{t('serviceTier.settings.upstream')}</SelectItem><SelectItem value='client'>{t('serviceTier.settings.client')}</SelectItem></SelectContent></Select></FormItem>
              )} />
              <p className='text-muted-foreground w-full text-xs'>{t('serviceTier.settings.fallback')}</p>
            </fieldset>
            <div ref={priceListRef} className='min-h-0 flex-1 overflow-x-hidden overflow-y-auto pr-2'>
              {!initialized && (
                <p role='status' className='text-muted-foreground py-8 text-center'>
                  {t(currentPricing.isError ? 'price.apply.loadFailed' : 'common.loading')}
                </p>
              )}
              {!initialized && currentPricing.isError && (
                <Button type='button' variant='outline' onClick={() => currentPricing.refetch()}>
                  {t('price.apply.retry')}
                </Button>
              )}
              {initialized && !fields.length && <p className='text-muted-foreground py-10 text-center'>{t('price.noPrices')}</p>}
              <div style={{ height: rowVirtualizer.getTotalSize(), position: 'relative' }}>
                {rowVirtualizer.getVirtualItems().map((virtualRow) => {
                  const index = virtualRow.index;
                  const field = fields[index];
                  if (!field) return null;
                  return (
                    <div
                      key={virtualRow.key}
                      data-index={index}
                      ref={rowVirtualizer.measureElement}
                      className='absolute top-0 left-0 w-full pb-3'
                      style={{ transform: `translateY(${virtualRow.start}px)` }}
                    >
                      <PriceCard
                        control={control}
                        index={index}
                        availableModels={availableModels[index] ?? supportedModels}
                        portalContainer={dialogContent}
                        multiplier={multiplier}
                        busy={busy}
                        onModelSelected={onModelSelected}
                        onDuplicate={duplicatePrice}
                        onRemove={removePrice}
                      />
                    </div>
                  );
                })}
              </div>
            </div>
            <DialogFooter className='shrink-0 gap-2 border-t pt-3 sm:justify-between'>
              <div className='flex flex-wrap gap-2'>
                <Button type='button' variant='outline' size='sm' disabled={busy} onClick={addPrice}>
                  <IconPlus size={14} />
                  {t('price.addPrice')}
                </Button>
                <Button type='button' variant='outline' size='sm' disabled={busy} onClick={handleExport}>
                  <IconDownload size={14} />
                  {t('price.export.button')}
                </Button>
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  disabled={busy}
                  title={t('price.import.hint')}
                  onClick={() => fileInputRef.current?.click()}
                >
                  <IconUpload size={14} />
                  {t('price.import.button')}
                </Button>
                <input
                  ref={fileInputRef}
                  type='file'
                  accept='.json,application/json'
                  className='hidden'
                  onChange={(event) => {
                    const file = event.target.files?.[0];
                    event.target.value = '';
                    void handleImport(file);
                  }}
                />
              </div>
              <div className='flex gap-2'>
                <Button type='button' variant='ghost' size='sm' onClick={handleClose}>
                  {t('common.buttons.cancel')}
                </Button>
                <Button type='submit' size='sm' disabled={busy}>
                  {t('common.buttons.save')}
                </Button>
              </div>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  );
}
