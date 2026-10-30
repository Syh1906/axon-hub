import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import type { ChannelEndpoint } from '../data/schema';

export function CodexEndpointOptions({ endpoint, baseURL, onChange, showWebSocketOption = true }: {
  endpoint: ChannelEndpoint;
  baseURL: string;
  onChange: (patch: Partial<ChannelEndpoint>) => void;
  showWebSocketOption?: boolean;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState(() => ({ source: endpoint.catalogJSON, text: endpoint.catalogJSON ? JSON.stringify(endpoint.catalogJSON, null, 2) : '', invalid: false }));
  // Keep the user's formatting during edits, but reset when the parent restores
  // endpoint defaults or loads another saved value.
  if (draft.source !== endpoint.catalogJSON) {
    setDraft({ source: endpoint.catalogJSON, text: endpoint.catalogJSON ? JSON.stringify(endpoint.catalogJSON, null, 2) : '', invalid: false });
  }
  const { text, invalid } = draft;
  const supportsWS = endpoint.supportsWebSockets ?? (endpoint.transport
    ? endpoint.transport === 'websocket'
    : /^wss?:\/\//.test(endpoint.baseURL || baseURL));
  return (
    <div className='flex flex-col gap-3'>
      {showWebSocketOption && <><div className='flex items-center gap-2'>
        <Checkbox id='endpoint-supports-ws' checked={supportsWS} onCheckedChange={(checked) => onChange({ supportsWebSockets: checked === true, transport: '' })} />
        <Label htmlFor='endpoint-supports-ws'>{t('channels.endpoints.supportsWebSockets')}</Label>
      </div>
      <p className='text-muted-foreground text-xs'>{t('channels.endpoints.webSocketsHint')}</p></>}
      <div className='flex flex-col gap-2'>
        <Label htmlFor='endpoint-catalog-url'>{t('channels.endpoints.catalogURL')}</Label>
        <Input id='endpoint-catalog-url' value={endpoint.catalogURL ?? ''} placeholder={t('channels.endpoints.catalogURLPlaceholder')} onChange={(event) => onChange({ catalogURL: event.target.value })} />
        <p className='text-muted-foreground text-xs'>{t('channels.endpoints.catalogURLHint')}</p>
      </div>
      <div className='flex flex-col gap-2'>
        <Label htmlFor='endpoint-catalog-json'>{t('channels.endpoints.catalogJSON')}</Label>
        <Textarea id='endpoint-catalog-json' value={text} rows={4} className='max-h-48 font-mono text-xs' aria-invalid={invalid} placeholder={'{"models": [...]}'} onChange={(event) => {
          const value = event.target.value;
          try {
            const source = value.trim() ? JSON.parse(value) : null;
            setDraft({ source, text: value, invalid: false });
            onChange({ catalogJSON: source });
          } catch {
            setDraft({ source: value, text: value, invalid: true });
            onChange({ catalogJSON: value });
          }
        }} />
        <p className={invalid ? 'text-destructive text-xs' : 'text-muted-foreground text-xs'}>{t(invalid ? 'channels.endpoints.catalogJSONInvalid' : 'channels.endpoints.catalogJSONHint')}</p>
      </div>
    </div>
  );
}
