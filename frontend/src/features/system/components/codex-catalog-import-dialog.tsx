import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { apiRequest } from '@/lib/api-client';
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Textarea } from '@/components/ui/textarea';

export type CatalogSource = 'official' | 'manual' | 'url';
type Change = { slug: string; action: 'add' | 'remove' | 'update'; fields?: string[]; minimumVersion?: { from: string; to: string } };
type Preview = { revision: string; changes: Change[] };
const repositoryCatalogURL = 'https://raw.githubusercontent.com/Syh1906/axon-hub/refs/heads/release/v1.0.x/catalogs/codex/models.json';
const powershell =
  "$dir = if ($env:CODEX_HOME) { $env:CODEX_HOME } else { Join-Path $env:USERPROFILE '.codex' }; Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $dir 'models_cache.json') | ConvertFrom-Json | Select-Object models,client_version,fetched_at | ConvertTo-Json -Depth 100 -Compress | Set-Clipboard";
const unix = `python3 -c 'import json,os,pathlib; d=json.loads((pathlib.Path(os.environ.get("CODEX_HOME") or pathlib.Path.home()/".codex")/"models_cache.json").read_bytes()); print(json.dumps({k:d[k] for k in ("models","client_version","fetched_at") if k in d}))'`;
const commands = { Windows: powershell, macOS: unix + ' | pbcopy', Linux: unix };

export function CodexCatalogImportDialog({
  activeSource,
  activeURL,
  offline,
  onClose,
  onApplied,
}: {
  activeSource: CatalogSource;
  activeURL?: string;
  offline: boolean;
  onClose: () => void;
  onApplied: () => void;
}) {
  const { t } = useTranslation();
  const [source, setSource] = useState<CatalogSource>(!offline && activeSource === 'url' ? 'url' : 'manual');
  const [url, setURL] = useState(activeURL || repositoryCatalogURL);
  const [subscribe, setSubscribe] = useState(true);
  const [raw, setRaw] = useState('');
  const [cacheInfo, setCacheInfo] = useState('');
  const [platform, setPlatform] = useState<keyof typeof commands>(
    /Mac/i.test(navigator.platform) ? 'macOS' : /Linux/i.test(navigator.platform) ? 'Linux' : 'Windows'
  );
  const { handleCopy } = useCopyToClipboard({ text: commands[platform] });
  const [preview, setPreview] = useState<Preview>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  function reset() {
    setPreview(undefined);
    setCacheInfo('');
    setError('');
  }
  async function submit(apply: boolean) {
    setBusy(true);
    setError('');
    try {
      let catalog;
      if (source === 'manual') {
        const cache = JSON.parse(raw);
        if (!Array.isArray(cache?.models)) throw new Error(t('system.codex.import.invalid'));
        catalog = { models: cache.models, client_version: cache.client_version };
        setCacheInfo([cache.client_version, cache.fetched_at].filter(Boolean).join(' · '));
      }
      const body = source === 'url' ? { source, url: url.trim(), subscribe } : { source, catalog };
      const result = await apiRequest<Preview>('/admin/codex/public-catalog/' + (apply ? 'apply' : 'preview'), {
        method: 'POST',
        requireAuth: true,
        body: { ...body, preview: apply ? preview?.revision : undefined },
      });
      if (apply) onApplied();
      else setPreview(result);
    } catch (cause) {
      setPreview(undefined);
      setError(cause instanceof SyntaxError ? t('system.codex.import.invalid') : cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(false);
    }
  }
  const fieldNames: Record<string, string> = {
    priority: 'priority',
    minimal_client_version: 'minimumVersion',
    display_name: 'name',
    description: 'desc',
    additional_speed_tiers: 'speed',
    service_tiers: 'speed',
    model_messages: 'instructions',
    base_instructions: 'instructions',
    experimental_supported_tools: 'tools',
  };
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
    >
      <DialogContent className='max-h-[90dvh] overflow-y-auto sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>{t('system.codex.import.title')}</DialogTitle>
          <DialogDescription>{t('system.codex.import.help')}</DialogDescription>
        </DialogHeader>
        <fieldset disabled={busy} className='flex min-w-0 flex-col gap-4'>
          <Tabs
            value={source}
            onValueChange={(value) => {
              reset();
              setSource(value as CatalogSource);
            }}
          >
            <TabsList>
              {(['manual', 'url', 'official'] as const).map((s) => (
                <TabsTrigger key={s} value={s} disabled={offline && s !== 'manual'}>
                  {t('system.codex.import.' + s)}
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
          {source === 'manual' && (
            <>
              <div className='flex flex-wrap items-center gap-2'>
                <select
                  className='bg-background rounded-md border px-2 py-1 text-sm'
                  aria-label={t('system.codex.import.platform')}
                  value={platform}
                  onChange={(e) => setPlatform(e.target.value as keyof typeof commands)}
                >
                  {Object.keys(commands).map((p) => (
                    <option key={p}>{p}</option>
                  ))}
                </select>
                <Button type='button' variant='outline' size='sm' onClick={handleCopy}>
                  {t('system.codex.import.copy')}
                </Button>
              </div>
              <p className='text-muted-foreground text-sm'>
                {t(platform === 'Linux' ? 'system.codex.import.linuxHint' : 'system.codex.import.copyHint')}
              </p>
              <Label htmlFor='catalog-json'>{t('system.codex.import.paste')}</Label>
              <Textarea
                id='catalog-json'
                value={raw}
                className='h-20 font-mono'
                onChange={(e) => {
                  reset();
                  setRaw(e.target.value);
                }}
              />
              <Label htmlFor='catalog-file'>{t('system.codex.import.file')}</Label>
              <Input
                id='catalog-file'
                type='file'
                accept='.json,application/json'
                onChange={async (e) => {
                  const file = e.target.files?.[0];
                  if (!file) return;
                  reset();
                  setRaw('');
                  if (file.size > 2 * 1024 * 1024) {
                    setError(t('system.codex.import.large'));
                    return;
                  }
                  setBusy(true);
                  try {
                    setRaw(await file.text());
                  } catch {
                    setError(t('system.codex.import.invalid'));
                  } finally {
                    setBusy(false);
                  }
                }}
              />
            </>
          )}
          {source === 'url' && (
            <>
              <div className='flex items-center justify-between gap-2'>
                <Label htmlFor='catalog-url'>{t('system.codex.import.address')}</Label>
                <Button type='button' size='sm' variant='ghost' disabled={url === repositoryCatalogURL} onClick={() => {
                  reset();
                  setURL(repositoryCatalogURL);
                }}>{t('system.codex.import.restoreRepository')}</Button>
              </div>
              <Input
                id='catalog-url'
                type='url'
                value={url}
                placeholder='https://example.org/models.json'
                onChange={(e) => {
                  reset();
                  setURL(e.target.value);
                }}
              />
              <label className='flex items-center gap-2 text-sm'>
                <Checkbox
                  checked={subscribe}
                  onCheckedChange={(value) => {
                    reset();
                    setSubscribe(value === true);
                  }}
                />
                {t('system.codex.import.subscribe')}
              </label>
              <p className='text-muted-foreground text-sm'>{t('system.codex.import.urlHint')}</p>
            </>
          )}
          {source === 'official' && <p className='text-muted-foreground text-sm'>{t('system.codex.import.officialHint')}</p>}
          {preview && (
            <section className='flex flex-col gap-2' aria-live='polite'>
              <h3 className='font-medium'>{t('system.codex.import.changes', { count: preview.changes.length })}</h3>
              {cacheInfo && <span className='text-muted-foreground text-xs'>{cacheInfo}</span>}
              <div className='max-h-48 divide-y overflow-auto rounded-md border'>
                {preview.changes.map((row) => {
                  const changed = row.fields ?? [];
                  const ordered = [...changed.filter((key) => fieldNames[key]), ...changed.filter((key) => !fieldNames[key])];
                  const fields = [
                    ...new Set(
                      ordered.map((field) => {
                        if (field === 'minimal_client_version' && row.minimumVersion) {
                          return t('system.codex.import.minimumVersionChange', {
                            from: row.minimumVersion.from || t('system.codex.import.unspecified'),
                            to: row.minimumVersion.to || t('system.codex.import.unspecified'),
                          });
                        }
                        return fieldNames[field] ? t('system.codex.import.field.' + fieldNames[field]) : field;
                      })
                    ),
                  ];
                  const summary = fields.slice(0, 2).join(' · ');
                  return (
                    <div key={row.slug} className='grid grid-cols-[minmax(8rem,1fr)_2fr] gap-3 px-3 py-2 text-sm'>
                      <span className='font-medium break-all'>{row.slug}</span>
                      {fields.length > 2 ? (
                        <details className='text-muted-foreground min-w-0'>
                          <summary className='cursor-pointer break-words'>
                            {summary} · +{fields.length - 2}
                          </summary>
                          <p className='mt-1 break-words'>{fields.slice(2).join(' · ')}</p>
                        </details>
                      ) : (
                        <span>{summary || t('system.codex.import.' + row.action)}</span>
                      )}
                    </div>
                  );
                })}
              </div>
            </section>
          )}
        </fieldset>
        {error && (
          <p role='alert' className='text-destructive text-sm'>
            {error}
          </p>
        )}
        <DialogFooter>
          <Button variant='outline' disabled={busy} onClick={onClose}>
            {t('common.buttons.cancel')}
          </Button>
          <Button
            disabled={busy || (source === 'manual' && !raw.trim()) || (source === 'url' && !url.trim())}
            onClick={() => void submit(!!preview)}
          >
            {t(busy ? 'common.loading' : preview ? 'system.codex.import.apply' : 'system.codex.import.preview')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
