import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { graphqlRequest } from '@/gql/graphql';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';

interface Props {
  modelId: string;
  value?: Record<string, unknown> | null;
  onChange: (value: Record<string, unknown> | null) => void;
  onValidityChange: (valid: boolean) => void;
}

export function ModelCodexDefinitionEditor({ modelId, value, onChange, onValidityChange }: Props) {
  const { t } = useTranslation();
  const [version, setVersion] = useState('');
  const [draft, setDraft] = useState('');
  const [confirmed, setConfirmed] = useState(true);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const importRequest = useRef<AbortController | null>(null);
  useEffect(() => () => importRequest.current?.abort(), []);
  useEffect(() => {
    setDraft(value ? JSON.stringify(value, null, 2) : '');
    if (typeof value?.clientVersion === 'string') setVersion(value.clientVersion);
  }, [value]);

  const edit = (text: string) => {
    setDraft(text);
    setConfirmed(false);
    onValidityChange(false);
    setError('');
  };
  const apply = () => {
    try {
      const parsed: unknown = JSON.parse(draft);
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error();
      onChange(parsed as Record<string, unknown>);
      onValidityChange(true);
      setError('');
    } catch {
      setError(t('models.codex.invalid'));
      onValidityChange(false);
    }
  };
  const preview = async () => {
    const controller = new AbortController();
    importRequest.current?.abort();
    importRequest.current = controller;
    const wasApplied = draft === (value ? JSON.stringify(value, null, 2) : '');
    setLoading(true);
    setError('');
    onValidityChange(false);
    try {
      const result = await graphqlRequest<{ previewModelCodexCatalog: Record<string, unknown> }>(
        `query PreviewModelCodexCatalog($id: ID!, $version: String!) {
          previewModelCodexCatalog(id: $id, clientVersion: $version)
        }`,
        { id: modelId, version },
        undefined,
        { signal: controller.signal }
      );
      if (controller.signal.aborted) return;
      edit(JSON.stringify(result.previewModelCodexCatalog, null, 2));
    } catch (e) {
      if (controller.signal.aborted) return;
      setError(e instanceof Error ? e.message : t('models.codex.importFailed'));
      onValidityChange(wasApplied);
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  };
  const clear = () => {
    onChange(null);
    setDraft('');
    setError('');
    onValidityChange(true);
  };

  return (
    <section className='flex flex-col gap-3 rounded-md border p-4'>
      <p className='text-sm font-medium'>{t('models.codex.title')}</p>
      <p className='text-xs text-muted-foreground'>{t('models.codex.description')}</p>
      <div className='flex gap-2'>
        <Input disabled={loading} aria-label={t('models.codex.version')} placeholder={t('models.codex.version')} value={version} onChange={(e) => setVersion(e.target.value)} />
        <Button type='button' variant='outline' disabled={loading || !/^\d+\.\d+\.\d+$/.test(version)} onClick={preview}>{t('models.codex.import')}</Button>
      </div>
      <Textarea disabled={loading} aria-label={t('models.codex.title')} className='h-48 font-mono text-xs' value={draft} onChange={(e) => edit(e.target.value)} />
      <label className='flex items-start gap-2 text-xs'>
        <Checkbox checked={confirmed} onCheckedChange={(checked) => setConfirmed(checked === true)} />
        {t('models.codex.confirm')}
      </label>
      <div className='flex gap-2'>
        <Button type='button' variant='outline' disabled={!confirmed || !draft || loading} onClick={apply}>{t('models.codex.apply')}</Button>
        <Button type='button' variant='ghost' disabled={loading} onClick={clear}>{t('models.codex.clear')}</Button>
      </div>
      {error && <p role='alert' className='text-xs text-destructive'>{error}</p>}
    </section>
  );
}
