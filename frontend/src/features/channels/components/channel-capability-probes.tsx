import { useEffect, useId, useRef, useState } from 'react';
import { Loader2, Radar } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { adaptResults, capabilityEnabled, toggleCapability, type ProbeEdits } from '../data/capability-probes';
import { useDetectChannelEndpoints, type DetectChannelEndpointsInput } from '../data/channels';
import type { DetectedChannelEndpoint } from '../data/schema';

const reasonKeys: Record<string, string> = {
  not_found: 'notFound',
  auth_error: 'authError',
  rate_limited: 'rateLimited',
  server_error: 'serverError',
  unreachable: 'unreachable',
  invalid_request: 'invalidRequest',
};

interface Props {
  input: DetectChannelEndpointsInput;
  models: string[];
  configured: string[];
  edits: ProbeEdits;
  onChange: (value: ProbeEdits, toggledFormat?: string) => void;
  startAction?: React.ReactNode;
  compact?: boolean;
  connection?: string;
  ready?: boolean;
  onBlocked?: (format: string) => boolean;
}

export function ChannelCapabilityProbes({
  input,
  models,
  configured,
  edits,
  onChange,
  startAction,
  compact,
  connection,
  ready = true,
  onBlocked,
}: Props) {
  const { t } = useTranslation();
  const detect = useDetectChannelEndpoints();
  const [results, setResults] = useState<DetectedChannelEndpoint[]>([]);
  const [model, setModel] = useState('');
  const id = useId();
  const generation = useRef(0);
  const current = useRef({ edits, onChange, configured });
  current.current = { edits, onChange, configured };
  useEffect(() => {
    setResults([]);
    return () => {
      generation.current++;
    };
  }, [connection]);
  const run = async () => {
    const version = ++generation.current;
    try {
      const rows = await detect.mutateAsync({ ...input, model: model.trim() || input.model });
      if (version !== generation.current) return;
      setResults(rows);
      current.current.onChange(adaptResults(current.current.edits, rows, current.current.configured));
    } catch {
      /* The mutation displays the error. */
    }
  };
  const formats = [...new Set([...results.map((row) => row.apiFormat), ...(edits.settings.adaptedCapabilities ?? [])])];
  const p = (key: string) => t('channels.probes.' + key);
  return (
    <div className='space-y-2'>
      <div className='flex flex-wrap items-center gap-2'>
        {startAction}
        <Button type='button' size='sm' variant='outline' disabled={!ready || detect.isPending} onClick={run}>
          {detect.isPending ? <Loader2 className='size-4 animate-spin' /> : <Radar className='size-4' />}
          {p('run')}
        </Button>
        <label htmlFor={id} className='text-muted-foreground text-xs'>
          {p('model')}
        </label>
        <Input
          id={id}
          list={id + '-models'}
          className='h-8 w-40 text-xs'
          value={model}
          disabled={detect.isPending}
          placeholder={input.model || p('auto')}
          onChange={(event) => setModel(event.target.value)}
        />
        <datalist id={id + '-models'}>
          {models.map((value) => (
            <option key={value} value={value} />
          ))}
        </datalist>
      </div>
      <p className='text-muted-foreground text-xs'>{!compact && !ready ? p('saveConnection') : p(compact ? 'draftHint' : 'hint')}</p>
      {formats.length > 0 && (
        <div className='max-h-96 overflow-auto rounded-md border' aria-live='polite'>
          <table className='w-full text-left text-xs'>
            <thead className='bg-background sticky top-0'>
              <tr className='border-b'>
                <th className='p-2'>{p('capability')}</th>
                <th className='p-2'>{p('result')}</th>
                {!compact && <th className='p-2'>{p('reason')}</th>}
                <th className='p-2'>{p('adaptation')}</th>
              </tr>
            </thead>
            <tbody>
              {formats.map((format) => {
                const row = results.find((item) => item.apiFormat === format);
                const adapted = edits.settings.adaptedCapabilities?.includes(format);
                const enabled = capabilityEnabled(format, edits, configured);
                const reason = !row
                  ? p('previous')
                  : row.supported
                    ? t('channels.probes.evidence.' + format, { defaultValue: p('verified') })
                    : reasonKeys[row.reason]
                      ? t('channels.endpoints.detect.reasons.' + reasonKeys[row.reason])
                      : p(row.reason);
                const evidence = reason + (row && !row.supported && row.statusCode ? ' · ' + row.statusCode : '');
                const status = !row
                  ? 'previous'
                  : row.supported
                    ? 'supported'
                    : ['not_found', 'unsupported_tool'].includes(row.reason)
                      ? 'unsupported'
                      : 'unknown';
                return (
                  <tr key={format} className='border-b last:border-0'>
                    <td className='p-2'>
                      {t('channels.probes.names.' + format, {
                        defaultValue: t('channels.dialogs.fields.apiFormat.formats.' + format, { defaultValue: format }),
                      })}
                    </td>
                    <td className='p-2'>
                      <span
                        className={
                          row?.supported
                            ? 'text-emerald-600 dark:text-emerald-400'
                            : status === 'unknown'
                              ? 'text-amber-600 dark:text-amber-400'
                              : 'text-muted-foreground'
                        }
                      >
                        {p(status)}
                      </span>
                      {compact && row && <div className='text-muted-foreground'>{evidence}</div>}
                    </td>
                    {!compact && <td className='p-2'>{row ? evidence : '—'}</td>}
                    <td className='p-2'>
                      <div className='flex items-center justify-between gap-2'>
                        <span>{p(adapted ? (enabled ? 'on' : 'off') : 'unadapted')}</span>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          className='h-6 px-2 text-xs'
                          disabled={!adapted}
                          onClick={() => {
                            if (enabled && onBlocked?.(format)) return;
                            onChange(toggleCapability(format, edits, configured), format);
                          }}
                        >
                          {p(adapted && enabled ? 'disable' : 'enable')}
                        </Button>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      {results[0]?.model && (
        <p className='text-muted-foreground text-xs'>
          {p('model')} · {results[0].model}
        </p>
      )}
    </div>
  );
}
