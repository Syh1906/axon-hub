import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { endpointName, endpointDefaultPath, endpointRequestURL, mergeEndpoints } from '../data/channel-addresses';
import type { ChannelAddress, ChannelEndpoint } from '../data/schema';
import { CodexEndpointOptions } from './codex-endpoint-options';

export function ChannelEndpointEditor({
  defaults,
  endpoints,
  formats,
  baseURL,
  addresses,
  onChange,
  onRemove,
  onManageAddresses,
  container,
  showWebSocketOption = true,
}: {
  defaults: ChannelEndpoint[];
  endpoints: ChannelEndpoint[];
  formats: readonly string[];
  baseURL: string;
  addresses: ChannelAddress[];
  onChange: (endpoints: ChannelEndpoint[]) => void;
  onRemove: (format: string) => void;
  onManageAddresses: () => void;
  container?: HTMLElement | null;
  showWebSocketOption?: boolean;
}) {
  const { t } = useTranslation();
  const all = mergeEndpoints(defaults, endpoints);
  const [selected, setSelected] = useState(all[0]?.apiFormat ?? '');
  const [adding, setAdding] = useState(false);
  const endpoint = all.find((item) => item.apiFormat === selected);
  const update = (patch: Partial<ChannelEndpoint>) => {
    if (!endpoint) return;
    const next = { ...endpoint, ...patch };
    onChange(
      endpoints.some((item) => item.apiFormat === selected)
        ? endpoints.map((item) => (item.apiFormat === selected ? next : item))
        : [...endpoints, next]
    );
  };
  const enabled = addresses.filter((address) => address.enabled).sort((a, b) => a.priority - b.priority);
  const previewBases = endpoint?.baseURL
    ? [{ url: endpoint.baseURL, name: t('channels.addresses.custom'), priority: 0 }]
    : enabled.length
      ? enabled
      : [{ url: baseURL, name: t('channels.addresses.primary'), priority: 10 }];
  return (
    <div className='flex flex-col gap-4'>
      <div className='max-h-64 overflow-auto rounded-lg border'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('channels.addresses.interface')}</TableHead>
              <TableHead>{t('channels.endpoints.path')}</TableHead>
              <TableHead>{t('channels.addresses.usingAddresses')}</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {all.map((item) => (
              <TableRow key={item.apiFormat}>
                <TableCell>
                  {endpointName(item.apiFormat)}{' '}
                  {defaults[0]?.apiFormat === item.apiFormat && <Badge variant='outline'>{t('channels.addresses.default')}</Badge>}
                </TableCell>
                <TableCell className='font-mono text-xs'>
                  {item.path || endpointDefaultPath(item.apiFormat) || t('channels.addresses.providerPath')}
                </TableCell>
                <TableCell className='max-w-40 truncate text-xs'>
                  {item.baseURL || t('channels.addresses.inherit', { count: enabled.length || 1 })}
                </TableCell>
                <TableCell>
                  <Button type='button' variant='outline' size='sm' onClick={() => setSelected(item.apiFormat)}>
                    {t('channels.addresses.configure')}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      {endpoint && (
        <div className='flex flex-col gap-3 rounded-lg border p-4'>
          <h3 className='text-sm font-medium'>
            {t('channels.addresses.configure')} {endpointName(endpoint.apiFormat)}
          </h3>
          <div className='flex flex-col gap-2'>
            <Label htmlFor='endpoint-path-edit'>{t('channels.endpoints.path')}</Label>
            <Input
              id='endpoint-path-edit'
              value={endpoint.path ?? ''}
              placeholder={endpointDefaultPath(endpoint.apiFormat) || t('channels.addresses.providerPath')}
              onChange={(event) => update({ path: event.target.value })}
            />
            <p className='text-muted-foreground text-xs'>{t('channels.addresses.pathHint')}</p>
          </div>
          <div className='flex flex-col gap-2'>
            <Label>{t('channels.addresses.usingAddresses')}</Label>
            <div className='flex gap-2'>
              <Select
                value={endpoint.baseURL ? 'custom' : 'channel'}
                onValueChange={(value) => update({ baseURL: value === 'custom' ? baseURL : '' })}
              >
                <SelectTrigger className='flex-1'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent container={container}>
                  <SelectGroup>
                    <SelectItem value='channel'>{t('channels.addresses.inherit', { count: enabled.length || 1 })}</SelectItem>
                    <SelectItem value='custom'>{t('channels.addresses.custom')}</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
              <Button type='button' variant='link' onClick={onManageAddresses}>
                {t('channels.addresses.manageShort')}
              </Button>
            </div>
            {endpoint.baseURL && (
              <Input
                aria-label={t('channels.addresses.url')}
                value={endpoint.baseURL}
                onChange={(event) => update({ baseURL: event.target.value })}
              />
            )}
          </div>
          {endpoint.apiFormat === 'openai/responses' && <CodexEndpointOptions key={endpoint.apiFormat} endpoint={endpoint} baseURL={baseURL} onChange={update} showWebSocketOption={showWebSocketOption} />}
          <div className='bg-muted/40 flex flex-col gap-2 rounded-md p-3'>
            <p className='text-sm font-medium'>{t('channels.addresses.requestURL')}</p>
            {previewBases.slice(0, 3).map((address) => (
              <div key={address.url} className='text-muted-foreground flex gap-2 text-xs'>
                <span className='shrink-0'>
                  {address.name} · {address.priority}
                </span>
                <code className='break-all'>{endpointRequestURL(address.url, endpoint) || t('channels.addresses.providerPath')}</code>
              </div>
            ))}
            {previewBases.length > 3 && (
              <p className='text-muted-foreground text-xs'>{t('channels.addresses.more', { count: previewBases.length - 3 })}</p>
            )}
          </div>
          {endpoints.some((item) => item.apiFormat === selected) && (
            <Button type='button' variant='outline' size='sm' className='self-start' onClick={() => onRemove(selected)}>
              {t(
                defaults.some((item) => item.apiFormat === selected)
                  ? 'channels.addresses.restoreDefault'
                  : 'channels.addresses.removeInterface'
              )}
            </Button>
          )}
        </div>
      )}
      {adding ? (
        <Select
          onValueChange={(format) => {
            onChange([...endpoints, { apiFormat: format }]);
            setSelected(format);
            setAdding(false);
          }}
        >
          <SelectTrigger>
            <SelectValue placeholder={t('channels.addresses.addInterface')} />
          </SelectTrigger>
          <SelectContent container={container}>
            <SelectGroup>
              {formats
                .filter((format) => !all.some((item) => item.apiFormat === format))
                .map((format) => (
                  <SelectItem key={format} value={format}>
                    {endpointName(format)}
                  </SelectItem>
                ))}
            </SelectGroup>
          </SelectContent>
        </Select>
      ) : (
        <Button
          type='button'
          variant='outline'
          size='sm'
          className='self-start'
          disabled={formats.every((format) => all.some((item) => item.apiFormat === format))}
          onClick={() => setAdding(true)}
        >
          {t('channels.addresses.addInterface')}
        </Button>
      )}
    </div>
  );
}
