import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { z } from 'zod';
import { useForm } from 'react-hook-form';
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
import { useProxyPresets, useSaveProxyPreset } from '@/features/system/data/system';
import { ProxyType } from '../components/channels-proxy-dialog';
import { antigravityOAuthExchange, antigravityOAuthStart } from '../data/antigravity';
import {
  useCreateChannel,
  useDuplicateChannel,
  useUpdateChannelSettings,
  useFetchModels,
  useAllChannelNames,
  useAllChannelTags,
  useChannelDisabledAPIKeys,
  useDisableChannelAPIKey,
  useEnableChannelAPIKey,
  useSyncChannelModels,
} from '../data/channels';
import { claudecodeOAuthExchange, claudecodeOAuthStart } from '../data/claudecode';
import { codexDecodeAuthJSON, codexOAuthExchange, codexOAuthStart } from '../data/codex';
import {
  getDefaultBaseURL,
  getDefaultModels,
  OPENAI_CHAT_COMPLETIONS,
  OPENAI_RESPONSES,
  OPENAI_IMAGE_GENERATION,
  GEMINI_CONTENTS,
} from '../data/config_channels';
import {
  PROVIDER_CONFIGS,
  getProviderFromChannelType,
  getApiFormatsForProvider,
  getChannelTypeForApiFormat,
  getBaseChannelTypeForProvider,
} from '../data/config_providers';
import { getModelProtocolsForApiFormat } from '../data/protocol-options';
import { Channel, ChannelType, ApiFormat, ChannelSettings, createChannelInputSchema, updateChannelInputSchema } from '../data/schema';
import { xaiDecodeSSO, xaiOAuthExchange, xaiOAuthStart } from '../data/xai';
import { ProxyConfig, useOAuthFlow } from '../hooks/use-oauth-flow';
import {
  ApiFormatOption,
  MAX_MODELS_DISPLAY,
  OPENAI_RESPONSES_WEBSOCKET,
  Props,
  ResponsesTransport,
  alwaysOAuthProviderKeys,
  baseURLMatchesResponsesTransport,
  extractCodexAuthJSONText,
  formatRetryableErrorPatterns,
  formatRetryableStatusCodes,
  getInitialApiFormatForRow,
  getNextDuplicateName,
  getResponsesTransportBaseURLError,
  getResponsesTransportFromChannel,
  getResponsesWebSocketBaseURL,
  isOfficialClaudeCodeChannel,
  isOfficialCodexChannel,
  parseRetryableErrorPatternsInput,
  parseRetryableStatusCodesInput,
} from '../utils/channel-dialog';
import { mergeChannelSettingsForUpdate } from '../utils/merge';
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
  const duplicateChannel = useDuplicateChannel();
  const updateChannelSettings = useUpdateChannelSettings();
  const fetchModels = useFetchModels();
  const syncChannelModels = useSyncChannelModels();
  const { data: allChannelNames = [], isSuccess: allChannelNamesLoaded } = useAllChannelNames({ enabled: open && isDuplicate });
  const { data: allTags = [], isLoading: isLoadingTags } = useAllChannelTags();
  const { data: proxyPresets = [] } = useProxyPresets();
  const saveProxyPreset = useSaveProxyPreset();
  const { hasSystemScope, modelPermissions } = usePermissions();
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
  const [showApiKey, setShowApiKey] = useState(false);
  const [showCommandCodeAuthCookie, setShowCommandCodeAuthCookie] = useState(false);
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
  const [retryableStatusCodesText, setRetryableStatusCodesText] = useState(() =>
    formatRetryableStatusCodes(initialRow?.settings?.retryableStatusCodes)
  );
  const [retryableErrorPatternsText, setRetryableErrorPatternsText] = useState(() =>
    formatRetryableErrorPatterns(initialRow?.settings?.retryableErrorPatterns)
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
      setXaiSSOToken('');
    }
  }, [open, resetCodexOAuth, resetClaudecodeOAuth, resetAntigravityOAuth, resetXaiOAuth]);

  useEffect(() => {
    if (!open) {
      setShowApiKey(false);
      setShowCommandCodeAuthCookie(false);
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

  // Sync manualModels when dialog opens with new initialRow
  useEffect(() => {
    if (open && initialRow) {
      setManualModels(initialRow.manualModels || []);
    }
  }, [open, initialRow]);

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
      if (format === OPENAI_RESPONSES) {
        options.push(OPENAI_RESPONSES_WEBSOCKET);
      }
    }
    return options;
  }, [availableApiFormats, selectedProvider]);

  const selectedApiFormatOption: ApiFormatOption =
    selectedApiFormat === OPENAI_RESPONSES && responsesTransport === 'websocket' ? OPENAI_RESPONSES_WEBSOCKET : selectedApiFormat;

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

  const form = useForm<z.infer<typeof formSchema>>({
    resolver: zodResolver(formSchema),
    defaultValues:
      isEdit && currentRow
        ? {
            type: currentRow.type,
            baseURL: currentRow.baseURL,
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
              baseURL: duplicateFromRow.baseURL,
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
              name: '',
              policies: { stream: 'unlimited' },
              credentials: {
                apiKeys: [],
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
  const watchedAutoSync = form.watch('autoSyncSupportedModels');
  const watchedAutoSyncPattern = form.watch('autoSyncModelPattern');

  const activeChannelType = selectedType || derivedChannelType;
  const isCodexType = activeChannelType === 'codex';
  const isAntigravityType = activeChannelType === 'antigravity';
  const isClineType = activeChannelType === 'cline';
  const isClaudeCodeType = activeChannelType === 'claudecode';
  const isCopilotType = activeChannelType === 'github_copilot';
  const isXAISubscriptionType = activeChannelType === 'xai_subscription';
  const isZenmuxType = ['zenmux', 'zenmux_responses', 'zenmux_anthropic', 'zenmux_gemini'].includes(activeChannelType);
  const isCommandCodeType = activeChannelType === 'commandcode' || activeChannelType === 'commandcode_anthropic';

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
          const baseURL = responsesTransport === 'websocket' ? getResponsesWebSocketBaseURL('codex') : getDefaultBaseURL('codex');
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
    [form, useGeminiVertex, useAnthropicAws, useKimiCoding, isDuplicate, isEdit, selectedApiFormat, isOAuthChannel, responsesTransport]
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

  const handleResponsesTransportChange = useCallback(
    (transport: ResponsesTransport) => {
      if (isOAuthChannel && selectedProvider !== 'codex') return;
      setResponsesTransport(transport);

      const channelType = selectedProvider === 'codex' ? 'codex' : selectedType || derivedChannelType;
      // Third-party Codex channels use custom endpoints — don't overwrite them on transport switch
      if (isCodexType && authMode === 'third-party') return;
      const baseURL = transport === 'websocket' ? getResponsesWebSocketBaseURL(channelType) : getDefaultBaseURL(channelType);
      if (baseURL) {
        form.setValue('baseURL', baseURL, { shouldDirty: true });
      }
    },
    [authMode, derivedChannelType, form, isCodexType, isOAuthChannel, selectedProvider, selectedType]
  );

  const handleGeminiVertexChange = useCallback(
    (checked: boolean) => {
      if (isOAuthChannel) return;
      setUseGeminiVertex(checked);

      if (selectedApiFormat === 'gemini/contents') {
        const newChannelType = checked ? 'gemini_vertex' : 'gemini';
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
  }, [isCommandCodeType]);

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
      const baseURL =
        channelTypeForURL === 'codex' && responsesTransport === 'websocket'
          ? getResponsesWebSocketBaseURL(channelTypeForURL)
          : getDefaultBaseURL(channelTypeForURL);
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
    responsesTransport,
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

      const valuesForSubmit = isEdit
        ? values
        : {
            ...values,
            type: derivedChannelType,
          };

      const dataWithModels = {
        ...valuesForSubmit,
        supportedModels,
        manualModels,
        credentials: valuesForSubmit.credentials,
      };
      // The Command Code quota cookie is a browser-session credential that only
      // belongs on Command Code channels. Never let a duplicate/type-switch
      // flow attach it to an unrelated channel type. Clearing it explicitly
      // sends providerQuota: null so the backend removes the stored cookie.
      const isCommandCodeSubmit = valuesForSubmit.type === 'commandcode' || valuesForSubmit.type === 'commandcode_anthropic';
      const commandCodeAuthCookie = isCommandCodeSubmit ? values.settings?.providerQuota?.commandCode?.authCookie?.trim() : undefined;
      const settingsForSubmit = values.settings
        ? {
            ...values.settings,
            ...(isCommandCodeSubmit && commandCodeAuthCookie ? {} : { providerQuota: null }),
          }
        : undefined;

      const shouldUseProtocolDefaultBaseURL =
        (isCodexType && (authMode === 'official' || authMode === 'auth-json')) ||
        (isClaudeCodeType && authMode === 'official' && !isDuplicate) ||
        isXAISubscriptionType;
      if (shouldUseProtocolDefaultBaseURL) {
        const currentType = selectedType || derivedChannelType;
        const baseURL =
          isCodexType && responsesTransport === 'websocket' ? getResponsesWebSocketBaseURL('codex') : getDefaultBaseURL(currentType);
        if (baseURL) {
          dataWithModels.baseURL = baseURL;
        }
      }

      if (selectedApiFormat === OPENAI_RESPONSES && !baseURLMatchesResponsesTransport(dataWithModels.baseURL, responsesTransport)) {
        form.setError('baseURL', {
          type: 'manual',
          message: t(getResponsesTransportBaseURLError(responsesTransport)),
        });
        return;
      }

      if (isEdit && currentRow) {
        const existingModelProtocols = currentRow.settings?.modelProtocols;
        const shouldUpdateModelProtocols =
          selectedApiFormat === 'zenmux/video' ||
          existingModelProtocols?.some((protocol) => protocol.apiFormats.includes('zenmux/video')) === true;
        const settingsPatch: Partial<ChannelSettings> = {
          healthGateFailureThreshold: values.settings?.healthGateFailureThreshold ?? null,
          passThroughUserAgent,
          passThroughBody,
          forceOneHourPromptCache: values.settings?.forceOneHourPromptCache,
          retryableStatusCodes,
          retryableErrorPatterns,
          primaryApiFormat: selectedApiFormat === OPENAI_IMAGE_GENERATION ? OPENAI_IMAGE_GENERATION : '',
          // Cookie edits (including clearing the saved cookie) travel through
          // the settings patch; mergeChannelSettingsForUpdate preserves the
          // field when the patch omits it and carries the null clear through.
          providerQuota: settingsForSubmit?.providerQuota,
          ...(shouldUpdateModelProtocols
            ? { modelProtocols: getModelProtocolsForApiFormat(selectedApiFormat, supportedModels, existingModelProtocols) }
            : {}),
        };

        const updateInput = {
          ...dataWithModels,
          ...(isOAuthChannel ? { type: currentRow.type } : {}),
        } as z.infer<typeof updateChannelInputSchema>;
        delete updateInput.settings;

        const finalChannelType = updateInput.type || currentRow.type;
        const keepsManagementApiKey = ['zenmux', 'zenmux_responses', 'zenmux_anthropic', 'zenmux_gemini'].includes(finalChannelType);
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

        if (!hasApiKey && !hasApiKeys && !hasManagementApiKey && !hasGcpCredentials) {
          delete updateInput.credentials;
        }

        await updateChannelSettings.mutateAsync({
          id: currentRow.id,
          input: updateInput,
          patch: settingsPatch,
        });
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
          ...(selectedApiFormat === 'zenmux/video' ||
          settingsForSubmit?.modelProtocols?.some((protocol) => protocol.apiFormats.includes('zenmux/video'))
            ? {
                modelProtocols: getModelProtocolsForApiFormat(selectedApiFormat, supportedModels, settingsForSubmit?.modelProtocols),
              }
            : {}),
        });

        const createInput = {
          ...(dataWithModels as z.infer<typeof createChannelInputSchema>),
          settings: nextSettings,
        } as z.infer<typeof createChannelInputSchema>;

        if (isDuplicate && duplicateFromRow) {
          await duplicateChannel.mutateAsync({
            sourceID: duplicateFromRow.id,
            input: createInput,
          });
        } else {
          await createChannel.mutateAsync(createInput);
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
  // Helper function to parse OAuth token from JSON string
  const parseOauthToken = (oauthApiKey: string): string => {
    if (!oauthApiKey) return '';
    try {
      const parsed = JSON.parse(oauthApiKey);
      if (parsed.access_token) {
        return parsed.access_token;
      }
      if (parsed.tokens?.access_token) {
        return parsed.tokens.access_token;
      }
    } catch {
      // Not JSON, use as-is
    }
    return oauthApiKey;
  };

  const handleFetchModels = useCallback(async () => {
    const channelType = form.getValues('type');
    const baseURL = form.getValues('baseURL');
    const apiKeys = form.getValues('credentials.apiKeys');
    const oauthApiKey = form.getValues('credentials.apiKey');

    if (!channelType || !baseURL) {
      return;
    }

    try {
      // For OAuth-based providers (like Copilot), prefer oauthApiKey first
      let firstApiKey = '';
      if (oauthApiKey) {
        // If it's OAuth JSON, send full JSON so backend detects isOAuthJSON
        if (oauthApiKey.trimStart().startsWith('{')) {
          firstApiKey = oauthApiKey;
        } else {
          const parsed = parseOauthToken(oauthApiKey || '');
          if (parsed) {
            firstApiKey = parsed;
          }
        }
      }

      // Fall back to apiKeys array if no OAuth token
      if (!firstApiKey && apiKeys?.length) {
        firstApiKey = apiKeys.find((key) => key.trim().length > 0)?.trim() || '';
      }

      const result = await fetchModels.mutateAsync({
        channelType,
        baseURL,
        apiKey: firstApiKey || undefined,
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
  }, [fetchModels, form, isEdit, currentRow]);

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

    setSupportedModels(result.supportedModels || []);
    return result.supportedModels || [];
  }, [currentRow, form, patternError, syncChannelModels]);

  const canFetchModels = () => {
    const baseURL = form.watch('baseURL');
    const apiKeys = form.watch('credentials.apiKeys');
    const hasApiKey = apiKeys?.some((key) => key.trim().length > 0);

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

    return !!baseURL && hasApiKey;
  };
  // Memoize quick models to avoid re-evaluating on every render
  const currentType = form.watch('type');
  const quickModels = useMemo(() => {
    if (useFetchedModels || !currentType) return [];
    return getDefaultModels(currentType);
  }, [currentType, useFetchedModels]);

  // Filtered fetched models based on search and filter
  const filteredFetchedModels = useMemo(() => {
    let models = fetchedModels;
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
