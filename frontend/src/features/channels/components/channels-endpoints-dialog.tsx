'use client';

import { useState, useMemo, useCallback, useEffect, useRef } from 'react';
import { AlertCircle, Check, ChevronDown, Pencil, Plus, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Switch } from '@/components/ui/switch';
import { AutoCompleteSelect } from '@/components/auto-complete-select';
import { useChannels } from '../context/channels-context';
import { useUpdateChannelSettings } from '../data/channels';
import { getAvailableProtocolFormats, getConfigurableApiFormatsForChannelType } from '../data/protocol-options';
import {
  Channel,
  ChannelEndpoint,
  ModelProtocol,
  configurableChannelEndpointApiFormats,
  configurableChannelEndpointApiFormatSchema,
} from '../data/schema';
import { ChannelEndpointEditor } from './channel-endpoint-editor';

interface Props {
  channel: Channel;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

const ALLOWED_API_FORMATS = configurableChannelEndpointApiFormatSchema.options;

function ProtocolFormatMultiSelect({
  formats,
  selectedFormats,
  onToggle,
  placeholder,
  portalContainer,
}: {
  formats: string[];
  selectedFormats: string[];
  onToggle: (format: string) => void;
  placeholder: string;
  portalContainer?: HTMLElement | null;
}) {
  const [open, setOpen] = useState(false);
  const displayText = selectedFormats.length > 0 ? selectedFormats.join(', ') : placeholder;

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button type='button' variant='outline' className='h-10 w-full min-w-0 justify-between font-normal'>
          <span className={selectedFormats.length > 0 ? 'truncate font-mono text-xs' : 'text-muted-foreground truncate text-xs'}>
            {displayText}
          </span>
          <ChevronDown className='ml-2 h-4 w-4 shrink-0 opacity-50' />
        </Button>
      </PopoverTrigger>
      <PopoverContent container={portalContainer} className='w-[var(--radix-popover-trigger-width)] min-w-64 p-1' align='start'>
        {formats.length === 0 ? (
          <div className='text-muted-foreground px-2 py-3 text-center text-xs'>{placeholder}</div>
        ) : (
          <div className='max-h-56 overflow-y-auto'>
            {formats.map((format) => {
              const checked = selectedFormats.includes(format);
              return (
                <label
                  key={format}
                  className='hover:bg-accent flex w-full cursor-pointer items-center gap-2 rounded-sm px-2 py-2 text-left text-xs'
                >
                  <Checkbox checked={checked} onCheckedChange={() => onToggle(format)} />
                  <span className='truncate font-mono'>{format}</span>
                </label>
              );
            })}
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}

export function ChannelsEndpointsDialog({ channel, open, onOpenChange }: Props) {
  const { t } = useTranslation();
  const updateChannelSettings = useUpdateChannelSettings();
  const { setOpen } = useChannels();
  const [dialogContentElement, setDialogContentElement] = useState<HTMLDivElement | null>(null);
  const wasOpenRef = useRef(false);

  const defaultEndpoints = useMemo(() => channel.defaultEndpoints ?? [], [channel.defaultEndpoints]);

  const [endpoints, setEndpoints] = useState<ChannelEndpoint[]>(() => channel.endpoints ?? []);
  const [modelProtocols, setModelProtocols] = useState<ModelProtocol[]>(() => channel.settings?.modelProtocols ?? []);
  const [newProtocolModel, setNewProtocolModel] = useState('');
  const [newProtocolFormats, setNewProtocolFormats] = useState<string[]>([]);
  const [editingProtocolModel, setEditingProtocolModel] = useState<string | null>(null);
  const [blockedEndpointRemoval, setBlockedEndpointRemoval] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const supportedModelOptions = useMemo(
    () =>
      Array.from(new Set([...channel.supportedModels, ...modelProtocols.map((mp) => mp.model)])).map((model) => ({
        value: model,
        label: model,
      })),
    [channel.supportedModels, modelProtocols]
  );

  const availableModelOptions = useMemo(() => {
    const configuredModels = new Set(modelProtocols.filter((mp) => mp.model !== editingProtocolModel).map((mp) => mp.model));
    return supportedModelOptions.filter((option) => !configuredModels.has(option.value));
  }, [editingProtocolModel, modelProtocols, supportedModelOptions]);

  useEffect(() => {
    const justOpened = open && !wasOpenRef.current;
    wasOpenRef.current = open;
    if (!justOpened) {
      return;
    }

    setEndpoints(channel.endpoints ?? []);
    setModelProtocols(channel.settings?.modelProtocols ?? []);
    setNewProtocolModel('');
    setNewProtocolFormats([]);
    setEditingProtocolModel(null);
    setBlockedEndpointRemoval(null);
    setError(null);
  }, [open, channel.endpoints, channel.settings]);

  const handleRemoveEndpoint = useCallback((apiFormat: string) => {
    setEndpoints((prev) => prev.filter((ep) => ep.apiFormat !== apiFormat));
    setBlockedEndpointRemoval(null);
    setError(null);
  }, []);

  const handleRequestRemoveEndpoint = useCallback(
    (apiFormat: string) => {
      const remainsAvailable = defaultEndpoints.some((ep) => ep.apiFormat === apiFormat);
      if (!remainsAvailable && modelProtocols.some((mp) => mp.enabled !== false && mp.apiFormats.includes(apiFormat))) {
        setBlockedEndpointRemoval(apiFormat);
        return;
      }
      handleRemoveEndpoint(apiFormat);
    },
    [defaultEndpoints, handleRemoveEndpoint, modelProtocols]
  );

  const availableProtocolFormats = useMemo(() => {
    return getAvailableProtocolFormats(defaultEndpoints, endpoints);
  }, [defaultEndpoints, endpoints]);

  const affectedProtocolModels = useMemo(
    () =>
      blockedEndpointRemoval && !defaultEndpoints.some((ep) => ep.apiFormat === blockedEndpointRemoval)
        ? modelProtocols.filter((mp) => mp.apiFormats.includes(blockedEndpointRemoval)).map((mp) => mp.model)
        : [],
    [blockedEndpointRemoval, defaultEndpoints, modelProtocols]
  );

  useEffect(() => {
    setNewProtocolFormats((prev) => prev.filter((format) => availableProtocolFormats.includes(format)));
  }, [availableProtocolFormats]);

  const handleToggleProtocolFormat = useCallback((format: string) => {
    setNewProtocolFormats((prev) => (prev.includes(format) ? prev.filter((f) => f !== format) : [...prev, format]));
  }, []);

  const handleSaveModelProtocol = useCallback(() => {
    setError(null);

    const model = newProtocolModel.trim();
    if (!model) {
      setError(t('channels.endpoints.modelProtocols.modelRequired'));
      return;
    }

    if (modelProtocols.some((mp) => mp.model === model && mp.model !== editingProtocolModel)) {
      setError(t('channels.endpoints.modelProtocols.duplicateModel'));
      return;
    }

    if (newProtocolFormats.length === 0) {
      setError(t('channels.endpoints.modelProtocols.formatsRequired'));
      return;
    }

    if (editingProtocolModel) {
      setModelProtocols((prev) =>
        prev.map((mp) => (mp.model === editingProtocolModel ? { ...mp, model, apiFormats: [...newProtocolFormats] } : mp))
      );
    } else {
      setModelProtocols((prev) => [...prev, { model, apiFormats: [...newProtocolFormats], enabled: true }]);
    }
    setNewProtocolModel('');
    setNewProtocolFormats([]);
    setEditingProtocolModel(null);
  }, [editingProtocolModel, newProtocolModel, newProtocolFormats, modelProtocols, t]);

  const handleStartEditModelProtocol = useCallback((protocol: ModelProtocol) => {
    setNewProtocolModel(protocol.model);
    setNewProtocolFormats([...protocol.apiFormats]);
    setEditingProtocolModel(protocol.model);
    setError(null);
  }, []);

  const handleCancelEditModelProtocol = useCallback(() => {
    setNewProtocolModel('');
    setNewProtocolFormats([]);
    setEditingProtocolModel(null);
    setError(null);
  }, []);

  const handleRemoveModelProtocol = useCallback(
    (model: string) => {
      setModelProtocols((prev) => prev.filter((mp) => mp.model !== model));
      if (editingProtocolModel === model) {
        setEditingProtocolModel(null);
        setNewProtocolModel('');
        setNewProtocolFormats([]);
      }
      setError(null);
    },
    [editingProtocolModel]
  );

  const handleToggleModelProtocol = useCallback((model: string, enabled: boolean) => {
    setModelProtocols((prev) => prev.map((mp) => (mp.model === model ? { ...mp, enabled } : mp)));
    setError(null);
  }, []);

  const handleSave = useCallback(async () => {
    setError(null);

    const apiFormats = endpoints.map((ep) => ep.apiFormat);
    const duplicates = apiFormats.filter((f, i) => apiFormats.indexOf(f) !== i);
    if (duplicates.length > 0) {
      setError(t('channels.endpoints.duplicateError'));
      return;
    }

    const invalidApiFormat = apiFormats.find((format) => !ALLOWED_API_FORMATS.includes(format as (typeof ALLOWED_API_FORMATS)[number]));
    if (invalidApiFormat) {
      setError(t('channels.endpoints.invalidApiFormat', 'Unsupported API format'));
      return;
    }

    // Every model protocol must reference an endpoint that exists after this save.
    // The backend validates against resolved endpoints (type defaults + custom), so
    // the check here must include the default endpoints too.
    const savedFormats = new Set([...defaultEndpoints.map((ep) => ep.apiFormat), ...apiFormats]);
    const invalidProtocolFormat = modelProtocols.find((mp) => mp.enabled !== false && mp.apiFormats.some((f) => !savedFormats.has(f)));
    if (invalidProtocolFormat) {
      setError(t('channels.endpoints.modelProtocols.formatsRequired'));
      return;
    }

    try {
      // Endpoints and model protocol overrides must be committed together in one
      // update: saving them separately validates each half against the other's
      // stale state (e.g. deleting an endpoint together with the override that
      // references it would be rejected) and a later failure would leave the
      // earlier change half-applied.
      await updateChannelSettings.mutateAsync({
        id: channel.id,
        input: {
          endpoints: endpoints.map((ep) => ({
            apiFormat: ep.apiFormat,
            path: ep.path || undefined,
            baseURL: ep.baseURL || undefined,
            transport: ep.transport || undefined,
          })),
        },
        patch: { modelProtocols },
      });

      toast.success(t('channels.messages.updateSuccess'));
      onOpenChange(false);
    } catch {
      // error handled by hook
    }
  }, [channel, defaultEndpoints, endpoints, modelProtocols, onOpenChange, updateChannelSettings, t]);

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent
          ref={setDialogContentElement}
          className='flex max-h-[90vh] w-full max-w-full flex-col overflow-hidden sm:max-w-4xl'
        >
          <DialogHeader className='shrink-0'>
            <DialogTitle>{t('channels.endpoints.title')}</DialogTitle>
            <DialogDescription>{channel.name}</DialogDescription>
          </DialogHeader>

          <div className='min-h-0 flex-1 space-y-6 overflow-y-auto py-4'>
            <ChannelEndpointEditor
              defaults={defaultEndpoints}
              endpoints={endpoints}
              formats={getConfigurableApiFormatsForChannelType(channel.type, configurableChannelEndpointApiFormats)}
              baseURL={channel.baseURL || ''}
              addresses={channel.settings?.upstreamAddresses ?? []}
              onChange={setEndpoints}
              onRemove={handleRequestRemoveEndpoint}
              onManageAddresses={() => setOpen('edit')}
              container={dialogContentElement}
            />

            {/* Model protocol overrides */}
            <details className='rounded-lg border p-3'>
              <summary className='cursor-pointer text-sm font-medium'>
                {t('channels.endpoints.modelProtocols.title')} · {modelProtocols.length}
              </summary>
              <div className='mt-3 flex flex-col gap-3'>
                <div className='flex items-center justify-between'>
                  <label className='text-sm font-medium'>{t('channels.endpoints.modelProtocols.title')}</label>
                  {modelProtocols.length > 0 && (
                    <span className='text-muted-foreground text-xs'>
                      {t('channels.endpoints.modelProtocols.count', { count: modelProtocols.length })}
                    </span>
                  )}
                </div>
                <p className='text-muted-foreground text-xs'>{t('channels.endpoints.modelProtocols.description')}</p>

                <div className='grid items-start gap-3 md:grid-cols-[minmax(10rem,1fr)_minmax(14rem,1.2fr)_minmax(14rem,1.2fr)_auto]'>
                  <div className='min-w-0'>
                    <AutoCompleteSelect
                      selectedValue={newProtocolModel}
                      onSelectedValueChange={setNewProtocolModel}
                      items={availableModelOptions}
                      placeholder={t('channels.endpoints.modelProtocols.modelPlaceholder')}
                      emptyMessage={t(
                        availableModelOptions.length === 0 && supportedModelOptions.length > 0
                          ? 'channels.endpoints.modelProtocols.allModelsConfigured'
                          : 'channels.endpoints.modelProtocols.noModelsAvailable'
                      )}
                      portalContainer={dialogContentElement}
                    />
                  </div>
                  <div className='min-w-0 md:col-span-2'>
                    <ProtocolFormatMultiSelect
                      formats={availableProtocolFormats}
                      selectedFormats={newProtocolFormats}
                      onToggle={handleToggleProtocolFormat}
                      placeholder={t('channels.endpoints.modelProtocols.formatsPlaceholder')}
                      portalContainer={dialogContentElement}
                    />
                  </div>
                  <div className='flex items-center gap-2 justify-self-end md:col-start-4 md:justify-self-auto'>
                    <Button
                      type='button'
                      variant='default'
                      size='icon'
                      onClick={handleSaveModelProtocol}
                      disabled={!newProtocolModel || newProtocolFormats.length === 0 || availableModelOptions.length === 0}
                      className='shrink-0'
                      aria-label={t(
                        editingProtocolModel ? 'channels.endpoints.modelProtocols.save' : 'channels.endpoints.modelProtocols.add'
                      )}
                    >
                      {editingProtocolModel ? <Check className='h-4 w-4' /> : <Plus className='h-4 w-4' />}
                    </Button>
                    {editingProtocolModel && (
                      <Button
                        type='button'
                        variant='ghost'
                        size='icon'
                        onClick={handleCancelEditModelProtocol}
                        className='shrink-0'
                        aria-label={t('channels.endpoints.modelProtocols.cancelEdit')}
                      >
                        <X className='h-4 w-4' />
                      </Button>
                    )}
                  </div>
                </div>

                {modelProtocols.length === 0 ? (
                  <div className='text-muted-foreground rounded-lg border border-dashed p-3 text-center text-xs'>
                    {t('channels.endpoints.modelProtocols.empty')}
                  </div>
                ) : (
                  <div className='divide-y rounded-lg border'>
                    {modelProtocols.map((mp) => (
                      <div
                        key={mp.model}
                        className={`flex items-center justify-between gap-2 px-3 py-2 text-sm ${mp.enabled !== false ? '' : 'bg-muted/30 opacity-70'}`}
                      >
                        <div className='flex min-w-0 items-center gap-2'>
                          <Switch
                            checked={mp.enabled !== false}
                            onCheckedChange={(checked) => handleToggleModelProtocol(mp.model, checked)}
                            aria-label={t('channels.endpoints.modelProtocols.toggle', { model: mp.model })}
                          />
                          <span className='truncate font-mono text-xs'>{mp.model}</span>
                        </div>
                        <div className='flex flex-1 flex-wrap items-center justify-end gap-1.5'>
                          {mp.apiFormats.map((format) => (
                            <Badge key={format} variant='secondary' className='font-mono text-xs'>
                              {format}
                            </Badge>
                          ))}
                          <Button
                            type='button'
                            variant='ghost'
                            size='sm'
                            className='hover:bg-accent h-7 w-7 p-0'
                            onClick={() => handleStartEditModelProtocol(mp)}
                            aria-label={t('channels.endpoints.modelProtocols.edit', { model: mp.model })}
                          >
                            <Pencil className='h-3.5 w-3.5' />
                          </Button>
                          <Button
                            type='button'
                            variant='ghost'
                            size='sm'
                            className='hover:text-destructive hover:bg-destructive/10 h-7 w-7 p-0'
                            onClick={() => handleRemoveModelProtocol(mp.model)}
                            aria-label={t('channels.endpoints.modelProtocols.remove', { model: mp.model })}
                          >
                            <X className='h-3.5 w-3.5' />
                          </Button>
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </details>
            {error && (
              <div className='text-destructive bg-destructive/10 flex items-center gap-2 rounded-md px-3 py-2 text-sm'>
                <AlertCircle className='h-4 w-4 shrink-0' />
                <span>{error}</span>
              </div>
            )}
          </div>

          <DialogFooter className='shrink-0 border-t pt-4'>
            <Button variant='outline' onClick={() => onOpenChange(false)}>
              {t('common.buttons.cancel')}
            </Button>
            <Button onClick={handleSave} disabled={updateChannelSettings.isPending}>
              {updateChannelSettings.isPending ? t('common.buttons.saving') : t('common.buttons.save')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <AlertDialog
        open={blockedEndpointRemoval !== null}
        onOpenChange={(isOpen) => {
          if (!isOpen) setBlockedEndpointRemoval(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader className='text-left'>
            <AlertDialogTitle>{t('channels.endpoints.removeEndpointBlockedTitle')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t('channels.endpoints.removeEndpointBlockedDescription', {
                apiFormat: blockedEndpointRemoval ?? '',
                count: affectedProtocolModels.length,
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          {affectedProtocolModels.length > 0 && (
            <div className='flex flex-wrap gap-1 rounded-md border px-3 py-2'>
              {affectedProtocolModels.map((model) => (
                <Badge key={model} variant='secondary' className='font-mono text-xs'>
                  {model}
                </Badge>
              ))}
            </div>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel>{t('common.buttons.close')}</AlertDialogCancel>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
