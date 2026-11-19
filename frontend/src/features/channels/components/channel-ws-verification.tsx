import { useEffect, useId, useMemo, useState, type ReactNode } from 'react';
import { ChevronDown, ChevronUp } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Separator } from '@/components/ui/separator';
import type { Channel } from '../data/schema';
import { useVerifyChannelWebSocket, useWebSocketEvidence } from '../data/websocket-evidence';

type ProbeChannel = Pick<Channel, 'id' | 'defaultTestModel' | 'supportedModels'>;

export function ChannelWSVerification({
  channel,
  children,
  hint,
  container,
}: {
  channel?: ProbeChannel;
  children: ReactNode;
  hint?: string;
  container?: HTMLElement | null;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const id = useId();
  return (
    <div className='space-y-2'>
      <div className='flex items-center justify-between gap-2'>
        {children}
        {channel && (
          <Button type='button' variant='ghost' size='sm' aria-expanded={open} aria-controls={id} onClick={() => setOpen(!open)}>
            {t(open ? 'channels.ws.collapse' : 'channels.ws.action')}
            {open ? <ChevronUp /> : <ChevronDown />}
          </Button>
        )}
      </div>
      {hint && <p className='text-muted-foreground text-xs'>{hint}</p>}
      {open && channel && (
        <div id={id}>
          <VerificationPanel key={channel.id} channel={channel} container={container} />
        </div>
      )}
    </div>
  );
}

function VerificationPanel({ channel, container }: { channel: ProbeChannel; container?: HTMLElement | null }) {
  const { t, i18n } = useTranslation();
  const models = useMemo(
    () => [...new Set([channel.defaultTestModel, ...channel.supportedModels].filter(Boolean))],
    [channel.defaultTestModel, channel.supportedModels]
  );
  const [selected, setSelected] = useState('');
  const model = models.includes(selected) ? selected : (models[0] ?? '');
  const query = useWebSocketEvidence(channel.id, model);
  const verify = useVerifyChannelWebSocket();
  const evidence = query.data;
  const expires = evidence?.expiresAt ? Date.parse(evidence.expiresAt) : 0;
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    if (!expires || expires <= Date.now()) return;
    const timer = window.setTimeout(() => setNow(Date.now()), Math.min(expires - Date.now() + 1, 2147483647));
    return () => window.clearTimeout(timer);
  }, [expires, now]);
  const expired = !!expires && expires <= Math.max(now, Date.now());
  const state = expired ? 'unknown' : (evidence?.state ?? 'unknown');
  const fieldID = useId();
  return (
    <section className='space-y-2 rounded-lg border p-3 text-sm' aria-label={t('channels.ws.action')}>
      <Label htmlFor={fieldID}>{t('channels.ws.model')}</Label>
      <div className='flex flex-wrap gap-2'>
        <Select
          value={model || undefined}
          onValueChange={(value) => {
            setSelected(value);
            verify.reset();
          }}
          disabled={!models.length || verify.isPending}
        >
          <SelectTrigger id={fieldID} className='min-w-40 flex-1'>
            <SelectValue placeholder={t('channels.ws.chooseModel')} />
          </SelectTrigger>
          <SelectContent container={container}>
            <SelectGroup>
              {models.map((item) => (
                <SelectItem key={item} value={item}>
                  {item}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
        <Button
          type='button'
          variant='outline'
          disabled={!model || verify.isPending}
          onClick={() => verify.mutate({ channelID: channel.id, modelID: model, reuseVerified: false })}
        >
          {t(verify.isPending ? 'channels.ws.checking' : 'channels.ws.action')}
        </Button>
      </div>
      <p className='text-muted-foreground text-xs'>{t('channels.ws.cost')}</p>
      <Separator />
      <div className='space-y-1.5' aria-live='polite'>
        <div className='flex flex-wrap items-center justify-between gap-2'>
          <Badge
            variant='outline'
            className={
              state === 'unavailable'
                ? 'border-amber-600/40 text-amber-600 dark:text-amber-400'
                : state === 'verified'
                  ? 'border-green-600/40 text-green-700 dark:text-green-400'
                  : undefined
            }
          >
            {t(`channels.ws.state.${state}`)}
          </Badge>
          {evidence?.checkedAt && (
            <small className='text-muted-foreground'>
              {t(evidence.source === 'probe' ? 'channels.ws.probe' : 'channels.ws.traffic')} ·{' '}
              {new Date(evidence.checkedAt).toLocaleString(i18n.language, {
                month: '2-digit',
                day: '2-digit',
                hour: '2-digit',
                minute: '2-digit',
                hour12: false,
              })}
            </small>
          )}
        </div>
        {expired && <p className='text-muted-foreground text-xs'>{t('channels.ws.expired')}</p>}
        {evidence?.reason && (
          <p>
            {evidence.reason === 'connection_failed' && evidence.stage && <>{t(`channels.ws.stage.${evidence.stage}`)} · </>}
            {t(`channels.ws.reason.${evidence.reason}`, { defaultValue: t('channels.ws.reason.model_failed') })}
            {evidence.statusCode > 0 && ` (${evidence.statusCode})`}
          </p>
        )}
        {evidence?.model && evidence.model !== model && (
          <p className='text-muted-foreground text-xs'>
            {t('channels.ws.model')}　{evidence.model}
          </p>
        )}
        {evidence?.endpoint && (
          <p className='text-muted-foreground text-xs break-all'>
            {t('channels.ws.endpoint')}　{evidence.endpoint}
          </p>
        )}
        {evidence?.checkedAt && (
          <p className='text-muted-foreground text-xs'>
            {t('channels.ws.credential')}　{evidence.credential || t('channels.ws.defaultCredential')}
          </p>
        )}
        {(verify.error || query.error) && (
          <p className='text-destructive text-xs' role='alert'>
            {(verify.error || query.error)?.message}
          </p>
        )}
      </div>
    </section>
  );
}
