import { useId, useState } from 'react';
import { useFormContext } from 'react-hook-form';
import { Download, Info } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { AutoComplete } from '@/components/auto-complete';
import { useGeneralSettings } from '@/features/system/data/system';
import { modelCardFromCatalog } from '../data/catalog-card';
import { priceFromCatalog } from '../data/pricing';
import { useDevelopersData } from '../data/providers';
import type { CreateModelInput } from '../data/schema';

export function ModelsCatalogImport({ portalContainer }: { portalContainer: HTMLDivElement | null }) {
  const { t, i18n } = useTranslation();
  const { data } = useDevelopersData();
  const { data: settings } = useGeneralSettings();
  const form = useFormContext<CreateModelInput>();
  const providerID = useId();
  const [open, setOpen] = useState(false);
  const [developer, setDeveloper] = useState('');
  const [modelID, setModelID] = useState('');
  const [search, setSearch] = useState('');
  const models = data.providers[developer]?.models ?? [];
  const selected = models.find((model) => model.id === modelID);
  const price = priceFromCatalog(selected, developer);
  const importCard = () => {
    if (!selected) return;
    form.reset({ ...form.getValues(), modelCard: modelCardFromCatalog(selected, developer) }, { keepDefaultValues: true });
    toast.success(t('models.cardImport.imported'));
  };
  return (
    <>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <h3 className='text-lg font-semibold'>{t('models.modelCard.title')}</h3>
        <Button
          type='button'
          variant='outline'
          size='sm'
          aria-expanded={open}
          onClick={() => {
            if (!open && !developer)
              setDeveloper(
                form.getValues('developer') in data.providers ? form.getValues('developer') : (Object.keys(data.providers)[0] ?? '')
              );
            setOpen(!open);
          }}
        >
          <Download />
          {t('models.cardImport.title')}
        </Button>
      </div>
      {open && (
        <div className='border-primary/20 bg-muted/20 flex flex-col gap-3 rounded-md border p-3'>
          <h5 className='text-sm font-medium'>{t('models.cardImport.title')}</h5>
          <div className='grid gap-3 sm:grid-cols-2'>
            <div className='flex flex-col gap-1.5'>
              <Label htmlFor={providerID}>{t('models.fields.developer')}</Label>
              <Select
                value={developer}
                onValueChange={(next) => {
                  setDeveloper(next);
                  setModelID('');
                  setSearch('');
                }}
              >
                <SelectTrigger className='w-full' id={providerID}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent container={portalContainer}>
                  <SelectGroup>
                    {Object.entries(data.providers).map(([id, provider]) => (
                      <SelectItem key={id} value={id}>
                        {provider.display_name || provider.name || id}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
            <div className='flex flex-col gap-1.5'>
              <Label>{t('models.cardImport.catalogModel')}</Label>
              <AutoComplete
                selectedValue={modelID}
                onSelectedValueChange={(id) => {
                  setModelID(id);
                  setSearch(id);
                }}
                searchValue={search}
                onSearchValueChange={(text) => {
                  setSearch(text);
                  setModelID((current) => {
                    const item = models.find((model) => model.id === current);
                    return item && (text === item.id || text === `${item.name || item.id} · ${item.id}`) ? current : '';
                  });
                }}
                items={models.map((model) => ({ value: model.id, label: `${model.name || model.id} · ${model.id}` }))}
                portalContainer={portalContainer}
                placeholder={t('models.cardImport.search')}
              />
            </div>
          </div>
          {selected && (
            <div className='bg-background grid gap-1 rounded-md border p-3 text-xs sm:grid-cols-2'>
              <p>
                {t('models.modelCard.toolCall')}: {t(selected.tool_call ? 'models.cardImport.yes' : 'models.cardImport.no')}
              </p>
              <p>
                {t('models.modelCard.temperature')}: {t(selected.temperature ? 'models.cardImport.yes' : 'models.cardImport.no')}
              </p>
              <p>
                {t('models.cardImport.inputModalities')}:{' '}
                {(selected.modalities?.input ?? [])
                  .map((name) => t(`models.modelCard.${name}`, { defaultValue: name.toUpperCase() }))
                  .join(' / ') || '—'}
              </p>
              <p>
                {t('models.cardImport.outputModalities')}:{' '}
                {(selected.modalities?.output ?? [])
                  .map((name) => t(`models.modelCard.${name}`, { defaultValue: name.toUpperCase() }))
                  .join(' / ') || '—'}
              </p>
              <p>
                {t('models.cardImport.context')}: {selected.limit?.context?.toLocaleString() ?? '—'}
              </p>
              <p>
                {t('models.cardImport.outputLimit')}: {selected.limit?.output?.toLocaleString() ?? '—'}
              </p>
            </div>
          )}
          {price ? (
            <>
              <div className='grid gap-2 sm:grid-cols-2 xl:grid-cols-3'>
                {price.items.map((item) => (
                  <div key={item.itemCode} className='bg-background rounded-md border p-2'>
                    <p className='text-muted-foreground text-xs'>{t(`price.itemCodes.${item.itemCode}`)}</p>
                    <p className='mt-1 font-mono text-sm font-medium'>
                      {t('currencies.format', {
                        val: Number(item.pricing.usagePerUnit),
                        currency: settings?.currencyCode,
                        locale: i18n.language === 'zh' ? 'zh-CN' : 'en-US',
                        minimumFractionDigits: 6,
                      })}{' '}
                      / 1M
                    </p>
                  </div>
                ))}
              </div>
              {!!price.volumeTiers?.length && (
                <Badge variant='outline' className='self-start'>
                  {t('price.volume.tierCount', { count: price.volumeTiers.length })}
                </Badge>
              )}
            </>
          ) : (
            <p className='text-muted-foreground text-xs'>{t(selected ? 'models.cardImport.noPrice' : 'models.cardImport.selectModel')}</p>
          )}
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <p className='text-muted-foreground flex items-center gap-1 text-xs'>
              <Info className='size-3.5 shrink-0' />
              {t('models.cardImport.scopeHint')}
            </p>
            <Button type='button' size='sm' disabled={!selected} onClick={importCard}>
              {t('models.cardImport.import')}
            </Button>
          </div>
        </div>
      )}
    </>
  );
}
