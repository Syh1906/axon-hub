import { useEffect } from 'react';
import { useController, useFormContext } from 'react-hook-form';
import { IconGripVertical, IconPlus, IconTrash } from '@tabler/icons-react';
import { DndContext, KeyboardSensor, PointerSensor, closestCenter, useSensor, useSensors } from '@dnd-kit/core';
import { SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import type { ImagePricing } from '@/features/channels/data/schema';
import { effectivePrice, switchImagePricingMode } from '@/features/models/data/pricing';

type Rule = ImagePricing['rules'][number];
type Condition = NonNullable<Rule['conditions']>[number];

export function defaultImagePricing(): ImagePricing {
  return { automatic: true, rules: ['1K', '2K', '4K'].map((name) => ({ name, fee: '', match: 'all', conditions: [] })) };
}

export function ImagePriceEditor({ name, currencyPrefix, multiplier }: { name: string; currencyPrefix: string; multiplier?: string }) {
  const { t } = useTranslation();
  const { control, getFieldState, formState } = useFormContext();
  const { field } = useController({ control, name });
  const config = field.value as ImagePricing | null | undefined;
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates })
  );
  useEffect(() => {
    if (!config) field.onChange(defaultImagePricing());
  }, [config, field.onChange]);
  if (!config) return null;
  const change = (rules: Rule[]) => field.onChange({ ...config, rules });
  const update = (index: number, rule: Rule) => change(config.rules.map((current, i) => (i === index ? rule : current)));
  const error = (path: string) => getFieldState(`${name}${path}`, formState).error?.message;
  return (
    <div className='col-span-full mt-2 ml-4 flex min-w-0 flex-col gap-3 rounded-md border border-dashed p-3'>
      <div className='flex items-center gap-2'>
        <Switch
          id={`${name}-automatic`}
          checked={config.automatic}
          onCheckedChange={(automatic) => field.onChange(switchImagePricingMode(config, automatic))}
        />
        <Label htmlFor={`${name}-automatic`}>{t('price.image.automatic')}</Label>
        <span className='text-muted-foreground text-xs'>{t(config.automatic ? 'price.image.defaultOn' : 'price.image.manualShort')}</span>
      </div>
      <p className='text-muted-foreground text-xs'>{t(config.automatic ? 'price.image.autoHint' : 'price.image.manualHint')}</p>
      {config.automatic && (
        <details className='text-muted-foreground text-xs'>
          <summary className='cursor-pointer'>{t('price.image.explain')}</summary>
          <p className='mt-2 leading-relaxed'>{t('price.image.explanation')}</p>
        </details>
      )}
      <DndContext
        sensors={sensors}
        collisionDetection={closestCenter}
        onDragEnd={({ active, over }) => {
          if (!over || active.id === over.id) return;
          const rules = [...config.rules];
          const [moved] = rules.splice(Number(active.id), 1);
          rules.splice(Number(over.id), 0, moved);
          change(rules);
        }}
      >
        <SortableContext items={config.rules.map((_, i) => String(i))} strategy={verticalListSortingStrategy}>
          {config.rules.map((rule, index) => (
            <ImageRule
              key={index}
              id={String(index)}
              rule={rule}
              automatic={config.automatic}
              currencyPrefix={currencyPrefix}
              multiplier={multiplier}
              onChange={(next) => update(index, next)}
              onRemove={() => change(config.rules.filter((_, i) => i !== index))}
              error={(path) => error(`.rules.${index}${path}`)}
            />
          ))}
        </SortableContext>
      </DndContext>
      <Button
        type='button'
        size='sm'
        variant='outline'
        disabled={config.automatic && config.rules.length >= 3}
        onClick={() =>
          change([
            ...config.rules,
            {
              name: config.automatic
                ? (['1K', '2K', '4K'].find((tier) => !config.rules.some((rule) => rule.name === tier)) ?? '1K')
                : t('price.image.newRule'),
              fee: '',
              match: 'all',
              conditions: config.automatic ? [] : [{ dimension: 'width', operator: 'gt', value: 1024 }],
            },
          ])
        }
      >
        <IconPlus size={14} />
        {t(config.automatic ? 'price.image.addTier' : 'price.image.addRule')}
      </Button>
      {error('') && <p className='text-destructive text-xs'>{error('')}</p>}
      <p className='text-muted-foreground text-xs'>{t('price.image.unpricedHint')}</p>
    </div>
  );
}

function Choice({
  value,
  options,
  label,
  onChange,
}: {
  value: string;
  options: string[];
  label: string;
  onChange: (value: string) => void;
}) {
  const { t } = useTranslation();
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger size='sm' className='h-8 min-w-0' aria-label={label}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {options.map((option) => (
          <SelectItem key={option} value={option}>
            {t(`price.image.${option}`, { defaultValue: option })}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

function ImageRule({
  id,
  rule,
  automatic,
  currencyPrefix,
  multiplier,
  onChange,
  onRemove,
  error,
}: {
  id: string;
  rule: Rule;
  automatic: boolean;
  currencyPrefix: string;
  multiplier?: string;
  onChange: (rule: Rule) => void;
  onRemove: () => void;
  error: (path: string) => string | undefined;
}) {
  const { t } = useTranslation();
  const { attributes, listeners, setNodeRef, transform, transition } = useSortable({ id, disabled: automatic });
  const conditions = rule.conditions ?? [];
  const setCondition = (index: number, patch: Partial<Condition>) =>
    onChange({ ...rule, conditions: conditions.map((condition, i) => (i === index ? { ...condition, ...patch } : condition)) });
  const amount = multiplier === undefined ? null : effectivePrice(rule.fee, multiplier);
  return (
    <div
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      className={automatic ? 'flex min-w-0 flex-col gap-1' : 'bg-background flex min-w-0 flex-col gap-2 rounded-md border p-2'}
    >
      <div className='flex min-w-0 flex-wrap items-center gap-2'>
        {!automatic && (
          <Button type='button' variant='ghost' size='icon-sm' {...attributes} {...listeners} aria-label={t('price.image.reorder')}>
            <IconGripVertical size={14} />
          </Button>
        )}
        <div className='min-w-0 flex-1'>
          {automatic ? (
            <Choice
              value={rule.name}
              options={['1K', '2K', '4K']}
              label={t('price.image.tier')}
              onChange={(name) => onChange({ ...rule, name })}
            />
          ) : (
            <Input
              className='h-8'
              aria-label={t('price.image.ruleName')}
              value={rule.name}
              onChange={(event) => onChange({ ...rule, name: event.target.value })}
            />
          )}
          {error('.name') && <p className='text-destructive text-xs'>{error('.name')}</p>}
        </div>
        <div className='relative min-w-0 flex-1'>
          <span className='text-muted-foreground absolute top-2 left-2 text-xs'>{currencyPrefix}</span>
          <Input
            className='h-8 pl-7 text-right'
            inputMode='decimal'
            aria-label={t('price.enterPrice')}
            value={rule.fee}
            onChange={(event) => onChange({ ...rule, fee: event.target.value })}
            placeholder={t('price.enterPrice')}
          />
          {error('.fee') && <p className='text-destructive text-xs'>{error('.fee')}</p>}
        </div>
        <span className='text-muted-foreground shrink-0 text-xs'>/{t('price.image.imageUnit')}</span>
        {amount !== null && (
          <span className='text-muted-foreground w-28 truncate text-right text-xs' title={`${currencyPrefix}${amount}`}>
            {t('price.effective', { price: `${currencyPrefix}${amount}` })}
          </span>
        )}
        <Button
          type='button'
          variant='ghost'
          size='icon-sm'
          className='text-destructive'
          aria-label={t('price.image.removeRule')}
          onClick={onRemove}
        >
          <IconTrash size={14} />
        </Button>
      </div>
      {!automatic && (
        <>
          <div className='flex flex-wrap items-center gap-2'>
            <Choice
              value={rule.match}
              options={['any', 'all']}
              label={t('price.image.match')}
              onChange={(match) => onChange({ ...rule, match: match as Rule['match'] })}
            />
            <Button
              type='button'
              size='sm'
              variant='outline'
              onClick={() =>
                onChange({
                  ...rule,
                  match: 'any',
                  conditions: [
                    { dimension: 'width', operator: 'gt', value: 1024 },
                    { dimension: 'height', operator: 'gt', value: 1024 },
                  ],
                })
              }
            >
              {t('price.image.exceedsSize')}
            </Button>
            <Button
              type='button'
              size='sm'
              variant='outline'
              onClick={() =>
                onChange({
                  ...rule,
                  match: 'all',
                  conditions: [
                    { dimension: 'width', operator: 'eq', value: 1024 },
                    { dimension: 'height', operator: 'eq', value: 1024 },
                  ],
                })
              }
            >
              {t('price.image.exactSize')}
            </Button>
          </div>
          {conditions.map((condition, index) => (
            <div key={index} className='flex min-w-0 flex-wrap items-center gap-2'>
              <Choice
                value={condition.dimension}
                options={['width', 'height']}
                label={t('price.image.dimension')}
                onChange={(dimension) => setCondition(index, { dimension: dimension as Condition['dimension'] })}
              />
              <Choice
                value={condition.operator}
                options={['gt', 'gte', 'eq', 'lte', 'lt']}
                label={t('price.image.operator')}
                onChange={(operator) => setCondition(index, { operator: operator as Condition['operator'] })}
              />
              <Input
                className='h-8 w-24'
                type='number'
                min={1}
                max={1000000}
                step={1}
                aria-label={t(`price.image.${condition.dimension}`)}
                value={condition.value || ''}
                onChange={(event) => setCondition(index, { value: Number(event.target.value) })}
              />
              <span className='text-muted-foreground text-xs'>px</span>
              <Button
                type='button'
                variant='ghost'
                size='icon-sm'
                aria-label={t('price.image.removeCondition')}
                onClick={() => onChange({ ...rule, conditions: conditions.filter((_, i) => i !== index) })}
              >
                <IconTrash size={14} />
              </Button>
              {error(`.conditions.${index}.value`) && (
                <p className='text-destructive w-full text-xs'>{error(`.conditions.${index}.value`)}</p>
              )}
            </div>
          ))}
          {error('.conditions') && <p className='text-destructive text-xs'>{error('.conditions')}</p>}
          <Button
            type='button'
            size='sm'
            variant='ghost'
            className='self-start'
            onClick={() => onChange({ ...rule, conditions: [...conditions, { dimension: 'height', operator: 'gt', value: 1024 }] })}
          >
            <IconPlus size={14} />
            {t('price.image.addCondition')}
          </Button>
        </>
      )}
    </div>
  );
}
