import { useEffect, useRef, useState } from 'react';
import { useFormContext, useWatch } from 'react-hook-form';
import Decimal from 'decimal.js-light';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import type { ModelPrice, ModelPriceItem, ServiceTierItemRule, ServiceTierPrices } from '@/features/channels/data/schema';
import { fetchModelStandardPrices } from '../data/models';
import { useOfficialTierPrices } from '../data/service-tier';
import {
  appliedModelPrice,
  applyOfficialTierPrices,
  resolveTierItem,
  tierUnitPrice,
  tierItemKeys,
  type TierMode,
  type TierPrice,
} from '../data/service-tier-rules';

function Choice({
  value,
  onChange,
  label,
  options,
  container,
}: {
  value: string;
  onChange: (value: string) => void;
  label: string;
  options: { value: string; label: string }[];
  container: HTMLDivElement | null;
}) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger aria-label={label} className='w-full'>
        <SelectValue />
      </SelectTrigger>
      <SelectContent container={container}>
        <SelectGroup>
          {options.map((option) => (
            <SelectItem key={option.value} value={option.value}>
              {option.label}
            </SelectItem>
          ))}
        </SelectGroup>
      </SelectContent>
    </Select>
  );
}

export function ServiceTierPriceEditor({
  pricePath,
  portalContainer,
  modelID,
  channel,
}: {
  pricePath: string;
  portalContainer: HTMLDivElement | null;
  modelID: string;
  channel: boolean;
}) {
  const { t } = useTranslation();
  const { control, setValue, getValues } = useFormContext();
  const price = useWatch({ control, name: pricePath }) as ModelPrice | null;
  const official = useOfficialTierPrices();
  const priceSignature = JSON.stringify(price);
  const [reference, setReference] = useState('');
  const [application, setApplication] = useState('ratio');
  const [pendingOfficial, setPendingOfficial] = useState<ModelPrice | null>(null);
  const [applying, setApplying] = useState(false);
  useEffect(() => {
    setPendingOfficial(null);
  }, [priceSignature]);
  const applyVersion = useRef(0);
  useEffect(() => {
    applyVersion.current++;
    setApplying(false);
    setPendingOfficial(null);
    return () => {
      applyVersion.current++;
    };
  }, [pricePath, modelID]);
  const row = official.data?.find((entry) => entry.model === (reference || price?.serviceTiers?.fast?.referenceModel || modelID));
  const applyModel = async () => {
    const version = ++applyVersion.current;
    setApplying(true);
    try {
      const model = (await fetchModelStandardPrices([modelID])).find((entry) => entry.requestModelID === modelID)?.model;
      if (version !== applyVersion.current) return;
      if (!model?.modelCard.price) {
        toast.error(t('price.apply.notFound', { modelId: modelID }));
        return;
      }
      setValue(pricePath, appliedModelPrice(model.modelCard.price, model.modelID), { shouldDirty: true, shouldValidate: true });
    } catch {
      if (version === applyVersion.current) toast.error(t('price.apply.loadFailed'));
    } finally {
      if (version === applyVersion.current) setApplying(false);
    }
  };
  return (
    <details className='rounded-md border p-3'>
      <summary className='cursor-pointer text-sm font-medium'>{t('serviceTier.prices.title')}</summary>
      <div className='mt-3 flex flex-col gap-3'>
        <p className='text-muted-foreground text-xs'>{t('serviceTier.prices.baseHint')}</p>
        <Choice
          value={row?.model ?? ''}
          onChange={(value) => {
            setReference(value);
            setPendingOfficial(null);
          }}
          label={t('serviceTier.prices.officialModel')}
          container={portalContainer}
          options={(official.data ?? []).map((entry) => ({ value: entry.model, label: entry.model }))}
        />
        {official.isError && (
          <Button type='button' variant='outline' size='sm' onClick={() => official.refetch()}>
            {t('price.apply.retry')}
          </Button>
        )}
        <div className='flex gap-2'>
          <Choice
            value={application}
            onChange={(value) => {
              setApplication(value);
              setPendingOfficial(null);
            }}
            label={t('serviceTier.items.officialApplication')}
            container={portalContainer}
            options={['ratio', 'unit_price'].map((value) => ({ value, label: t(`serviceTier.items.official_${value}`) }))}
          />
          <Button
            type='button'
            size='sm'
            variant='outline'
            disabled={!row || !price}
            onClick={() => {
              if (row && price) setPendingOfficial(applyOfficialTierPrices(price, row, application));
            }}
          >
            {t('serviceTier.items.reviewOfficial')}
          </Button>
        </div>
        <p className='text-muted-foreground text-xs'>{t('serviceTier.items.officialHint')}</p>
        {row && (
          <a className='text-muted-foreground text-xs underline' href={row.sourceURL} target='_blank' rel='noreferrer'>
            {row.version} · {row.currency} / {t('serviceTier.items.million')} · {row.verifiedAt}
          </a>
        )}
        {pendingOfficial && (
          <div className='flex flex-col gap-2 rounded-md border p-3'>
            <p className='text-xs'>{t('serviceTier.items.applyHint')}</p>
            <TierCostPreview price={price} next={pendingOfficial} />
            <div className='flex gap-2'>
              <Button
                type='button'
                size='sm'
                onClick={() => {
                  if (row)
                    setValue(pricePath, applyOfficialTierPrices(getValues(pricePath), row, application), {
                      shouldDirty: true,
                      shouldValidate: true,
                    });
                  setPendingOfficial(null);
                }}
              >
                {t('serviceTier.items.applyOfficial')}
              </Button>
              <Button type='button' variant='outline' size='sm' onClick={() => setPendingOfficial(null)}>
                {t('common.buttons.cancel')}
              </Button>
            </div>
          </div>
        )}
        <ModeRulesEditor pricePath={pricePath} rulesPath={`${pricePath}.serviceTiers`} portalContainer={portalContainer} />
        <TierCostPreview price={price} />
        {channel && (
          <>
            <Button type='button' variant='outline' size='sm' disabled={applying || !modelID} onClick={applyModel}>
              {t('serviceTier.prices.reapply')}
            </Button>
            <p className='text-muted-foreground text-xs'>{t('serviceTier.prices.copyHint')}</p>
          </>
        )}
      </div>
    </details>
  );
}

export function ModeRulesEditor({
  pricePath,
  rulesPath,
  portalContainer,
  contextPath,
}: {
  pricePath: string;
  rulesPath: string;
  portalContainer: HTMLDivElement | null;
  contextPath?: string;
}) {
  const { t } = useTranslation();
  const { control, setValue } = useFormContext();
  const price = useWatch({ control, name: pricePath }) as ModelPrice | null;
  const rules = useWatch({ control, name: rulesPath }) as ServiceTierPrices | null;
  const scope = useWatch({ control, name: contextPath ?? pricePath }) as { items?: ModelPriceItem[]; above?: number } | null;
  const update = (mode: TierMode, value: TierPrice | null) =>
    setValue(`${rulesPath}.${mode}`, value, { shouldDirty: true, shouldValidate: true });
  const custom = (rate: TierPrice | null | undefined): TierPrice => ({
    ...rate,
    source: 'custom',
    application: '',
    sourceURL: '',
    version: '',
    verifiedAt: '',
    currency: '',
    unit: '',
    unpriced: false,
  });
  const items = scope?.items ?? [];
  return (
    <div className='flex flex-col gap-3'>
      {(['fast', 'ultrafast'] as const).map((mode) => {
        const rate = rules?.[mode];
        const inherited = contextPath ? price?.serviceTiers?.[mode] : null;
        const pricedRules = rate?.items?.filter((rule) => rule.method !== 'unpriced') ?? [];
        const commonFactor =
          rate?.source === 'official' &&
          pricedRules.length > 0 &&
          pricedRules.every((rule) => rule.method === 'multiplier' && String(rule.value) === String(pricedRules[0].value))
            ? pricedRules[0].value
            : null;
        const summary = rate?.unpriced
          ? t('serviceTier.prices.unconfigured')
          : commonFactor != null
            ? t('serviceTier.items.unified', { value: commonFactor })
            : rate?.items?.length
              ? t('serviceTier.items.itemized')
              : rate?.multiplier != null
                ? `×${rate.multiplier}`
                : t(contextPath ? 'serviceTier.items.inherit' : 'serviceTier.prices.unconfigured');
        const setRule = (key: (typeof tierItemKeys)[number], method: string, value?: string) => {
          const existing = rate?.items ?? [];
          const remaining = existing.filter((r) => r.itemCode !== key.itemCode || (r.variantCode ?? '') !== key.variantCode);
          if (method !== 'inherit')
            remaining.push({
              ...key,
              method: method as ServiceTierItemRule['method'],
              ...(method !== 'unpriced' ? { value: value ?? '' } : {}),
            });
          update(mode, remaining.length === 0 && rate?.multiplier == null ? null : { ...custom(rate), items: remaining });
        };
        return (
          <details key={mode} className='rounded-md border p-2'>
            <summary className='cursor-pointer text-sm'>
              <Badge
                variant='outline'
                className='mr-2 border-orange-200 bg-orange-50 text-orange-700 dark:border-orange-800 dark:bg-orange-950/30 dark:text-orange-300'
              >
                {mode === 'fast' ? 'Fast' : 'Ultrafast'}
              </Badge>
              {summary}
            </summary>
            <div className='mt-3 flex flex-col gap-2'>
              <div className='grid grid-cols-[1fr_7rem] gap-2'>
                <Choice
                  value={rate?.unpriced ? 'unpriced' : (rate?.source ?? 'inherit')}
                  label={t('serviceTier.prices.sourceFor', { mode })}
                  container={portalContainer}
                  onChange={(value) => {
                    if (value === 'inherit') update(mode, null);
                    else if (value === 'unpriced') update(mode, { source: 'custom', unpriced: true });
                    else if (value === 'custom') update(mode, custom(rate));
                  }}
                  options={[
                    { value: 'inherit', label: t(contextPath ? 'serviceTier.items.inherit' : 'serviceTier.prices.unconfigured') },
                    { value: 'custom', label: t('serviceTier.prices.custom') },
                    { value: 'unpriced', label: t('serviceTier.items.block') },
                    ...(rate?.source === 'official' || rate?.source === 'model'
                      ? [{ value: rate.source, label: t(`serviceTier.prices.${rate.source}`) }]
                      : []),
                  ]}
                />
                <Input
                  aria-label={`${mode} ${t('serviceTier.items.defaultMultiplier')}`}
                  inputMode='decimal'
                  value={rate?.multiplier ?? ''}
                  placeholder={t('serviceTier.items.inherit')}
                  disabled={rate?.source !== 'custom' || rate.unpriced}
                  onChange={(event) =>
                    update(
                      mode,
                      event.target.value === '' && !rate?.items?.length
                        ? null
                        : { ...custom(rate), multiplier: event.target.value === '' ? null : event.target.value }
                    )
                  }
                />
              </div>
              {(rate?.contextAbove != null || rate?.contextUpTo != null) && (
                <p className='text-muted-foreground text-xs'>
                  {t('serviceTier.items.scope')}: {rate.contextAbove != null ? `> ${rate.contextAbove.toLocaleString()}` : '≥ 0'}
                  {rate.contextUpTo != null ? ` · ≤ ${rate.contextUpTo.toLocaleString()}` : ''}
                </p>
              )}
              <details>
                <summary className='cursor-pointer text-xs'>{t('serviceTier.items.settings')}</summary>
                <p className='text-muted-foreground my-2 text-xs'>{t('serviceTier.items.units')}</p>
                <div className='overflow-x-auto'>
                  <table className='w-full text-xs'>
                    <thead>
                      <tr className='text-left'>
                        <th className='pb-2'>{t('serviceTier.items.item')}</th>
                        <th>{t('serviceTier.items.rule')}</th>
                        <th>{t('serviceTier.items.value')}</th>
                        <th>{t('serviceTier.items.effective')}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {tierItemKeys
                        .filter((key) =>
                          items.some((item) => item.itemCode === key.itemCode && !['flat_fee', 'image_fixed'].includes(item.pricing.mode))
                        )
                        .map((key) => {
                          const rule = rate?.items?.find((r) => r.itemCode === key.itemCode && (r.variantCode ?? '') === key.variantCode);
                          const item = items.find((item) => item.itemCode === key.itemCode)!;
                          const base =
                            item.promptWriteCacheVariants?.find((v) => v.variantCode === key.variantCode)?.pricing ?? item.pricing;
                          const resolved = resolveTierItem(
                            contextPath ? inherited : rate,
                            contextPath ? rate : null,
                            key.itemCode,
                            key.variantCode,
                            (scope?.above ?? -1) + 1
                          );
                          const unit = tierUnitPrice(base, resolved);
                          const label = `${t(`serviceTier.items.${key.itemCode}`)}${key.variantCode ? ` · ${key.variantCode === 'five_min' ? '5m' : '1h'}` : ''}`;
                          return (
                            <tr key={`${key.itemCode}:${key.variantCode}`} className='border-t'>
                              <td className='min-w-20 py-2 pr-2'>{label}</td>
                              <td className='min-w-28 py-2 pr-2'>
                                <Choice
                                  value={rule?.method ?? 'inherit'}
                                  onChange={(method) =>
                                    setRule(key, method, method === rule?.method ? String(rule?.value ?? '') : undefined)
                                  }
                                  label={`${mode} ${label} ${t('serviceTier.items.rule')}`}
                                  container={portalContainer}
                                  options={['inherit', 'multiplier', 'unit_price', 'unpriced'].map((value) => ({
                                    value,
                                    label: t(`serviceTier.items.${value}`),
                                  }))}
                                />
                              </td>
                              <td className='min-w-20 py-2 pr-2'>
                                <Input
                                  className='w-20'
                                  aria-label={`${mode} ${label} ${t('serviceTier.items.value')}`}
                                  value={rule?.value ?? ''}
                                  disabled={!rule || rule.method === 'unpriced'}
                                  inputMode='decimal'
                                  onChange={(event) => {
                                    if (rule) setRule(key, rule.method, event.target.value);
                                  }}
                                />
                              </td>
                              <td className='min-w-20 font-mono'>{unit ?? '—'}</td>
                            </tr>
                          );
                        })}
                    </tbody>
                  </table>
                </div>
                <p className='text-muted-foreground mt-2 text-xs'>{t('serviceTier.items.inheritHint')}</p>
              </details>
            </div>
          </details>
        );
      })}
    </div>
  );
}

function previewAmount(price: ModelPrice | null | undefined, mode: TierMode, input: string, output: string): string | null {
  if (!price || !/^\d+$/.test(input) || !/^\d+$/.test(output)) return null;
  let scope: { items: ModelPriceItem[]; serviceTiers?: ServiceTierPrices | null } = { items: price.items };
  for (const tier of price.volumeTiers ?? []) if (Number(input) > tier.above) scope = tier;
  let total = new Decimal(0);
  for (const code of ['prompt_tokens', 'completion_tokens']) {
    const item = scope.items.find((item) => item.itemCode === code);
    if (!item) return null;
    const unit = tierUnitPrice(
      item.pricing,
      resolveTierItem(price.serviceTiers?.[mode], scope.serviceTiers?.[mode], code, '', Number(input))
    );
    if (unit == null) return null;
    total = total.add(new Decimal(unit).mul(code === 'prompt_tokens' ? input : output).div(1_000_000));
  }
  return total.toFixed(6);
}

function TierCostPreview({ price, next }: { price: ModelPrice | null | undefined; next?: ModelPrice }) {
  const { t } = useTranslation();
  const [input, setInput] = useState('100000');
  const [output, setOutput] = useState('10000');
  const [mode, setMode] = useState<TierMode>('fast');
  return (
    <div className='flex flex-col gap-2 rounded-md border p-3 text-xs'>
      <strong>{t('serviceTier.items.preview')}</strong>
      <div className='flex flex-wrap gap-2'>
        <label>
          {t('requests.columns.input')}
          <Input className='w-28' inputMode='numeric' value={input} onChange={(e) => setInput(e.target.value)} />
        </label>
        <label>
          {t('requests.columns.output')}
          <Input className='w-28' inputMode='numeric' value={output} onChange={(e) => setOutput(e.target.value)} />
        </label>
        <Button type='button' size='sm' variant='outline' onClick={() => setMode(mode === 'fast' ? 'ultrafast' : 'fast')}>
          {mode === 'fast' ? 'Fast' : 'Ultrafast'}
        </Button>
      </div>
      <p>{t('serviceTier.items.previewHint')}</p>
      <p className='font-mono'>
        {t('serviceTier.items.modeTotal')}: {previewAmount(price, mode, input, output) ?? '—'}
        {next && ` → ${previewAmount(next, mode, input, output) ?? '—'}`}
      </p>
    </div>
  );
}
