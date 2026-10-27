import { useMemo, useState } from 'react';
import { useFormContext, useWatch } from 'react-hook-form';
import { useQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useTranslation } from 'react-i18next';
import { useDebounce } from '@/hooks/use-debounce';
import { Button } from '@/components/ui/button';
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { TagsAutocompleteInput } from '@/components/ui/tags-autocomplete-input';
import { type CreateModelInput, type ModelMatchType, modelMatchTypeSchema } from '../data/schema';

export function ModelMatchTypeField() {
  const { t } = useTranslation();
  const form = useFormContext<CreateModelInput>();
  return (
    <FormField
      control={form.control}
      name='matchType'
      render={({ field }) => (
        <FormItem>
          <FormLabel>{t('models.match.type')}</FormLabel>
          <div className='flex gap-1' role='group' aria-label={t('models.match.type')}>
            {modelMatchTypeSchema.options.map((type) => (
              <Button
                key={type}
                type='button'
                size='sm'
                className='flex-1 px-2'
                variant={(field.value ?? 'exact') === type ? 'default' : 'outline'}
                aria-pressed={(field.value ?? 'exact') === type}
                onClick={() => field.onChange(type)}
              >
                {t(`models.match.types.${type}`)}
              </Button>
            ))}
          </div>
          <FormMessage />
        </FormItem>
      )}
    />
  );
}

export function ModelMatchTest({
  matchType,
  pattern,
  value,
  onChange,
}: {
  matchType: ModelMatchType;
  pattern: string;
  value: string;
  onChange: (value: string) => void;
}) {
  const { t } = useTranslation();
  const parameters = useMemo(() => ({ matchType, pattern, modelID: value }), [matchType, pattern, value]);
  const debounced = useDebounce(parameters, 350);
  const test = useQuery({
    queryKey: ['model-match-test', debounced],
    queryFn: async () => {
      const result = await graphqlRequest<{ testModelMatch: { matched: boolean; error?: string | null } }>(
        `query TestModelMatch($matchType: ModelMatchType!, $pattern: String!, $modelID: String!) {
          testModelMatch(matchType: $matchType, pattern: $pattern, modelID: $modelID) { matched error }
        }`,
        debounced
      );
      return result.testModelMatch;
    },
    enabled: Boolean(debounced.pattern && debounced.modelID),
    retry: false,
  });
  const current = debounced.matchType === matchType && debounced.pattern === pattern && debounced.modelID === value;
  return (
    <div className='bg-muted/30 space-y-2 rounded-md border p-3'>
      <Label htmlFor='model-match-test'>{t('models.match.test')}</Label>
      <Input
        id='model-match-test'
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={t('models.match.testPlaceholder')}
        className='font-mono text-xs'
      />
      <p className='text-muted-foreground text-xs'>{t('models.match.testHelp')}</p>
      {value && pattern && (
        <p role='status' className={`text-xs ${test.data?.matched ? 'text-emerald-600' : 'text-muted-foreground'}`}>
          {!current || test.isFetching
            ? t('models.match.testing')
            : test.isError
              ? t('models.match.testUnavailable')
              : test.data?.error
                ? t('models.match.invalid', { error: test.data.error })
                : test.data?.matched
                  ? t('models.match.matched')
                  : t('models.match.notMatched')}
        </p>
      )}
    </div>
  );
}

export function ModelMatchDetails() {
  const { t } = useTranslation();
  const form = useFormContext<CreateModelInput>();
  const matchType = useWatch({ control: form.control, name: 'matchType' }) ?? 'exact';
  const modelID = useWatch({ control: form.control, name: 'modelID' });
  const [testModelID, setTestModelID] = useState('');
  return (
    <>
      <ModelMatchTest matchType={matchType} pattern={modelID} value={testModelID} onChange={setTestModelID} />
      {matchType !== 'exact' && (
        <FormField
          control={form.control}
          name='matchPriority'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('models.match.priority')}</FormLabel>
              <FormControl>
                <Input type='number' step='1' value={field.value ?? 100} onChange={(event) => field.onChange(Number(event.target.value))} />
              </FormControl>
              <FormDescription>{t('models.match.priorityHelp')}</FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      )}
      <FormField
        control={form.control}
        name='publicModelIds'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('models.match.publicIds')}</FormLabel>
            <FormControl>
              <TagsAutocompleteInput
                value={field.value ?? (matchType === 'exact' && modelID ? [modelID] : [])}
                onChange={field.onChange}
                inputLabel={t('models.match.publicIds')}
                placeholder={t('models.match.publicIdsPlaceholder')}
              />
            </FormControl>
            <FormDescription>{t('models.match.publicIdsHelp')}</FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
    </>
  );
}
