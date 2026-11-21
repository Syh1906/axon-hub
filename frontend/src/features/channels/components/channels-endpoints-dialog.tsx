'use client';

import { useState, useMemo, useCallback, useEffect, useRef } from 'react';
import { ChannelCapabilityProbes } from './channel-capability-probes';
import { appendProbeEndpoints, type ProbeEdits } from '../data/capability-probes';
import { AlertCircle, Check, ChevronDown, Pencil, Plus, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from '@/components/ui/alert-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Switch } from '@/components/ui/switch';
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs';
import { ChannelProtocolPolicyEditor, type ProtocolPolicyDraft } from './channel-protocol-policy-editor';
import { AutoCompleteSelect } from '@/components/auto-complete-select';
import { useChannels } from '../context/channels-context';
import { cn } from '@/lib/utils';
import { useUpdateChannelSettings } from '../data/channels';
import { getAvailableProtocolFormats, getProbeConfiguredFormats, getConfigurableApiFormatsForChannelType, buildCodexResponsesEndpoints, getCodexResponsesWebSocketState, getCodexResponsesWebSocketURL, isResponsesFamilyFormat, toHttpBaseURL, translateLegacyCodexModelProtocols } from '../data/protocol-options';
import { Channel, ChannelEndpoint, ModelProtocol, configurableChannelEndpointApiFormats, configurableChannelEndpointApiFormatSchema } from '../data/schema';
import { ChannelEndpointEditor } from './channel-endpoint-editor';
import { ChannelWSVerification } from './channel-ws-verification';
interface Props {
  channel: Channel;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

const ALLOWED_API_FORMATS = configurableChannelEndpointApiFormatSchema.options;
const OPENAI_RESPONSES_WEBSOCKET = 'openai/responses-ws';
// WebSocket Responses endpoints are valid persisted endpoints, but they are not
// offered as custom endpoints: Codex manages them with its WebSocket checkbox.
const RECOGNIZED_API_FORMATS: readonly string[] = [...ALLOWED_API_FORMATS, OPENAI_RESPONSES_WEBSOCKET];

// Codex saves canonical Responses endpoints, so legacy WebSocket-only restrictions
// are edited in their WebSocket identity (mirrors the backend legacy mapping).
function getInitialModelProtocols(channel: Pick<Channel, 'type' | 'baseURL' | 'endpoints' | 'settings'>): ModelProtocol[] {
  const protocols = channel.settings?.modelProtocols ?? [];
  return (channel.type === 'codex' && translateLegacyCodexModelProtocols(channel, protocols)) || protocols;
}

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

  const isCodexChannel = channel.type === 'codex';
  const defaultEndpoints = useMemo(() => {
    const defaults = channel.defaultEndpoints ?? [];
    return channel.type === 'codex' ? defaults.filter((ep) => !isResponsesFamilyFormat(ep.apiFormat)) : defaults;
  }, [channel.type, channel.defaultEndpoints]);
  const [codexResponsesWebSocket, setCodexResponsesWebSocket] = useState(() => getCodexResponsesWebSocketState(channel).enabled);

  const [endpoints, setEndpoints] = useState<ChannelEndpoint[]>(() => channel.endpoints ?? []);
  const [modelProtocols, setModelProtocols] = useState<ModelProtocol[]>(() => getInitialModelProtocols(channel));
  const [protocolPolicy, setProtocolPolicy] = useState<ProtocolPolicyDraft>(() => channel.settings?.protocolPolicy ?? { allowedProtocols: [], sameProtocolOnly: false });
  const [restrictedProtocols, setRestrictedProtocols] = useState(() => !!channel.settings?.protocolPolicy?.allowedProtocols.length);
  const [activeTab, setActiveTab] = useState('upstream');
  const [newProtocolModel, setNewProtocolModel] = useState('');
  const [newProtocolFormats, setNewProtocolFormats] = useState<string[]>([]);
  const [editingProtocolModel, setEditingProtocolModel] = useState<string | null>(null);
  const [blockedEndpointRemoval, setBlockedEndpointRemoval] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [probeEdits, setProbeEdits] = useState<ProbeEdits>({ settings: {}, additions: [] });
  const probeSettings = {
    adaptedCapabilities: channel.settings?.adaptedCapabilities,
    disabledEndpoints: channel.settings?.disabledEndpoints,
    allowWebSearchTool: channel.settings?.allowWebSearchTool,
    allowImageGenerationTool: channel.settings?.allowImageGenerationTool ?? (
      [...(channel.defaultEndpoints ?? []), ...(channel.endpoints ?? [])].some(ep => isResponsesFamilyFormat(ep.apiFormat) && !channel.settings?.disabledEndpoints?.includes(ep.apiFormat))
        ? true : undefined
    ),
    ...probeEdits.settings,
  };
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
    setCodexResponsesWebSocket(getCodexResponsesWebSocketState(channel).enabled);
    setModelProtocols(getInitialModelProtocols(channel));
    setProtocolPolicy(channel.settings?.protocolPolicy ?? { allowedProtocols: [], sameProtocolOnly: false });
    setRestrictedProtocols(!!channel.settings?.protocolPolicy?.allowedProtocols.length);
    setActiveTab('upstream');
    setNewProtocolModel('');
    setNewProtocolFormats([]);
    setEditingProtocolModel(null);
    setBlockedEndpointRemoval(null);
    setError(null);
    setProbeEdits({settings:{},additions:[]});
  }, [open, channel]);

  const savedEndpoints = useMemo(
    () => isCodexChannel ? buildCodexResponsesEndpoints(endpoints, codexResponsesWebSocket) : endpoints,
    [isCodexChannel, endpoints, codexResponsesWebSocket]
  );
  const codexWebSocketURL = getCodexResponsesWebSocketURL(toHttpBaseURL(channel.baseURL), endpoints);
  const handleCodexWebSocketChange = useCallback((checked: boolean) => {
    if (!checked && modelProtocols.some((mp) => mp.enabled !== false && mp.apiFormats.includes(OPENAI_RESPONSES_WEBSOCKET))) {
      setBlockedEndpointRemoval(OPENAI_RESPONSES_WEBSOCKET);
      return;
    }
    setCodexResponsesWebSocket(checked);
    if (checked) setProbeEdits(previous => ({...previous, settings:{
      ...previous.settings, disabledEndpoints:(previous.settings.disabledEndpoints ?? channel.settings?.disabledEndpoints ?? []).filter(format => format !== OPENAI_RESPONSES_WEBSOCKET),
    }}));
    setBlockedEndpointRemoval(null);
  }, [modelProtocols, channel.settings?.disabledEndpoints]);
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
    return getAvailableProtocolFormats(defaultEndpoints, savedEndpoints).filter(format => !probeSettings.disabledEndpoints?.includes(format));
  }, [defaultEndpoints, savedEndpoints, probeSettings.disabledEndpoints]);

  // A saved-channel probe must test the same upstream address shown in the editor.
  const probeTarget = (items: readonly ChannelEndpoint[]) => JSON.stringify(items
    .filter(ep => ep.path || ep.baseURL).map(ep => [ep.apiFormat,ep.path || '',ep.baseURL || '']).sort());
  const probeConnection = probeTarget(savedEndpoints);
  const probeReady = probeConnection === probeTarget(channel.endpoints ?? []);

  const affectedProtocolModels = useMemo(
    () =>
      blockedEndpointRemoval
        ? modelProtocols.filter((mp) => mp.enabled !== false && mp.apiFormats.includes(blockedEndpointRemoval)).map((mp) => mp.model)
        : [],
    [blockedEndpointRemoval, modelProtocols]
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
    if (restrictedProtocols && protocolPolicy.allowedProtocols.length === 0) {
      setError(t('protocolPolicy.required'));
      setActiveTab('policy');
      return;
    }

    const apiFormats = savedEndpoints.map((ep) => ep.apiFormat);
    const duplicates = apiFormats.filter((f, i) => apiFormats.indexOf(f) !== i);
    if (duplicates.length > 0) {
      setError(t('channels.endpoints.duplicateError'));
      return;
    }

    const invalidApiFormat = apiFormats.find((format) => !RECOGNIZED_API_FORMATS.includes(format) && !channel.endpoints?.some(ep => ep.apiFormat === format) && !defaultEndpoints.some(ep => ep.apiFormat === format));
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

    // Canonical Codex Responses endpoints inherit the channel Base URL, so a legacy
    // WS(S) Base URL is saved as its HTTP(S) equivalent.
    const baseURL = isCodexChannel ? toHttpBaseURL(channel.baseURL) : channel.baseURL;

    try {
      // Endpoints and model protocol overrides must be committed together in one
      // update: saving them separately validates each half against the other's
      // stale state (e.g. deleting an endpoint together with the override that
      // references it would be rejected) and a later failure would leave the
      // earlier change half-applied.
      await updateChannelSettings.mutateAsync({
        id: channel.id,
        input: {
          ...(baseURL !== channel.baseURL ? { baseURL } : {}),
          endpoints: savedEndpoints.map((ep) => ({
            apiFormat: ep.apiFormat,
            path: ep.path || undefined,
            baseURL: ep.baseURL || undefined,
            transport: ep.transport || undefined,
            supportsWebSockets: ep.supportsWebSockets,
            catalogURL: ep.catalogURL || undefined,
            catalogJSON: ep.catalogJSON ?? null,
          })),
        },
        patch: { ...probeEdits.settings, modelProtocols, protocolPolicy: { ...protocolPolicy, allowedProtocols: restrictedProtocols ? protocolPolicy.allowedProtocols : [] } },
      });

      toast.success(t('channels.messages.updateSuccess'));
      onOpenChange(false);
    } catch {
      // error handled by hook
    }
  }, [channel, defaultEndpoints, isCodexChannel, savedEndpoints, modelProtocols, protocolPolicy, restrictedProtocols, onOpenChange, updateChannelSettings, t, probeEdits.settings]);

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent
          ref={setDialogContentElement}
          className={cn('flex max-h-[90vh] w-full max-w-full flex-col overflow-hidden', activeTab === 'policy' ? 'sm:max-w-2xl' : 'sm:max-w-4xl')}
        >
          <DialogHeader className='shrink-0'>
            <DialogTitle>{t('channels.endpoints.title')}</DialogTitle>
            <DialogDescription>{channel.name}</DialogDescription>
          </DialogHeader>

          <Tabs value={activeTab} onValueChange={setActiveTab} className='min-h-0 flex-1'>
            <TabsList className='w-full shrink-0'>
              <TabsTrigger value='upstream'>{t('protocolPolicy.upstreamTab')}</TabsTrigger>
              <TabsTrigger value='policy'>{t('protocolPolicy.policyTab')}</TabsTrigger>
              <TabsTrigger value='models'>{t('protocolPolicy.modelsTab')}</TabsTrigger>
            </TabsList>
          <div className='min-h-0 flex-1 overflow-y-auto py-4'>
            <TabsContent value='upstream' className='flex flex-col gap-6'>
            {isCodexChannel && (
              <div className='space-y-2'>
                <ChannelWSVerification channel={channel} container={dialogContentElement}><div className='flex flex-1 items-center justify-between gap-2'>
                  <span>{t('channels.dialogs.fields.apiFormat.formats.openai/responses')}</span>
                  <label className='flex items-center gap-2 text-sm'>
                    <Checkbox data-testid='codex-endpoints-websocket-checkbox' checked={codexResponsesWebSocket} onCheckedChange={(checked) => handleCodexWebSocketChange(checked === true)} />
                    {t('channels.dialogs.fields.apiFormat.codexWebSocket.label')}
                  </label>
                </div></ChannelWSVerification>
                {codexResponsesWebSocket && codexWebSocketURL && <p data-testid='codex-endpoints-websocket-url' className='text-muted-foreground break-all text-xs'>{t('channels.dialogs.fields.apiFormat.codexWebSocket.derivedURL', { url: codexWebSocketURL })}</p>}
                {getCodexResponsesWebSocketState(channel).webSocketOnly && <p data-testid='codex-endpoints-websocket-upgrade-note' className='text-muted-foreground text-xs'>{t('channels.dialogs.fields.apiFormat.codexWebSocket.legacyUpgrade')}</p>}
              </div>
            )}
            <ChannelEndpointEditor
              verificationChannel={channel}
              defaults={defaultEndpoints}
              endpoints={isCodexChannel ? savedEndpoints.filter((ep) => ep.apiFormat !== OPENAI_RESPONSES_WEBSOCKET) : endpoints}
              showWebSocketOption={!isCodexChannel}
              webSocketDisabled={probeSettings.disabledEndpoints?.includes(OPENAI_RESPONSES_WEBSOCKET)}
              formats={getConfigurableApiFormatsForChannelType(channel.type, configurableChannelEndpointApiFormats)}
              baseURL={channel.baseURL || ''}
              addresses={channel.settings?.upstreamAddresses ?? []}
              onChange={(next, webSocketChoice) => {
                if (webSocketChoice === false && modelProtocols.some(mp => mp.enabled !== false && mp.apiFormats.includes(OPENAI_RESPONSES_WEBSOCKET))) {
                  setBlockedEndpointRemoval(OPENAI_RESPONSES_WEBSOCKET);
                  return;
                }
                const added = next.filter(ep => !endpoints.some(old => old.apiFormat === ep.apiFormat)).map(ep => ep.apiFormat);
                if (webSocketChoice === true) added.push(OPENAI_RESPONSES_WEBSOCKET);
                if (added.length && probeSettings.disabledEndpoints?.some(format => added.includes(format))) {
                  setProbeEdits({...probeEdits, settings:{...probeEdits.settings,disabledEndpoints:probeSettings.disabledEndpoints.filter(format => !added.includes(format))}});
                }
                setEndpoints(next);
              }}
              onRemove={(format) => {
                if (isCodexChannel && format === 'openai/responses') {
                  setEndpoints((prev) => prev.filter((ep) => !isResponsesFamilyFormat(ep.apiFormat)));
                  return;
                }
                handleRequestRemoveEndpoint(format);
              }}
              onManageAddresses={() => setOpen('edit')}
              container={dialogContentElement}
            />

            {open && <ChannelCapabilityProbes
              input={{channelID:channel.id, model:channel.defaultTestModel || undefined}}
              ready={probeReady}
              connection={probeConnection}
              models={channel.supportedModels}
              configured={getProbeConfiguredFormats(defaultEndpoints, savedEndpoints, channel.baseURL)}
              edits={{settings:probeSettings, additions:[]}}
              onBlocked={format => {
                if (!modelProtocols.some(mp => mp.enabled !== false && mp.apiFormats.includes(format))) return false;
                setBlockedEndpointRemoval(format);
                return true;
              }}
              onChange={next => {
                if (next.additions.length) {
                  setEndpoints(previous => appendProbeEndpoints(previous,next.additions));
                  if (isCodexChannel && next.additions.includes(OPENAI_RESPONSES_WEBSOCKET)) setCodexResponsesWebSocket(true);
                }
                if (isCodexChannel && next.settings.disabledEndpoints?.includes(OPENAI_RESPONSES_WEBSOCKET)) setCodexResponsesWebSocket(false);
                setProbeEdits({...next, additions:[]});
              }}
            />}
            </TabsContent>
            <TabsContent value='policy'>
              <ChannelProtocolPolicyEditor value={protocolPolicy} onChange={setProtocolPolicy} restricted={restrictedProtocols} onRestrictedChange={setRestrictedProtocols} />
            </TabsContent>
            <TabsContent value='models'>
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
            </TabsContent>
            {error && (
              <div className='text-destructive bg-destructive/10 flex items-center gap-2 rounded-md px-3 py-2 text-sm'>
                <AlertCircle className='h-4 w-4 shrink-0' />
                <span>{error}</span>
              </div>
            )}
          </div>
          </Tabs>

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
