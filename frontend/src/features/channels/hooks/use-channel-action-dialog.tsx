import type { APIKeyStrategyValue } from '../components/channel-api-key-strategy';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { z } from 'zod';
import { useFieldArray, useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useVirtualizer } from '@tanstack/react-virtual';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { useHorizontalScroll } from '@/hooks/use-horizontal-scroll';
import { usePermissions } from '@/hooks/usePermissions';
import { Button } from '@/components/ui/button';
import { FormLabel } from '@/components/ui/form';
import { Textarea } from '@/components/ui/textarea';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { useProxyPresets, useSaveProxyPreset, usePassThroughSettings, useQuotaRoutingSettings, useUserAgentPassThroughSettings } from '@/features/system/data/system';
import { ProxyType } from '../components/channels-proxy-dialog';
import { antigravityOAuthExchange, antigravityOAuthStart } from '../data/antigravity';
import { useRefreshChannelUpstreams } from '../data/channel-upstream';
import { useCreateChannel, useDuplicateChannel, useUpdateChannelSettings, useFetchModels, useAllChannelNames, useAllChannelTags, useChannelDisabledAPIKeys, useDisableChannelAPIKey, useEnableChannelAPIKey, useSyncChannelModels } from '../data/channels';
import { claudecodeOAuthExchange, claudecodeOAuthStart } from '../data/claudecode';
import { codexDecodeAuthJSON, codexOAuthExchange, codexOAuthStart } from '../data/codex';
import { getDefaultBaseURL, getDefaultModels, OPENAI_CHAT_COMPLETIONS, OPENAI_RESPONSES, OPENAI_IMAGE_GENERATION, GEMINI_CONTENTS } from '../data/config_channels';
import { PROVIDER_CONFIGS, getProviderFromChannelType, getApiFormatsForProvider, getChannelTypeForApiFormat, getBaseChannelTypeForProvider } from '../data/config_providers';
import { getModelProtocolsForApiFormat, getCodexResponsesWebSocketState, translateLegacyCodexModelProtocols } from '../data/protocol-options';
import { Channel, ChannelType, ApiFormat, ChannelSettings, ChannelQuotaRoutingMode, createChannelInputSchema, updateChannelInputSchema } from '../data/schema';
import { xaiDecodeSSO, xaiOAuthExchange, xaiOAuthStart } from '../data/xai';
import { ProxyConfig, useOAuthFlow } from '../hooks/use-oauth-flow';
import { recallQuotaRoutingMode, quotaRoutingModeSettingsPatch, formatMaxSingleChannelRetries, parseMaxSingleChannelRetriesInput, isVertexChannelType, emptyVertexAPIKey, getInitialBaseURL, parseOauthToken, getCodexThirdPartySubmitCredentials, getFetchModelsApiKey, getCodexThirdPartyCredentialError, ApiFormatOption, MAX_MODELS_DISPLAY, OPENAI_RESPONSES_WEBSOCKET, Props, ResponsesTransport, alwaysOAuthProviderKeys, baseURLMatchesResponsesTransport, extractCodexAuthJSONText, formatRetryableErrorPatterns, formatRetryableStatusCodes, getInitialApiFormatForRow, getNextDuplicateName, getResponsesTransportBaseURLError, getResponsesTransportFromChannel, getResponsesWebSocketBaseURL, isOfficialClaudeCodeChannel, isOfficialCodexChannel, parseRetryableErrorPatternsInput, parseRetryableStatusCodesInput } from '../utils/channel-dialog';
import { mergeChannelSettingsForUpdate } from '../utils/merge';
import { isSuggestedModel } from '../../models/data/model-suggestions';
import { matchesModelPattern } from '../utils/pattern';

function useDebounce<T>(value: T, delay: number): T {
  const [debouncedValue, setDebouncedValue] = useState<T>(value);

  useEffect(() => {
    const handler = setTimeout(() => {
      setDebouncedValue(value);
    }, delay);

    return () => {
      clearTimeout(handler);
    };
  }, [value, delay]);

  return debouncedValue;
}

export function useChannelActionDialog({ currentRow, duplicateFromRow, open, onOpenChange, showModelsPanel = false }: Props) {
  const { t } = useTranslation();
  const isEdit = !!currentRow;
  const isDuplicate = !!duplicateFromRow && !isEdit;
  const initialRow: Channel | undefined = currentRow || duplicateFromRow;
  const createChannel = useCreateChannel();
  const refreshUpstream = useRefreshChannelUpstreams();
  const duplicateChannel = useDuplicateChannel();
  const updateChannelSettings = useUpdateChannelSettings();
  const fetchModels = useFetchModels();
  const syncChannelModels = useSyncChannelModels();
  const { data: allChannelNames = [], isSuccess: allChannelNamesLoaded } = useAllChannelNames({ enabled: open && isDuplicate });
  const { data: allTags = [], isLoading: isLoadingTags } = useAllChannelTags(undefined, open);
  const { data: proxyPresets = [] } = useProxyPresets(open);
  const saveProxyPreset = useSaveProxyPreset();
  const { hasSystemScope, modelPermissions } = usePermissions();
  const { data: quotaRoutingSettings } = useQuotaRoutingSettings();
  const canReadSystemSettings = hasSystemScope('read_settings');
  const { data: userAgentPassThroughSettings } = useUserAgentPassThroughSettings({ enabled: canReadSystemSettings });
  const { data: passThroughSettings } = usePassThroughSettings({ enabled: canReadSystemSettings });
  const [supportedModels, setSupportedModels] = useState<string[]>(() => initialRow?.supportedModels || []);
  const [manualModels, setManualModels] = useState<string[]>(() => initialRow?.manualModels || []);
  const [newModel, setNewModel] = useState('');
  const [selectedDefaultModels, setSelectedDefaultModels] = useState<string[]>([]);
  const [fetchedModels, setFetchedModels] = useState<string[]>([]);
  const [useFetchedModels, setUseFetchedModels] = useState(false);
  const providerRefs = useRef<Record<string, HTMLDivElement | null>>({});
  const providerListRef = useRef<HTMLDivElement | null>(null);
  const providerHorizontalScrollRef = useHorizontalScroll<HTMLDivElement>();

  // Expandable panel states
  const [showConfiguredModelsPanel, setShowConfiguredModelsPanel] = useState(false);
  const [showFetchedModelsPanel, setShowFetchedModelsPanel] = useState(false);
  const [showSupportedModelsPanel, setShowSupportedModelsPanel] = useState(false);
  const [fetchedModelsSearch, setFetchedModelsSearch] = useState('');
  const [supportedModelsSearch, setSupportedModelsSearch] = useState('');
  const [selectedFetchedModels, setSelectedFetchedModels] = useState<string[]>([]);
  const [showNotAddedModelsOnly, setShowNotAddedModelsOnly] = useState(false);
  const [supportedModelsExpanded, setSupportedModelsExpanded] = useState(false);
  const [showClearAllPopover, setShowClearAllPopover] = useState(false);
  const [applyPatternFilter, setApplyPatternFilter] = useState(false);
  const hasAutoSetDuplicateNameRef = useRef(false);
  const codexThirdPartyBaseURLRef = useRef('');
  const [showApiKey, setShowApiKey] = useState(false);
  const [showCommandCodeAuthCookie, setShowCommandCodeAuthCookie] = useState(false);
  const [showOllamaAuthCookie, setShowOllamaAuthCookie] = useState(false);
  const [showApiKeysPanel, setShowApiKeysPanel] = useState(false);
  const [apiKeysSearch, setApiKeysSearch] = useState('');
  const [selectedKeysToRemove, setSelectedKeysToRemove] = useState<Set<string>>(new Set());
  const [confirmRemoveSelectedOpen, setConfirmRemoveSelectedOpen] = useState(false);
  const [confirmRemoveKey, setConfirmRemoveKey] = useState<string | null>(null);
  const [confirmDisableKey, setConfirmDisableKey] = useState<string | null>(null);
  const [authMode, setAuthMode] = useState<'official' | 'auth-json' | 'third-party'>('official');
  const [codexAuthJSONText, setCodexAuthJSONText] = useState('');
  const [xaiSSOToken, setXaiSSOToken] = useState('');
  const [isImportingXAISSO, setIsImportingXAISSO] = useState(false);
  const [patternError, setPatternError] = useState<string | null>(null);

  // Debounced search values for better performance
  const debouncedFetchedModelsSearch = useDebounce(fetchedModelsSearch, 300);
  const debouncedSupportedModelsSearch = useDebounce(supportedModelsSearch, 300);

  // Refs for virtual scrolling
  const fetchedModelsParentRef = useRef<HTMLDivElement>(null);
  const supportedModelsParentRef = useRef<HTMLDivElement>(null);

  const [proxyType, setProxyType] = useState<ProxyType>(() => {
    if (initialRow?.settings?.proxy?.type) {
      return initialRow.settings.proxy.type as ProxyType;
    }
    return ProxyType.ENVIRONMENT;
  });
  const [proxyUrl, setProxyUrl] = useState(() => initialRow?.settings?.proxy?.url || '');
  const [proxyUsername, setProxyUsername] = useState(() => initialRow?.settings?.proxy?.username || '');
  const [proxyPassword, setProxyPassword] = useState(() => initialRow?.settings?.proxy?.password || '');
  const [passThroughUserAgent, setPassThroughUserAgent] = useState<boolean | null>(() => {
    return initialRow?.settings?.passThroughUserAgent ?? null;
  });
  const [passThroughBody, setPassThroughBody] = useState<boolean | null>(() => {
    return initialRow?.settings?.passThroughBody ?? null;
  });
  const [quotaRoutingMode, setQuotaRoutingMode] = useState<ChannelQuotaRoutingMode>(() => recallQuotaRoutingMode(initialRow?.settings));
  const [retryableStatusCodesText, setRetryableStatusCodesText] = useState(() =>
    formatRetryableStatusCodes(initialRow?.settings?.retryableStatusCodes)
  );
  const [retryableErrorPatternsText, setRetryableErrorPatternsText] = useState(() =>
    formatRetryableErrorPatterns(initialRow?.settings?.retryableErrorPatterns)
  );
  const [maxSingleChannelRetriesText, setMaxSingleChannelRetriesText] = useState(() =>
    formatMaxSingleChannelRetries(initialRow?.settings?.maxSingleChannelRetries)
  );
  const userAgentInheritLabel = userAgentPassThroughSettings
    ? t('channels.dialogs.userAgentPassThrough.inheritWithValue', {
        value: t(
          userAgentPassThroughSettings.enabled
            ? 'channels.dialogs.userAgentPassThrough.enabled'
            : 'channels.dialogs.userAgentPassThrough.disabled'
        ),
      })
    : t('channels.dialogs.userAgentPassThrough.inherit');
  const passThroughInheritLabel = passThroughSettings
    ? t('channels.dialogs.bodyPassThrough.inheritWithValue', {
        value: t(passThroughSettings.enabled ? 'channels.dialogs.bodyPassThrough.enabled' : 'channels.dialogs.bodyPassThrough.disabled'),
      })
    : t('channels.dialogs.bodyPassThrough.inherit');
  const [apiKeyStrategy, setApiKeyStrategy] = useState<APIKeyStrategyValue>(
    () => initialRow?.settings?.apiKeyStrategy ?? 'sticky'
  );
  const [apiKeyRoundRobinSwitchAfter, setApiKeyRoundRobinSwitchAfter] = useState<number>(
    () => initialRow?.settings?.apiKeyRoundRobinSwitchAfter ?? 1
  );

  // Memoized proxy config for OAuth exchange
  const proxyConfig: ProxyConfig | undefined = useMemo(() => {
    if (proxyType === ProxyType.URL && proxyUrl) {
      return {
        type: proxyType,
        url: proxyUrl,
        ...(proxyUsername && { username: proxyUsername }),
        ...(proxyPassword && { password: proxyPassword }),
      };
    }
    return undefined;
  }, [proxyType, proxyUrl, proxyUsername, proxyPassword]);

  const handleProxyPresetSelect = (presetUrl: string) => {
    const preset = proxyPresets.find((p) => p.url === presetUrl);
    if (preset) {
      setProxyType(ProxyType.URL);
      setProxyUrl(preset.url);
      setProxyUsername(preset.username || '');
      setProxyPassword(preset.password || '');
    }
  };

  // OAuth flows using the reusable hook
  // OAuth credentials are stored in apiKey field as JSON string, not in apiKeys array
  const codexOAuth = useOAuthFlow({
    startFn: codexOAuthStart,
    exchangeFn: codexOAuthExchange,
    proxyConfig,
    onSuccess: (credentials) => {
      form.setValue('credentials.apiKey', credentials);
    },
  });

  const claudecodeOAuth = useOAuthFlow({
    startFn: claudecodeOAuthStart,
    exchangeFn: claudecodeOAuthExchange,
    proxyConfig,
    onSuccess: (credentials) => {
      form.setValue('credentials.apiKey', credentials);
    },
  });

  const xaiOAuth = useOAuthFlow({
    startFn: xaiOAuthStart,
    exchangeFn: xaiOAuthExchange,
    proxyConfig,
    onSuccess: (credentials) => {
      form.setValue('credentials.apiKey', credentials);
    },
  });

  const antigravityOAuth = useOAuthFlow({
    startFn: antigravityOAuthStart,
    exchangeFn: antigravityOAuthExchange,
    proxyConfig,
    onSuccess: (credentials) => {
      form.setValue('credentials.apiKey', credentials);
    },
  });
  const { reset: resetCodexOAuth } = codexOAuth;
  const { reset: resetClaudecodeOAuth } = claudecodeOAuth;
  const { reset: resetXaiOAuth } = xaiOAuth;
  const { reset: resetAntigravityOAuth } = antigravityOAuth;

  // Provider-based selection state
  const [selectedProvider, setSelectedProvider] = useState<string>(() => {
    if (initialRow) {
      return getProviderFromChannelType(initialRow.type) || 'openai';
    }
    return 'openai';
  });
  const [selectedApiFormat, setSelectedApiFormat] = useState<ApiFormat>(() =>
    initialRow ? getInitialApiFormatForRow(initialRow) : OPENAI_CHAT_COMPLETIONS
  );
  const [responsesTransport, setResponsesTransport] = useState<ResponsesTransport>(() => getResponsesTransportFromChannel(initialRow));
  const initialCodexResponses = useMemo(() => getCodexResponsesWebSocketState(initialRow), [initialRow]);
  const setCodexResponsesWebSocket = (enabled: boolean) => setResponsesTransport(enabled ? 'websocket' : 'http');
  const [useGeminiVertex, setUseGeminiVertex] = useState(() => {
    if (initialRow) {
      return initialRow.type === 'gemini_vertex';
    }
    return false;
  });
  const [useAnthropicAws, setUseAnthropicAws] = useState(() => {
    if (initialRow) {
      return initialRow.type === 'anthropic_aws';
    }
    return false;
  });
  const [useKimiCoding, setUseKimiCoding] = useState(() => {
    if (initialRow) {
      return initialRow.type === 'moonshot_coding';
    }
    return false;
  });

  useEffect(() => {
    if (!initialRow) return;

    const provider = getProviderFromChannelType(initialRow.type) || 'openai';
    setSelectedProvider(provider);
    setSelectedApiFormat(getInitialApiFormatForRow(initialRow));
    setResponsesTransport(getResponsesTransportFromChannel(initialRow));
    setCodexResponsesWebSocket(getCodexResponsesWebSocketState(initialRow).enabled);
    setUseGeminiVertex(initialRow.type === 'gemini_vertex');
    setUseAnthropicAws(initialRow.type === 'anthropic_aws');
    setUseKimiCoding(initialRow.type === 'moonshot_coding');

    // Detect authMode for codex and claudecode
    if (initialRow.type === 'codex') {
      setAuthMode(isOfficialCodexChannel(initialRow) ? 'official' : 'third-party');
      setCodexAuthJSONText(extractCodexAuthJSONText(initialRow.credentials?.apiKey) || '');
    } else if (initialRow.type === 'claudecode') {
      setAuthMode(isOfficialClaudeCodeChannel(initialRow) ? 'official' : 'third-party');
    }
  }, [initialRow]);

  useEffect(() => {
    if (!open) {
      hasAutoSetDuplicateNameRef.current = false;
      resetCodexOAuth();
      resetClaudecodeOAuth();
      resetAntigravityOAuth();
      resetXaiOAuth();
      setCodexAuthJSONText('');
      codexThirdPartyBaseURLRef.current = '';
      setCodexResponsesWebSocket(initialCodexResponses.enabled);
      setXaiSSOToken('');
    }
  }, [open, resetCodexOAuth, resetClaudecodeOAuth, resetAntigravityOAuth, resetXaiOAuth, initialCodexResponses.enabled]);

  useEffect(() => {
    if (!open) {
      setShowApiKey(false);
      setShowCommandCodeAuthCookie(false);
      setShowOllamaAuthCookie(false);
      setShowApiKeysPanel(false);
      setApiKeysSearch('');
      setSelectedKeysToRemove(new Set());
      setConfirmRemoveSelectedOpen(false);
      setConfirmRemoveKey(null);
      setConfirmDisableKey(null);
      setPatternError(null);
      setFetchedModels([]);
      setUseFetchedModels(false);
      setShowConfiguredModelsPanel(false);
      setShowFetchedModelsPanel(false);
      setShowSupportedModelsPanel(false);
      setSelectedDefaultModels([]);
      setFetchedModelsSearch('');
      setSupportedModelsSearch('');
      setSelectedFetchedModels([]);
      setShowNotAddedModelsOnly(false);
      setApplyPatternFilter(false);
      setSupportedModelsExpanded(false);
    }
  }, [open]);

  useEffect(() => {
    if (!open) return;

    const timer = setTimeout(() => {
      const target = providerRefs.current[selectedProvider];
      const container = providerListRef.current;
      if (target && container) {
        const isHorizontal =
          (window.getComputedStyle(container).overflowX === 'auto' || window.getComputedStyle(container).overflowX === 'scroll') &&
          container.scrollWidth > container.clientWidth;
        if (isHorizontal) {
          const targetCenter = target.offsetLeft + target.clientWidth / 2;
          container.scrollLeft = Math.max(0, targetCenter - container.clientWidth / 2);
        } else {
          const targetCenter = target.offsetTop + target.clientHeight / 2;
          container.scrollTop = Math.max(0, targetCenter - container.clientHeight / 2);
        }
      }
    }, 100);

    return () => clearTimeout(timer);
  }, [open, isEdit, selectedProvider]);

  const setProviderListRef = useCallback(
    (el: HTMLDivElement | null) => {
      providerListRef.current = el;
      providerHorizontalScrollRef(el);
    },
    [providerHorizontalScrollRef]
  );

  // Auto-open supported models panel when showModelsPanel is true
  useEffect(() => {
    if (open && showModelsPanel && initialRow && initialRow.supportedModels.length > 0) {
      setShowConfiguredModelsPanel(false);
      setShowSupportedModelsPanel(true);
      setShowFetchedModelsPanel(false);
      setShowApiKeysPanel(false);
    }
  }, [open, showModelsPanel, initialRow]);

  // Get available providers (excluding fake types)
  const availableProviders = useMemo(
    () =>
      Object.entries(PROVIDER_CONFIGS)
        .filter(([, config]) => {
          // Filter out providers that only have fake types
          const nonFakeTypes = config.channelTypes.filter((t) => !t.endsWith('_fake'));
          return nonFakeTypes.length > 0;
        })
        .map(([key, config]) => ({
          key,
          label: t(`channels.providers.${key}`),
          icon: config.icon,
          channelTypes: config.channelTypes.filter((t) => !t.endsWith('_fake')),
        })),
    [t]
  );

  // Get available API formats for selected provider
  const availableApiFormats = useMemo(() => {
    return getApiFormatsForProvider(selectedProvider);
  }, [selectedProvider]);

  const availableApiFormatOptions = useMemo<ApiFormatOption[]>(() => {
    if (selectedProvider !== 'openai') return availableApiFormats;

    const options: ApiFormatOption[] = [];
    for (const format of availableApiFormats) {
      options.push(format);

    }
    return options;
  }, [availableApiFormats, selectedProvider]);

  const selectedApiFormatOption: ApiFormatOption =
    selectedApiFormat;

  const getApiFormatLabel = useCallback(
    (format: ApiFormat) => {
      return t(`channels.dialogs.fields.apiFormat.formats.${format}`);
    },
    [t]
  );

  const getApiFormatOptionLabel = useCallback(
    (format: ApiFormatOption) => {
      if (format === OPENAI_RESPONSES_WEBSOCKET) {
        return t('channels.dialogs.fields.apiFormat.formats.openai/responses_websocket');
      }
      return getApiFormatLabel(format);
    },
    [getApiFormatLabel, t]
  );

  // Determine the actual channel type based on provider and API format
  const derivedChannelType = useMemo(() => {
    if (selectedApiFormat === OPENAI_IMAGE_GENERATION) {
      return getBaseChannelTypeForProvider(selectedProvider) || 'openai';
    }

    // If gemini/contents is selected and vertex checkbox is checked, use gemini_vertex
    if (selectedApiFormat === 'gemini/contents' && useGeminiVertex) {
      return 'gemini_vertex';
    }

    // If anthropic/messages is selected, check which variant is selected
    if (selectedApiFormat === 'anthropic/messages') {
      if (useAnthropicAws) return 'anthropic_aws';
      if (useKimiCoding) return 'moonshot_coding';
    }

    return getChannelTypeForApiFormat(selectedProvider, selectedApiFormat) || 'openai';
  }, [selectedProvider, selectedApiFormat, useGeminiVertex, useAnthropicAws, useKimiCoding]);

  const formSchema = isEdit ? updateChannelInputSchema : createChannelInputSchema;

  const form = useForm<z.input<typeof formSchema>, unknown, z.output<typeof formSchema>>({
    resolver: zodResolver(formSchema),
    defaultValues:
      isEdit && currentRow
        ? {
            type: currentRow.type,
            baseURL: getInitialBaseURL(currentRow),
            websiteURL: currentRow.websiteURL ?? '',
            endpoints: currentRow.endpoints ?? [],
            name: currentRow.name,
            policies: currentRow.policies ?? { stream: 'unlimited' },
            supportedModels: currentRow.supportedModels,
            autoSyncSupportedModels: currentRow.autoSyncSupportedModels,
            autoSyncModelPattern: currentRow.autoSyncModelPattern || '',
            defaultTestModel: currentRow.defaultTestModel,
            tags: currentRow.tags || [],
            remark: currentRow.remark || '',
            credentials: {
              // OAuth 类型的凭据存储在 apiKey 字段，不放入 apiKeys
              apiKey: currentRow.credentials?.apiKey || undefined,
              apiKeys: currentRow.credentials?.apiKeys || [],
              vertexApiKeys: currentRow.type === 'gemini_vertex' ? (currentRow.credentials?.vertexApiKeys ?? undefined) : [],
              managementApiKey: currentRow.credentials?.managementApiKey || undefined,
              gcp: {
                region: currentRow.credentials?.gcp?.region || '',
                projectID: currentRow.credentials?.gcp?.projectID || '',
                jsonData: currentRow.credentials?.gcp?.jsonData || '',
              },
            },
            settings: currentRow.settings ?? undefined,
          }
        : duplicateFromRow
          ? {
              type: duplicateFromRow.type,
              baseURL: getInitialBaseURL(duplicateFromRow),
              websiteURL: duplicateFromRow.websiteURL ?? '',
              endpoints: duplicateFromRow.endpoints ?? [],
              name: duplicateFromRow.name,
              policies: duplicateFromRow.policies ?? { stream: 'unlimited' },
              supportedModels: duplicateFromRow.supportedModels,
              autoSyncSupportedModels: duplicateFromRow.autoSyncSupportedModels,
              autoSyncModelPattern: duplicateFromRow.autoSyncModelPattern || '',
              defaultTestModel: duplicateFromRow.defaultTestModel,
              tags: duplicateFromRow.tags || [],
              remark: duplicateFromRow.remark || '',
              settings: duplicateFromRow.settings ?? undefined,
              credentials: {
                // OAuth 类型的凭据存储在 apiKey 字段，不放入 apiKeys
                apiKey: duplicateFromRow.credentials?.apiKey || undefined,
                apiKeys: duplicateFromRow.credentials?.apiKeys || [],
                vertexApiKeys: duplicateFromRow.type === 'gemini_vertex' ? (duplicateFromRow.credentials?.vertexApiKeys ?? undefined) : [],
                managementApiKey: duplicateFromRow.credentials?.managementApiKey || undefined,
                gcp: {
                  region: duplicateFromRow.credentials?.gcp?.region || '',
                  projectID: duplicateFromRow.credentials?.gcp?.projectID || '',
                  jsonData: duplicateFromRow.credentials?.gcp?.jsonData || '',
                },
              },
            }
          : {
              type: derivedChannelType,
              baseURL: getDefaultBaseURL(derivedChannelType),
              websiteURL: '',
              name: '',
              policies: { stream: 'unlimited' },
              credentials: {
                apiKeys: [],
                vertexApiKeys: isVertexChannelType(derivedChannelType) ? [emptyVertexAPIKey()] : [],
                managementApiKey: undefined,
                gcp: {
                  region: '',
                  projectID: '',
                  jsonData: '',
                },
              },
              supportedModels: [],
              defaultTestModel: '',
              tags: [],
              remark: '',
              settings: undefined,
            },
  });

  const vertexApiKeyFields = useFieldArray({ control: form.control, name: 'credentials.vertexApiKeys' });
  const apiKeys = form.watch('credentials.apiKeys');
  const apiKeysCount = useMemo(() => (apiKeys || []).filter((k) => k.trim().length > 0).length, [apiKeys]);
  const isSubmitting = createChannel.isPending || duplicateChannel.isPending || updateChannelSettings.isPending;

  const { data: disabledKeys = [], isFetching: isFetchingDisabledKeys } = useChannelDisabledAPIKeys(currentRow?.id || '', {
    enabled: isEdit && !!currentRow?.id && showApiKeysPanel,
  });

  const disabledKeySet = useMemo(() => new Set(disabledKeys.map((dk) => dk.key)), [disabledKeys]);

  // Keys that exist in the backend (used to hide disable/enable button for unsaved new keys)
  const savedAPIKeySet = useMemo(
    () => new Set((currentRow?.credentials?.apiKeys || []).filter((k) => k.trim().length > 0)),
    [currentRow?.credentials?.apiKeys]
  );

  const disableAPIKey = useDisableChannelAPIKey();
  const enableAPIKey = useEnableChannelAPIKey();

  useEffect(() => {
    if (!open || !isDuplicate || !duplicateFromRow) return;
    if (!allChannelNamesLoaded) return;
    if (hasAutoSetDuplicateNameRef.current) return;

    const currentName = form.getValues('name');
    if (currentName !== duplicateFromRow.name) {
      return;
    }

    const nextName = getNextDuplicateName(duplicateFromRow.name, new Set(allChannelNames));
    form.setValue('name', nextName);
    hasAutoSetDuplicateNameRef.current = true;
  }, [open, isDuplicate, duplicateFromRow, allChannelNamesLoaded, allChannelNames, form]);
  const selectedType = form.watch('type') as ChannelType | undefined;
  const isGeminiVertex = (selectedType || derivedChannelType) === 'gemini_vertex';
  const watchedAutoSync = form.watch('autoSyncSupportedModels');
  const watchedAutoSyncPattern = form.watch('autoSyncModelPattern');

  const activeChannelType = selectedType || derivedChannelType;
  const isCodexType = activeChannelType === 'codex';
  const isAntigravityType = activeChannelType === 'antigravity';
  const isClineType = activeChannelType === 'cline';
  const isClaudeCodeType = activeChannelType === 'claudecode';
  const isCopilotType = activeChannelType === 'github_copilot';
  const isXAISubscriptionType = activeChannelType === 'xai_subscription';
  const isZenmuxType = ['zenmux', 'zenmux_responses', 'zenmux_anthropic', 'zenmux_gemini', 'zenmux_video'].includes(activeChannelType);
  const isCommandCodeType = activeChannelType === 'commandcode' || activeChannelType === 'commandcode_anthropic';
  const isOllamaType = activeChannelType === 'ollama' || activeChannelType === 'ollama_anthropic';

  // OAuth providers cannot have their provider/API format changed during edit.
  // Derived from currentRow credentials so it stays stable across re-renders
  // and is not affected by mutable authMode state.
  const isOAuthChannel = useMemo(() => {
    if (!isEdit || !currentRow) return false;
    if (alwaysOAuthProviderKeys.includes(currentRow.type)) return true;
    if (currentRow.type === 'codex') return isOfficialCodexChannel(currentRow);
    if (currentRow.type === 'claudecode') return isOfficialClaudeCodeChannel(currentRow);
    return false;
  }, [isEdit, currentRow]);

  const wrapUnsupported = useCallback(
    (enabled: boolean, children: React.ReactNode, wrapperClassName: string) => {
      if (!enabled) return children;
      return (
        <Tooltip>
          <TooltipTrigger asChild>
            <span className={wrapperClassName}>{children}</span>
          </TooltipTrigger>
          <TooltipContent>
            <p>{t('channels.dialogs.fields.unsupported')}</p>
          </TooltipContent>
        </Tooltip>
      );
    },
    [t]
  );

  const baseURLPlaceholder = useMemo(() => {
    const currentType = selectedType || derivedChannelType;
    if (selectedApiFormat === OPENAI_RESPONSES && responsesTransport === 'websocket') {
      const websocketBaseURL = getResponsesWebSocketBaseURL(currentType);
      if (websocketBaseURL) {
        return websocketBaseURL;
      }
    }
    const defaultURL = getDefaultBaseURL(currentType);
    if (defaultURL) {
      return defaultURL;
    }
    return t('channels.dialogs.fields.baseURL.placeholder');
  }, [selectedType, derivedChannelType, selectedApiFormat, responsesTransport, t]);


  // Sync form type when provider or API format changes
  const handleProviderChange = useCallback(
    (provider: string) => {
      if (isOAuthChannel) return;
      if (isEdit && alwaysOAuthProviderKeys.includes(provider)) return;
      setSelectedProvider(provider);
      setAuthMode(isEdit && ['codex', 'claudecode'].includes(provider) ? 'third-party' : 'official');

      if (provider !== 'gemini') {
        setUseGeminiVertex(false);
      }
      if (provider !== 'anthropic') {
        setUseAnthropicAws(false);
      }
      if (provider !== 'moonshot') {
        setUseKimiCoding(false);
      }

      if (provider === 'codex') {
        setSelectedApiFormat(OPENAI_RESPONSES);
        form.setValue('type', 'codex');
        if (!isEdit) {
          const baseURL = getDefaultBaseURL('codex');
          if (baseURL) {
            form.setValue('baseURL', baseURL, { shouldDirty: true });
          }
          setFetchedModels([]);
          setUseFetchedModels(false);
        }
        return;
      }

      if (provider === 'xai_subscription') {
        setSelectedApiFormat(OPENAI_RESPONSES);
        setResponsesTransport('http');
        form.setValue('type', 'xai_subscription');
        if (!isEdit) {
          const baseURL = getDefaultBaseURL('xai_subscription');
          if (baseURL) {
            form.setValue('baseURL', baseURL, { shouldDirty: true });
          }
          setFetchedModels([]);
          setUseFetchedModels(false);
        }
        return;
      }

      if (provider === 'antigravity') {
        setSelectedApiFormat(GEMINI_CONTENTS);
        form.setValue('type', 'antigravity');
        if (!isEdit) {
          setFetchedModels([]);
          setUseFetchedModels(false);
          // Set default Base URL only if empty
          const baseURL = getDefaultBaseURL('antigravity');
          const currentURL = form.getValues('baseURL');
          if (baseURL && !isDuplicate && (!currentURL || currentURL === '')) {
            form.setValue('baseURL', baseURL);
          }
        }
        return;
      }

      const formats = getApiFormatsForProvider(provider);
      const currentFormat = selectedApiFormat;
      let newFormat = currentFormat;

      if (!formats.includes(currentFormat)) {
        newFormat = formats[0] || 'openai/chat_completions';
      }

      setSelectedApiFormat(newFormat);
      const newChannelType =
        newFormat === OPENAI_IMAGE_GENERATION
          ? getBaseChannelTypeForProvider(provider)
          : provider === 'gemini' && newFormat === 'gemini/contents' && useGeminiVertex
            ? 'gemini_vertex'
            : provider === 'anthropic' && newFormat === 'anthropic/messages' && useAnthropicAws
              ? 'anthropic_aws'
              : provider === 'moonshot' && newFormat === 'anthropic/messages' && useKimiCoding
                ? 'moonshot_coding'
                : getChannelTypeForApiFormat(provider, newFormat);
      if (newChannelType) {
        form.setValue('type', newChannelType);
        if (!isEdit) {
          if (!isDuplicate) {
            const baseURL = getDefaultBaseURL(newChannelType);
            if (baseURL) {
              form.resetField('baseURL', { defaultValue: baseURL });
            }
          }
          setFetchedModels([]);
          setUseFetchedModels(false);
        }
      }
    },
    [form, useGeminiVertex, useAnthropicAws, useKimiCoding, isDuplicate, isEdit, selectedApiFormat, isOAuthChannel]
  );

  const handleApiFormatChange = useCallback(
    (formatOption: ApiFormatOption) => {
      if (isOAuthChannel) return;
      if (selectedProvider === 'codex' || selectedProvider === 'antigravity') return;

      const format = formatOption === OPENAI_RESPONSES_WEBSOCKET ? OPENAI_RESPONSES : formatOption;
      const nextResponsesTransport = formatOption === OPENAI_RESPONSES_WEBSOCKET ? 'websocket' : 'http';

      setSelectedApiFormat(format);
      setResponsesTransport(nextResponsesTransport);

      // Reset vertex checkbox if not gemini/contents
      if (format !== 'gemini/contents') {
        setUseGeminiVertex(false);
      }
      // Reset anthropic/kimi checkboxes if not anthropic/messages
      if (format !== 'anthropic/messages') {
        setUseAnthropicAws(false);
        setUseKimiCoding(false);
      }

      if (format === OPENAI_IMAGE_GENERATION) {
        const imageChannelType = getBaseChannelTypeForProvider(selectedProvider);
        if (imageChannelType) {
          form.setValue('type', imageChannelType);
          if (!isEdit && !isDuplicate) {
            const baseURL = getDefaultBaseURL(imageChannelType);
            if (baseURL) {
              form.resetField('baseURL', { defaultValue: baseURL });
            }
          }
        }
        return;
      }

      const channelTypeFromFormat = getChannelTypeForApiFormat(selectedProvider, format);
      const newChannelType =
        format === 'gemini/contents' && useGeminiVertex
          ? 'gemini_vertex'
          : format === 'anthropic/messages' && useAnthropicAws
            ? 'anthropic_aws'
            : format === 'anthropic/messages' && useKimiCoding
              ? 'moonshot_coding'
              : channelTypeFromFormat;
      if (newChannelType) {
        form.setValue('type', newChannelType);

        if (!isEdit) {
          const baseURLFieldState = form.getFieldState('baseURL', form.formState);
          if (nextResponsesTransport === 'websocket') {
            const baseURL = getResponsesWebSocketBaseURL(newChannelType);
            if (baseURL) {
              form.resetField('baseURL', { defaultValue: baseURL });
            }
          } else if (!baseURLFieldState.isDirty && !isDuplicate) {
            const baseURL = getDefaultBaseURL(newChannelType);
            if (baseURL) {
              form.resetField('baseURL', { defaultValue: baseURL });
            }
          }
        }
      }
    },
    [selectedProvider, form, useGeminiVertex, useAnthropicAws, useKimiCoding, isDuplicate, isEdit, isOAuthChannel]
  );

  const handleResponsesTransportChange = useCallback((transport: ResponsesTransport) => {
    setResponsesTransport(transport);
  }, []);

  const handleGeminiVertexChange = useCallback(
    (checked: boolean) => {
      if (isOAuthChannel) return;
      setUseGeminiVertex(checked);

      if (selectedApiFormat === 'gemini/contents') {
        const newChannelType = checked ? 'gemini_vertex' : 'gemini';
        form.setValue('type', newChannelType);

        // Switching into Vertex mode needs a row to edit: an empty list would
        // reject the submit with no visible field to fill in.
        if (checked && !(form.getValues('credentials.vertexApiKeys') ?? []).length) {
          form.setValue('credentials.vertexApiKeys', [emptyVertexAPIKey()]);
        }

        if (!isEdit) {
          const baseURLFieldState = form.getFieldState('baseURL', form.formState);
          if (!baseURLFieldState.isDirty && !isDuplicate) {
            const baseURL = getDefaultBaseURL(newChannelType);
            if (baseURL) {
              form.resetField('baseURL', { defaultValue: baseURL });
            }
          }
        }
      }
    },
    [selectedApiFormat, form, isDuplicate, isEdit, isOAuthChannel]
  );

  const handleAnthropicAwsChange = useCallback(
    (checked: boolean) => {
      if (isOAuthChannel) return;
      setUseAnthropicAws(checked);

      if (selectedApiFormat === 'anthropic/messages') {
        const newChannelType = checked ? 'anthropic_aws' : 'anthropic';
        form.setValue('type', newChannelType);

        if (!isEdit) {
          const baseURLFieldState = form.getFieldState('baseURL', form.formState);
          if (!baseURLFieldState.isDirty && !isDuplicate) {
            const baseURL = getDefaultBaseURL(newChannelType);
            if (baseURL) {
              form.resetField('baseURL', { defaultValue: baseURL });
            }
          }
        }
      }
    },
    [selectedApiFormat, form, isDuplicate, isEdit, isOAuthChannel]
  );

  const handleKimiCodingChange = useCallback(
    (checked: boolean) => {
      if (isOAuthChannel) return;
      setUseKimiCoding(checked);

      if (selectedApiFormat === 'anthropic/messages') {
        const newChannelType = checked ? 'moonshot_coding' : 'moonshot_anthropic';
        form.setValue('type', newChannelType);

        if (!isEdit) {
          const baseURLFieldState = form.getFieldState('baseURL', form.formState);
          if (!baseURLFieldState.isDirty && !isDuplicate) {
            const baseURL = getDefaultBaseURL(newChannelType);
            if (baseURL) {
              form.resetField('baseURL', { defaultValue: baseURL });
            }
          }
        }
      }
    },
    [selectedApiFormat, form, isDuplicate, isEdit, isOAuthChannel]
  );

  // The Command Code quota cookie only exists on Command Code channel types.
  // Reset the reveal state as soon as the active type leaves the two types.
  useEffect(() => {
    if (!isCommandCodeType) {
      setShowCommandCodeAuthCookie(false);
    }
    if (!isOllamaType) {
      setShowOllamaAuthCookie(false);
    }
  }, [isCommandCodeType, isOllamaType]);

  useEffect(() => {
    if (isEdit || isDuplicate) return;

    if (!isCodexType) {
      resetCodexOAuth();
    }
    if (selectedProvider !== 'claudecode') {
      resetClaudecodeOAuth();
    }
    if (selectedProvider !== 'antigravity') {
      resetAntigravityOAuth();
    }
    if (selectedProvider !== 'xai_subscription') {
      resetXaiOAuth();
      setXaiSSOToken('');
    }

    const providerToChannelType: Partial<Record<string, ChannelType>> = {
      claudecode: authMode === 'official' ? 'claudecode' : undefined,
      codex: authMode === 'official' || authMode === 'auth-json' ? 'codex' : undefined,
      antigravity: 'antigravity',
      xai_subscription: 'xai_subscription',
    };

    const channelTypeForURL: ChannelType | undefined = providerToChannelType[selectedProvider];

    if (channelTypeForURL) {
      const baseURL = getDefaultBaseURL(channelTypeForURL);
      if (baseURL) {
        // Use setValue instead of resetField to avoid infinite loop
        const currentURL = form.getValues('baseURL');
        if (!currentURL || currentURL !== baseURL) {
          form.setValue('baseURL', baseURL);
        }
      }
    }
  }, [
    isEdit,
    isDuplicate,
    isCodexType,
    selectedProvider,
    authMode,
    form,
    resetCodexOAuth,
    resetClaudecodeOAuth,
    resetAntigravityOAuth,
    resetXaiOAuth,
  ]);

  const watchedDefaultTestModel = form.watch('defaultTestModel');
  useEffect(() => {
    if (supportedModels.length === 0) return;
    if (!isEdit && !isDuplicate) {
      if (!watchedDefaultTestModel || !supportedModels.includes(watchedDefaultTestModel)) {
        form.setValue('defaultTestModel', supportedModels[0]);
      }
      return;
    }
    if (watchedDefaultTestModel && !supportedModels.includes(watchedDefaultTestModel)) {
      form.setValue('defaultTestModel', supportedModels[0]);
    }
  }, [supportedModels, watchedDefaultTestModel, isEdit, isDuplicate, form]);

  const renderOAuthSection = useCallback(
    (oauth: ReturnType<typeof useOAuthFlow>, description: string) => (
      <div className='mt-3 space-y-2'>
        <div className='rounded-md border p-3'>
          <div className='flex flex-wrap items-center gap-2'>
            <Button type='button' variant='secondary' onClick={oauth.start} disabled={oauth.isStarting}>
              {oauth.isStarting ? t('channels.dialogs.oauth.buttons.starting') : t('channels.dialogs.oauth.buttons.startOAuth')}
            </Button>
            {oauth.authUrl && (
              <Button type='button' variant='ghost' onClick={() => window.open(oauth.authUrl || '', '_blank', 'noopener,noreferrer')}>
                {t('channels.dialogs.oauth.buttons.openOAuthLink')}
              </Button>
            )}
          </div>

          {oauth.authUrl && (
            <div className='mt-3 space-y-2'>
              <FormLabel className='text-sm font-medium'>{t('channels.dialogs.oauth.labels.authorizationUrl')}</FormLabel>
              <Textarea
                value={oauth.authUrl}
                readOnly
                className='min-h-[60px] resize-none font-mono text-xs'
                placeholder={t('channels.dialogs.oauth.placeholders.authorizationUrl')}
              />
            </div>
          )}

          <div className='mt-3 space-y-2'>
            <FormLabel className='text-sm font-medium'>{t('channels.dialogs.oauth.labels.callbackUrl')}</FormLabel>
            <Textarea
              value={oauth.callbackUrl}
              onChange={(e) => oauth.setCallbackUrl(e.target.value)}
              placeholder={t('channels.dialogs.oauth.placeholders.callbackUrl')}
              className='min-h-[80px] resize-y font-mono text-xs'
            />
            <Button type='button' onClick={oauth.exchange} disabled={oauth.isExchanging || !oauth.sessionId}>
              {oauth.isExchanging
                ? t('channels.dialogs.oauth.buttons.exchanging')
                : t('channels.dialogs.oauth.buttons.exchangeAndFillApiKey')}
            </Button>
          </div>

          <p className='mt-2 text-xs text-amber-600 dark:text-amber-400'>{t('channels.dialogs.proxy.oauthHint')}</p>
          <p className='text-muted-foreground mt-2 text-xs'>{description}</p>
        </div>
      </div>
    ),
    [t]
  );

  const applyXAISSO = useCallback(async () => {
    const token = xaiSSOToken.trim();
    if (!token) {
      toast.error(t('channels.dialogs.xaiSso.errors.required'));
      return;
    }

    setIsImportingXAISSO(true);
    try {
      const result = await xaiDecodeSSO({
        sso_token: token,
        ...(proxyConfig ? { proxy: proxyConfig } : {}),
      });
      form.setValue('credentials.apiKey', result.credentials, {
        shouldDirty: true,
        shouldTouch: true,
        shouldValidate: true,
      });
      setXaiSSOToken('');
      toast.success(t('channels.dialogs.xaiSso.messages.imported'));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : String(error));
    } finally {
      setIsImportingXAISSO(false);
    }
  }, [form, proxyConfig, t, xaiSSOToken]);

  const applyCodexAuthJSON = useCallback(async () => {
    try {
      const result = await codexDecodeAuthJSON({ auth_json: codexAuthJSONText });
      form.setValue('credentials.apiKey', result.credentials, {
        shouldDirty: true,
        shouldTouch: true,
        shouldValidate: true,
      });
      toast.success(t('channels.dialogs.codexAuthJson.messages.applied'));
    } catch {
      toast.error(t('channels.dialogs.codexAuthJson.messages.invalid'));
    }
  }, [codexAuthJSONText, form, t]);

  const onSubmit = async (values: z.infer<typeof formSchema>) => {
    if (values.websiteURL) {
      try {
        const url = new URL(values.websiteURL);
        if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) throw new Error('invalid');
      } catch {
        form.setError('websiteURL', { message: t('channels.website.invalid') });
        return;
      }
    }
    // Check if there are selected fetched models that haven't been confirmed
    if (selectedFetchedModels.length > 0) {
      toast.error(t('channels.messages.modelsNotConfirmed'));
      return;
    }

    try {
      if (values.credentials?.apiKeys) {
        values.credentials.apiKeys = [...new Set(values.credentials.apiKeys.map((key) => key.trim()).filter((key) => key.length > 0))];
      }

      const retryableStatusCodes = parseRetryableStatusCodesInput(retryableStatusCodesText);
      if (retryableStatusCodes === null) {
        toast.error(t('channels.dialogs.retryableStatusCodes.validation'));
        return;
      }

      const retryableErrorPatterns = parseRetryableErrorPatternsInput(retryableErrorPatternsText);
      if (retryableErrorPatterns === null) {
        toast.error(t('channels.dialogs.retryableErrorPatterns.validation'));
        return;
      }

      const maxSingleChannelRetriesParsed = parseMaxSingleChannelRetriesInput(maxSingleChannelRetriesText);
      if (!maxSingleChannelRetriesParsed.valid) {
        toast.error(t('channels.dialogs.maxSingleChannelRetries.validation'));
        return;
      }
      const maxSingleChannelRetries = maxSingleChannelRetriesParsed.value;

      const valuesForSubmit = isEdit
        ? values
        : {
            ...values,
            type: derivedChannelType,
          };

      // Vertex credentials are project-bound triples and are the only credential for
      // that channel type: a leftover singular apiKey would still be selected at
      // runtime and would not resolve to a project/region pair. Ordinary channels keep
      // their flat apiKeys and never carry the Vertex field.
      const submittedCredentials = valuesForSubmit.credentials;
      const vertexApiKeys = submittedCredentials?.vertexApiKeys;
      const credentials =
        valuesForSubmit.type === 'gemini_vertex'
          ? vertexApiKeys?.length
            ? {
                ...submittedCredentials,
                apiKey: undefined,
                apiKeys: undefined,
                vertexApiKeys: vertexApiKeys.map((credential) => ({
                  projectID: credential.projectID.trim(),
                  apiKey: credential.apiKey.trim(),
                  region: credential.region.trim() || 'global',
                })),
              }
            : { ...submittedCredentials, vertexApiKeys: undefined }
          : { ...submittedCredentials, vertexApiKeys: undefined };

      // A third-party Codex channel authenticates with a plain API key: reject an
      // empty or inherited OAuth-shaped credential instead of saving it.
      const codexThirdPartyCredentialError = getCodexThirdPartyCredentialError({
        type: values.type || derivedChannelType,
        authMode,
        apiKeys: values.credentials?.apiKeys,
      });
      if (codexThirdPartyCredentialError) {
        form.setError('credentials.apiKeys', {
          type: 'manual',
          message: t(codexThirdPartyCredentialError),
        });
        return;
      }

      const dataWithModels = {
        ...valuesForSubmit,
        supportedModels,
        manualModels,
        credentials: credentials
          ? getCodexThirdPartySubmitCredentials(credentials, {
               type: valuesForSubmit.type,
               authMode,
             })
          : credentials,
      };
      // The Command Code / Ollama quota cookie is a browser-session credential
      // that only belongs on its own channel type. Never let a
      // duplicate/type-switch flow attach it to an unrelated channel type.
      // Clearing it explicitly sends providerQuota: null so the backend
      // removes the stored cookie.
      const isCommandCodeSubmit =
        valuesForSubmit.type === 'commandcode' || valuesForSubmit.type === 'commandcode_anthropic';
      const commandCodeAuthCookie = isCommandCodeSubmit
        ? values.settings?.providerQuota?.commandCode?.authCookie?.trim()
        : undefined;
      const isOllamaSubmit =
        valuesForSubmit.type === 'ollama' || valuesForSubmit.type === 'ollama_anthropic';
      const ollamaAuthCookie = isOllamaSubmit
        ? values.settings?.providerQuota?.ollama?.authCookie?.trim()
        : undefined;
      const settingsForSubmit = values.settings
        ? {
            ...values.settings,
            ...((isCommandCodeSubmit && commandCodeAuthCookie) || (isOllamaSubmit && ollamaAuthCookie)
              ? {}
              : { providerQuota: null }),
          }
        : undefined;

      const shouldUseProtocolDefaultBaseURL =
        (isCodexType && (authMode === 'official' || authMode === 'auth-json')) ||
        (isClaudeCodeType && authMode === 'official' && !isDuplicate) ||
        isXAISubscriptionType;
      if (shouldUseProtocolDefaultBaseURL) {
        const currentType = selectedType || derivedChannelType;
        const baseURL = getDefaultBaseURL(currentType);
        if (baseURL) {
          dataWithModels.baseURL = baseURL;
        }
      }

      // Codex always saves an HTTP(S) Base URL; its WebSocket endpoint is derived from it.
      const requiredResponsesTransport: ResponsesTransport = isCodexType ? 'http' : responsesTransport;
      if (
        (isCodexType || selectedApiFormat === OPENAI_RESPONSES) &&
        !baseURLMatchesResponsesTransport(dataWithModels.baseURL, requiredResponsesTransport)
      ) {
        form.setError('baseURL', {
          type: 'manual',
          message: t(getResponsesTransportBaseURLError(requiredResponsesTransport)),
        });
        return;
      }

      if (selectedApiFormat === OPENAI_RESPONSES) {
        dataWithModels.baseURL = dataWithModels.baseURL?.replace(/^ws:/, 'http:').replace(/^wss:/, 'https:');
        const endpoints = [...(dataWithModels.endpoints ?? [])];
        const index = endpoints.findIndex((ep) => ep.apiFormat === OPENAI_RESPONSES);
        const endpoint = { ...(index >= 0 ? endpoints[index] : { apiFormat: OPENAI_RESPONSES }), supportsWebSockets: responsesTransport === 'websocket', transport: '' as const };
        if (index >= 0) endpoints[index] = endpoint; else endpoints.push(endpoint);
        dataWithModels.endpoints = endpoints;
      }

      const codexModelProtocols =
        isCodexType && initialRow ? translateLegacyCodexModelProtocols(initialRow, initialRow.settings?.modelProtocols) : undefined;

      if (isEdit && currentRow) {
        const existingModelProtocols = currentRow.settings?.modelProtocols;
        const shouldUpdateModelProtocols =
          selectedApiFormat === 'zenmux/video' ||
          existingModelProtocols?.some((protocol) => protocol.apiFormats.includes('zenmux/video')) === true;
        const settingsPatch: Partial<ChannelSettings> = {
          upstreamAddresses: values.settings?.upstreamAddresses,
          addressFailover: values.settings?.addressFailover,
          healthGateFailureThreshold: values.settings?.healthGateFailureThreshold ?? null,
          passThroughUserAgent,
          passThroughBody,
          forceOneHourPromptCache: values.settings?.forceOneHourPromptCache,
          retryableStatusCodes,
          retryableErrorPatterns,
          primaryApiFormat: selectedApiFormat === OPENAI_IMAGE_GENERATION ? OPENAI_IMAGE_GENERATION : '',
          maxSingleChannelRetries,
          apiKeyStrategy,
          apiKeyRoundRobinSwitchAfter:
            apiKeyStrategy === 'round_robin' || apiKeyStrategy === 'round_robin_success'
              ? apiKeyRoundRobinSwitchAfter
              : null,

          // Cookie edits (including clearing the saved cookie) travel through
          // the settings patch; mergeChannelSettingsForUpdate preserves the
          // field when the patch omits it and carries the null clear through.
          providerQuota: settingsForSubmit?.providerQuota,
          ...quotaRoutingModeSettingsPatch(quotaRoutingMode),
          ...(shouldUpdateModelProtocols
            ? { modelProtocols: getModelProtocolsForApiFormat(selectedApiFormat, supportedModels, existingModelProtocols) }
            : {}),
          ...(codexModelProtocols ? { modelProtocols: codexModelProtocols } : {}),
        };

        const updateInput = {
          ...dataWithModels,
          ...(isOAuthChannel ? { type: currentRow.type } : {}),
        } as z.infer<typeof updateChannelInputSchema>;
        delete updateInput.settings;

        const finalChannelType = updateInput.type || currentRow.type;
        const keepsManagementApiKey = [
          'zenmux',
          'zenmux_responses',
          'zenmux_anthropic',
          'zenmux_gemini',
          'zenmux_video',
        ].includes(finalChannelType);
        if (!keepsManagementApiKey && updateInput.credentials) {
          delete updateInput.credentials.managementApiKey;
        }

        const apiKey = values.credentials?.apiKey || '';
        const hasApiKey = apiKey.trim().length > 0;
        const apiKeys = values.credentials?.apiKeys || [];
        const hasApiKeys = apiKeys.length > 0 && apiKeys.some((k) => k.trim() !== '');
        const hasManagementApiKey = (values.credentials?.managementApiKey || '').trim().length > 0;
        const hasGcpCredentials =
          values.credentials?.gcp?.region &&
          values.credentials.gcp.region.trim() !== '' &&
          values.credentials?.gcp?.projectID &&
          values.credentials.gcp.projectID.trim() !== '' &&
          values.credentials?.gcp?.jsonData &&
          values.credentials.gcp.jsonData.trim() !== '';
        const hasVertexApiKeys =
          values.credentials?.vertexApiKeys && values.credentials.vertexApiKeys.some((binding) => binding.apiKey.trim() !== '');

        if (!hasApiKey && !hasApiKeys && !hasManagementApiKey && !hasGcpCredentials && !hasVertexApiKeys) {
          delete updateInput.credentials;
        }

        await updateChannelSettings.mutateAsync({
          id: currentRow.id,
          input: updateInput,
          patch: settingsPatch,
        });
        if (values.credentials?.apiKeys?.some((key) => !currentRow.credentials?.apiKeys?.includes(key)))
          refreshUpstream.mutate({ channelIds: [currentRow.id], includeDisabled: true });
        toast.success(t('channels.messages.updateSuccess'));
      } else {
        const proxyConfig = {
          type: proxyType as 'disabled' | 'environment' | 'url',
          ...(proxyType === ProxyType.URL && {
            url: proxyUrl,
            username: proxyUsername || undefined,
            password: proxyPassword || undefined,
          }),
        };

        const nextSettings = mergeChannelSettingsForUpdate(settingsForSubmit, {
          proxy: proxyConfig,
          passThroughUserAgent,
          passThroughBody,
          forceOneHourPromptCache: values.settings?.forceOneHourPromptCache,
          retryableStatusCodes,
          retryableErrorPatterns,
          primaryApiFormat: selectedApiFormat === OPENAI_IMAGE_GENERATION ? OPENAI_IMAGE_GENERATION : '',
          maxSingleChannelRetries,

          ...quotaRoutingModeSettingsPatch(quotaRoutingMode),
          apiKeyStrategy,
          apiKeyRoundRobinSwitchAfter:
            apiKeyStrategy === 'round_robin' || apiKeyStrategy === 'round_robin_success'
              ? apiKeyRoundRobinSwitchAfter
              : null,
          ...(selectedApiFormat === 'zenmux/video' ||
          settingsForSubmit?.modelProtocols?.some((protocol) => protocol.apiFormats.includes('zenmux/video'))
            ? {
                modelProtocols: getModelProtocolsForApiFormat(selectedApiFormat, supportedModels, settingsForSubmit?.modelProtocols),
              }
            : {}),
          ...(codexModelProtocols ? { modelProtocols: codexModelProtocols } : {}),
        });

        const createInput = {
          ...createChannelInputSchema.parse(dataWithModels),
          settings: nextSettings,
        } as z.infer<typeof createChannelInputSchema>;

        if (isDuplicate && duplicateFromRow) {
          await duplicateChannel.mutateAsync({
            sourceID: duplicateFromRow.id,
            input: createInput,
          });
        } else {
          const created = await createChannel.mutateAsync(createInput);
          if (createInput.credentials.apiKeys?.length) refreshUpstream.mutate({ channelIds: [created.id], includeDisabled: true });
        }

        // Auto-save proxy preset (preserve existing name if available)
        if (hasSystemScope('write_settings') && proxyType === ProxyType.URL && proxyUrl) {
          const existingPreset = proxyPresets.find((p) => p.url === proxyUrl);
          saveProxyPreset.mutate({
            name: existingPreset?.name,
            url: proxyUrl,
            username: proxyUsername || undefined,
            password: proxyPassword || undefined,
          });
        }
      }

      form.reset();
      setSupportedModels([]);
      setManualModels([]);
      onOpenChange(false);
    } catch (_error) {
      void _error;
    }
  };

  const addModel = () => {
    if (newModel.trim() && !supportedModels.includes(newModel.trim())) {
      setSupportedModels([...supportedModels, newModel.trim()]);
      setManualModels([...manualModels, newModel.trim()]);
      setNewModel('');
    }
  };

  const batchAddModels = useCallback(() => {
    const raw = newModel.trim();
    if (!raw) return;

    const models = raw
      .split(/[,，]+/)
      .map((m) => m.trim())
      .filter((m) => m.length > 0);

    if (models.length === 0) {
      setNewModel('');
      return;
    }

    setSupportedModels((prev) => {
      const combinedModels = new Set([...prev, ...models]);
      if (combinedModels.size === prev.length) return prev;
      return [...combinedModels];
    });
    setManualModels((prev) => {
      // Only add models that are NOT already in supportedModels
      const newModels = models.filter((m) => !supportedModels.includes(m));
      const combinedModels = new Set([...prev, ...newModels]);
      if (combinedModels.size === prev.length) return prev;
      return [...combinedModels];
    });
    setNewModel('');
  }, [newModel, supportedModels]);

  const removeModel = (model: string) => {
    setSupportedModels(supportedModels.filter((m) => m !== model));
    setManualModels(manualModels.filter((m) => m !== model));
  };

  const isModelManual = (model: string): boolean => {
    return manualModels.includes(model);
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      addModel();
    }
  };

  const toggleDefaultModel = (model: string) => {
    setSelectedDefaultModels((prev) => (prev.includes(model) ? prev.filter((m) => m !== model) : [...prev, model]));
  };

  const addSelectedDefaultModels = () => {
    const newModels = selectedDefaultModels.filter((model) => !supportedModels.includes(model));
    if (newModels.length > 0) {
      setSupportedModels((prev) => [...prev, ...newModels]);
      setSelectedDefaultModels([]);
    }
  };

  const handleClearAllSupportedModels = () => {
    setSupportedModels([]);
    setManualModels([]);
  };

  const handleFetchModels = useCallback(async () => {
    const channelType = form.getValues('type');
    const baseURL = form.getValues('baseURL');
    const apiKeys = form.getValues('credentials.apiKeys');
    const oauthApiKey = form.getValues('credentials.apiKey');
    const vertexApiKeys = channelType === 'gemini_vertex' ? form.getValues('credentials.vertexApiKeys') : undefined;
    const selectedVertexCredential = vertexApiKeys?.find(
      (credential) =>
        credential.apiKey.trim().length > 0 &&
        credential.projectID.trim().length > 0 &&
        (credential.region ?? 'global').trim().length > 0,
    );

    if (!channelType || !baseURL) {
      return;
    }

    try {
      // For OAuth-based providers (like Copilot), prefer oauthApiKey first
      let firstApiKey = '';
      if (channelType === 'gemini_vertex') {
        firstApiKey = selectedVertexCredential?.apiKey.trim() || '';
      } else {
        firstApiKey = getFetchModelsApiKey({
          type: channelType,
          authMode,
          oauthApiKey,
          apiKeys,
        });
      }

      // Fall back to Vertex API keys if neither OAuth nor flat apiKeys are present
      const result = await fetchModels.mutateAsync({
        channelType,
        baseURL,
        apiKey: firstApiKey || undefined,
        projectID: selectedVertexCredential?.projectID.trim() || undefined,
        region: selectedVertexCredential?.region?.trim() || undefined,
        channelID: isEdit ? currentRow?.id : undefined,
      });

      if (result.error) {
        toast.error(result.error);
        return;
      }

      const models = result.models.map((m) => m.id);
      if (models?.length) {
        setFetchedModels(models);
        setUseFetchedModels(true);
        setShowConfiguredModelsPanel(false);
        setShowFetchedModelsPanel(true);
        setShowSupportedModelsPanel(false);
        setShowApiKeysPanel(false);
        setSelectedFetchedModels([]);
        setFetchedModelsSearch('');
        setShowNotAddedModelsOnly(false);
        setApplyPatternFilter(false);
      }
    } catch (_error) {
      // Error is already handled by the mutation
    }
  }, [fetchModels, form, isEdit, currentRow, authMode]);

  const handleSyncNow = useCallback(async () => {
    if (!currentRow) return [];
    if (patternError) {
      toast.error(patternError);
      return [];
    }

    const channelId = currentRow.id;
    const formPattern = form.getValues('autoSyncModelPattern') || '';
    const result = await syncChannelModels.mutateAsync({
      channelID: channelId,
      pattern: formPattern.trim() ? formPattern : undefined,
    });

    // Sync is authoritative for both lists; refreshing only supportedModels
    // leaves manualModels stale and makes the header count drift from the badges.
    setSupportedModels(result.supportedModels || []);
    setManualModels(result.manualModels || []);
    return result.supportedModels || [];
  }, [currentRow, form, patternError, syncChannelModels]);

  const canFetchModels = () => {
    const baseURL = form.watch('baseURL');
    const apiKeys = form.watch('credentials.apiKeys');
    const hasApiKey = apiKeys?.some((key) => key.trim().length > 0);
    const vertexApiKeys = form.watch('credentials.vertexApiKeys');
    const hasVertexApiKey = vertexApiKeys?.some((credential) => credential.apiKey.trim().length > 0);

    if (isCodexType || isAntigravityType || isClineType || isXAISubscriptionType) {
      return !!baseURL;
    }

    if (isCopilotType) {
      const oauthApiKey = form.watch('credentials.apiKey');
      const hasOAuthToken = !!parseOauthToken(oauthApiKey || '');
      return !!baseURL && hasOAuthToken;
    }

    if (isEdit) {
      return !!baseURL;
    }

    return !!baseURL && (isGeminiVertex ? hasVertexApiKey : hasApiKey);
  };
  // Memoize quick models to avoid re-evaluating on every render
  const currentType = form.watch('type');
  const quickModels = useMemo(() => {
    if (useFetchedModels || !currentType) return [];
    return getDefaultModels(currentType);
  }, [currentType, useFetchedModels]);

  // Filter suggestions only; the complete fetched list still drives sync and deprecation checks.
  const filteredFetchedModels = useMemo(() => {
    let models = fetchedModels.filter(isSuggestedModel);
    if (showNotAddedModelsOnly) {
      models = models.filter((model) => !supportedModels.includes(model));
    }
    if (applyPatternFilter && watchedAutoSyncPattern && !patternError) {
      models = models.filter((model) => matchesModelPattern(model, watchedAutoSyncPattern));
    }
    if (debouncedFetchedModelsSearch.trim()) {
      const search = debouncedFetchedModelsSearch.toLowerCase();
      models = models.filter((model) => model.toLowerCase().includes(search));
    }
    return models;
  }, [
    fetchedModels,
    debouncedFetchedModelsSearch,
    showNotAddedModelsOnly,
    supportedModels,
    applyPatternFilter,
    watchedAutoSyncPattern,
    patternError,
  ]);

  // Toggle selection for fetched model
  const toggleFetchedModelSelection = useCallback((model: string) => {
    setSelectedFetchedModels((prev) => (prev.includes(model) ? prev.filter((m) => m !== model) : [...prev, model]));
  }, []);

  // Select all filtered models
  const selectAllFilteredModels = useCallback(() => {
    setSelectedFetchedModels(filteredFetchedModels);
  }, [filteredFetchedModels]);

  // Deselect all
  const deselectAllFetchedModels = useCallback(() => {
    setSelectedFetchedModels([]);
  }, []);

  // Add or remove selected fetched models to supported models
  const addSelectedFetchedModels = useCallback(() => {
    const modelsToRemove: string[] = [];

    setSupportedModels((prev) => {
      const modelsToAdd: string[] = [];

      selectedFetchedModels.forEach((model) => {
        if (prev.includes(model)) {
          modelsToRemove.push(model);
        } else {
          modelsToAdd.push(model);
        }
      });

      const afterRemoval = prev.filter((m) => !modelsToRemove.includes(m));
      return [...afterRemoval, ...modelsToAdd];
    });

    // Remove toggled-off models from manualModels
    if (modelsToRemove.length > 0) {
      setManualModels((prev) => prev.filter((m) => !modelsToRemove.includes(m)));
    }

    setSelectedFetchedModels([]);
  }, [selectedFetchedModels]);

  // Close panel handler
  const closeFetchedModelsPanel = useCallback(() => {
    setShowFetchedModelsPanel(false);
    setSelectedFetchedModels([]);
    setFetchedModelsSearch('');
    setShowNotAddedModelsOnly(false);
    setApplyPatternFilter(false);
  }, []);

  // Close supported models panel handler
  const closeSupportedModelsPanel = useCallback(() => {
    setShowSupportedModelsPanel(false);
  }, []);

  const closeApiKeysPanel = useCallback(() => {
    setShowApiKeysPanel(false);
    setApiKeysSearch('');
    setSelectedKeysToRemove(new Set());
    setConfirmRemoveSelectedOpen(false);
    setConfirmRemoveKey(null);
    setConfirmDisableKey(null);
  }, []);

  const removeApiKeys = useCallback(
    (keysToRemove: string[]) => {
      const currentKeys = form.getValues('credentials.apiKeys') || [];
      const keysToRemoveSet = new Set(keysToRemove.map((key) => key.trim()));
      const validNextKeys = currentKeys.map((key) => key.trim()).filter((key) => key.length > 0 && !keysToRemoveSet.has(key));
      if (validNextKeys.length === 0) {
        toast.error(t('channels.dialogs.fields.apiKey.mustKeepOne'));
        setConfirmRemoveSelectedOpen(false);
        setConfirmRemoveKey(null);
        return;
      }
      form.setValue('credentials.apiKeys', validNextKeys, { shouldDirty: true, shouldTouch: true });
      setSelectedKeysToRemove(new Set());
      setConfirmRemoveSelectedOpen(false);
      setConfirmRemoveKey(null);
    },
    [form, t]
  );

  // Remove deprecated models (models in supportedModels but not in fetchedModels and not manual)
  const removeDeprecatedModels = useCallback(() => {
    const fetchedModelsSet = new Set(fetchedModels);
    const manualModelsSet = new Set(manualModels);
    // Deprecated = not fetched AND not manual
    const deprecatedModels = supportedModels.filter((model) => !fetchedModelsSet.has(model) && !manualModelsSet.has(model));
    // Keep fetched models and manual models in supportedModels
    setSupportedModels((prev) => prev.filter((model) => fetchedModelsSet.has(model) || manualModelsSet.has(model)));
    // Remove deprecated models from manualModels (should be none, but for consistency)
    setManualModels((prev) => prev.filter((model) => !deprecatedModels.includes(model)));
  }, [fetchedModels, supportedModels, manualModels]);

  // Count of deprecated models
  const deprecatedModelsCount = useMemo(() => {
    const fetchedModelsSet = new Set(fetchedModels);
    const manualModelsSet = new Set(manualModels);
    // Count only models that are neither fetched nor manual
    return supportedModels.filter((model) => !fetchedModelsSet.has(model) && !manualModelsSet.has(model)).length;
  }, [supportedModels, fetchedModels, manualModels]);

  // Models to display (limited to MAX_MODELS_DISPLAY unless expanded)
  const displayedSupportedModels = useMemo(() => {
    if (supportedModels.length <= MAX_MODELS_DISPLAY) {
      return supportedModels;
    }
    return supportedModels.slice(0, MAX_MODELS_DISPLAY);
  }, [supportedModels]);

  // Filtered supported models based on search
  const filteredSupportedModels = useMemo(() => {
    if (!debouncedSupportedModelsSearch.trim()) {
      return supportedModels;
    }
    const search = debouncedSupportedModelsSearch.toLowerCase();
    return supportedModels.filter((model) => model.toLowerCase().includes(search));
  }, [supportedModels, debouncedSupportedModelsSearch]);

  // Virtual scrolling for fetched models
  const fetchedModelsVirtualizer = useVirtualizer({
    count: filteredFetchedModels.length,
    getScrollElement: () => fetchedModelsParentRef.current,
    estimateSize: () => 40,
    overscan: 5,
  });

  // Virtual scrolling for supported models
  const supportedModelsVirtualizer = useVirtualizer({
    count: filteredSupportedModels.length,
    getScrollElement: () => supportedModelsParentRef.current,
    estimateSize: () => 40,
    overscan: 5,
  });
  return {
    quotaRoutingSettings,
    quotaRoutingMode,
    setQuotaRoutingMode,
    maxSingleChannelRetriesText,
    setMaxSingleChannelRetriesText,
    apiKeyStrategy,
    setApiKeyStrategy,
    apiKeyRoundRobinSwitchAfter,
    setApiKeyRoundRobinSwitchAfter,
    vertexApiKeyFields,
    isGeminiVertex,
    isOllamaType,
    showOllamaAuthCookie,
    setShowOllamaAuthCookie,
    userAgentInheritLabel,
    passThroughInheritLabel,
    codexThirdPartyBaseURLRef,
    addModel,
    addSelectedDefaultModels,
    addSelectedFetchedModels,
    allTags,
    antigravityOAuth,
    apiKeys,
    apiKeysCount,
    apiKeysSearch,
    applyCodexAuthJSON,
    applyPatternFilter,
    applyXAISSO,
    authMode,
    availableApiFormatOptions,
    availableProviders,
    baseURLPlaceholder,
    batchAddModels,
    canFetchModels,
    claudecodeOAuth,
    closeApiKeysPanel,
    closeFetchedModelsPanel,
    closeSupportedModelsPanel,
    codexAuthJSONText,
    codexOAuth,
    confirmDisableKey,
    confirmRemoveKey,
    confirmRemoveSelectedOpen,
    deprecatedModelsCount,
    derivedChannelType,
    deselectAllFetchedModels,
    disableAPIKey,
    disabledKeySet,
    displayedSupportedModels,
    enableAPIKey,
    fetchModels,
    fetchedModels,
    fetchedModelsParentRef,
    fetchedModelsSearch,
    fetchedModelsVirtualizer,
    filteredFetchedModels,
    filteredSupportedModels,
    form,
    getApiFormatLabel,
    getApiFormatOptionLabel,
    handleAnthropicAwsChange,
    handleApiFormatChange,
    handleClearAllSupportedModels,
    handleFetchModels,
    handleGeminiVertexChange,
    handleKeyDown,
    handleKimiCodingChange,
    handleProviderChange,
    handleProxyPresetSelect,
    handleResponsesTransportChange,
    handleSyncNow,
    initialRow,
    isClaudeCodeType,
    isCodexType,
    isCommandCodeType,
    isCopilotType,
    isEdit,
    isFetchingDisabledKeys,
    isImportingXAISSO,
    isLoadingTags,
    isModelManual,
    isOAuthChannel,
    isSubmitting,
    isXAISubscriptionType,
    isZenmuxType,
    manualModels,
    modelPermissions,
    newModel,
    onSubmit,
    passThroughBody,
    passThroughUserAgent,
    patternError,
    providerRefs,
    proxyPassword,
    proxyPresets,
    proxyType,
    proxyUrl,
    proxyUsername,
    quickModels,
    removeApiKeys,
    removeDeprecatedModels,
    removeModel,
    renderOAuthSection,
    responsesTransport,
    retryableErrorPatternsText,
    retryableStatusCodesText,
    savedAPIKeySet,
    selectAllFilteredModels,
    selectedApiFormat,
    selectedApiFormatOption,
    selectedDefaultModels,
    selectedFetchedModels,
    selectedKeysToRemove,
    selectedProvider,
    selectedType,
    setApiKeysSearch,
    setApplyPatternFilter,
    setAuthMode,
    setCodexAuthJSONText,
    setConfirmDisableKey,
    setConfirmRemoveKey,
    setConfirmRemoveSelectedOpen,
    setFetchedModels,
    setFetchedModelsSearch,
    setManualModels,
    setNewModel,
    setPassThroughBody,
    setPassThroughUserAgent,
    setPatternError,
    setProviderListRef,
    setProxyPassword,
    setProxyType,
    setProxyUrl,
    setProxyUsername,
    setResponsesTransport,
    setRetryableErrorPatternsText,
    setRetryableStatusCodesText,
    setSelectedApiFormat,
    setSelectedDefaultModels,
    setSelectedFetchedModels,
    setSelectedKeysToRemove,
    setSelectedProvider,
    setShowApiKey,
    setShowApiKeysPanel,
    setShowClearAllPopover,
    setShowCommandCodeAuthCookie,
    setShowConfiguredModelsPanel,
    setShowFetchedModelsPanel,
    setShowNotAddedModelsOnly,
    setShowSupportedModelsPanel,
    setSupportedModels,
    setSupportedModelsExpanded,
    setSupportedModelsSearch,
    setUseAnthropicAws,
    setUseFetchedModels,
    setUseGeminiVertex,
    setUseKimiCoding,
    setXaiSSOToken,
    showApiKey,
    showApiKeysPanel,
    showClearAllPopover,
    showCommandCodeAuthCookie,
    showConfiguredModelsPanel,
    showFetchedModelsPanel,
    showNotAddedModelsOnly,
    showSupportedModelsPanel,
    supportedModels,
    supportedModelsExpanded,
    supportedModelsParentRef,
    supportedModelsSearch,
    supportedModelsVirtualizer,
    syncChannelModels,
    t,
    toggleDefaultModel,
    toggleFetchedModelSelection,
    updateChannelSettings,
    useAnthropicAws,
    useFetchedModels,
    useGeminiVertex,
    useKimiCoding,
    watchedAutoSync,
    watchedAutoSyncPattern,
    wrapUnsupported,
    xaiOAuth,
    xaiSSOToken,
  };
}
