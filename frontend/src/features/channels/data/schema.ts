import { z } from 'zod';
import { channelModelContextSchema } from '@/features/models/data/context-settings';
import { pageInfoSchema } from '@/gql/pagination';

const decimal = z.union([z.string(), z.number()]).transform(String);
const balanceSchema = z.object({
  usageStart: z.string().nullish(),
  usageEnd: z.string().nullish(),
  kind: z.enum(['ACCOUNT', 'KEY', 'SUBSCRIPTION', 'QUOTA']),
  amount: decimal.nullish(),
  used: decimal.nullish(),
  limit: decimal.nullish(),
  unlimited: z.boolean(),
  currency: z.string(),
  unit: z.string(),
  accountRef: z.string(),
  expiresAt: z.string().nullish(),
});
const appliedSchema = z.object({
  value: decimal,
  source: z.enum(['MANUAL', 'UPSTREAM', 'CACHED', 'ESTIMATED']),
  provider: z.string(),
  observedAt: z.string().nullish(),
});
export const upstreamAccountSchema = z.object({
  balanceErrorCode: z.string(),
  ref: z.string(),
  name: z.string(),
  provider: z.enum(['NEW_API', 'SUB2_API', 'DEEPSEEK', 'MOONSHOT', 'OPENAI_BILLING']),
  accountId: z.number().nullish(),
  hasToken: z.boolean(),
  verifiedAt: z.string().nullish(),
  keyCount: z.number(),
  balance: balanceSchema.nullish(),
  balances: z.array(balanceSchema),
  autoCreated: z.boolean(),
  updatedAt: z.string().nullish(),
  complete: z.boolean(),
});
export type UpstreamAccount = z.infer<typeof upstreamAccountSchema>;
export const channelUpstreamStateSchema = z.object({
  channelId: z.string(),
  provider: z.enum(['UNKNOWN', 'NEW_API', 'SUB2_API', 'DEEPSEEK', 'MOONSHOT', 'OPENAI_BILLING']),
  accounts: z.array(upstreamAccountSchema),
  balanceSummary: z.object({
    balances: z.array(balanceSchema),
    accountCount: z.number(),
    knownAccountCount: z.number(),
    unassignedKeyCount: z.number(),
    complete: z.boolean(),
  }),
  keySessionReuse: z.boolean(),
  manualMultiplier: decimal,
  settings: z.object({ enabled: z.boolean(), costMode: z.enum(['MANUAL', 'UPSTREAM']), refreshIntervalMinutes: z.number() }),
  keys: z.array(
    z.object({
      keyRef: z.string(),
      label: z.string(),
      remark: z.string(),
      accountRef: z.string(),
      priority: z.number().int(),
      disabled: z.boolean(),
      upstreamMultiplier: decimal.nullish(),
      applied: appliedSchema,
      snapshot: z.object({
        provider: z.string(),
        balanceState: z.string(),
        rateState: z.string(),
        balanceErrorCode: z.string(),
        rateErrorCode: z.string(),
        balanceUpdatedAt: z.string().nullish(),
        balanceCheckedAt: z.string().nullish(),
        rateCheckedAt: z.string().nullish(),
        nextBalanceCheckAt: z.string().nullish(),
        nextRateCheckAt: z.string().nullish(),
        balances: z.array(balanceSchema),
        account: z.object({ accountId: z.number(), balance: balanceSchema, updatedAt: z.string() }).nullish(),
        rate: z
          .object({
            multiplier: decimal,
            group: z.string(),
            observedAt: z.string(),
            syncedAt: z.string(),
            validUntil: z.string().nullish(),
          })
          .nullish(),
      }),
    })
  ),
});
export type ChannelUpstreamState = z.infer<typeof channelUpstreamStateSchema>;
export type UpstreamKey = ChannelUpstreamState['keys'][number];
export type UpstreamBalance = z.infer<typeof balanceSchema>;
export type UpstreamInput = ChannelUpstreamState['settings'] & {
  manualMultiplier: string;
};

export const apiFormatSchema = z.enum([
  'openai/chat_completions',
  'openai/responses',
  'openai/responses-ws',
  'openai/image_generation',
  'openai/image_edit',
  'openai/image_variation',
  'openai/embeddings',
  'openai/video',
  'zenmux/video',
  'openai/moderations',
  'openai/alpha_search',
  'openai/decisions',
  'openai/audio_speech',
  'openai/audio_transcriptions',
  'openai/audio_translations',
  'anthropic/messages',
  'gemini/contents',
  'gemini/embeddings',
  'aisdk/text',
  'aisdk/datastream',
  'jina/rerank',
  'jina/embeddings',
  'typesafe/systemone',
  'ollama/chat',
]);

export type ApiFormat = z.infer<typeof apiFormatSchema>;

export const configurableChannelEndpointApiFormats = [
  'openai/chat_completions',
  'openai/responses',
  'openai/image_generation',
  'openai/image_edit',
  'openai/image_variation',
  'openai/embeddings',
  'zenmux/video',
  'openai/moderations',
  'openai/alpha_search',
  'openai/decisions',
  'openai/audio_speech',
  'openai/audio_transcriptions',
  'openai/audio_translations',
  'anthropic/messages',
  'gemini/contents',
  'gemini/embeddings',
  'jina/rerank',
  'jina/embeddings',
  'typesafe/systemone',
] as const;

export const configurableChannelEndpointApiFormatSchema = z.enum(configurableChannelEndpointApiFormats);

// Channel Endpoint
export const channelEndpointSchema = z.object({
  apiFormat: z.string().min(1),
  path: z.string().optional(),
  baseURL: z.url('Invalid URL').optional().or(z.literal('')),
  transport: z.enum(['http', 'websocket']).optional().or(z.literal('')),
  supportsWebSockets: z.boolean().nullable().optional(),
  catalogURL: z.string().nullable().optional(),
  catalogJSON: z.any().nullable().optional(),
});
export type ChannelEndpoint = z.infer<typeof channelEndpointSchema>;

// Channel endpoint auto-detection result
export const detectedChannelEndpointSchema = z.object({
  apiFormat: z.string().min(1),
  supported: z.boolean(),
  statusCode: z.number().int().optional().nullable(),
  reason: z.string(),
});
export type DetectedChannelEndpoint = z.infer<typeof detectedChannelEndpointSchema>;

// Channel Types
export const channelTypeSchema = z.enum([
  'openai',
  'openai_responses',
  'atlascloud',
  'qiniu',
  'qiniu_anthropic',
  'fenno',
  'cline',
  'codex',
  'anthropic',
  'anthropic_aws',
  'anthropic_gcp',
  'gemini_openai',
  'gemini',
  'gemini_vertex',
  'deepseek',
  'deepseek_anthropic',
  'deepinfra',
  'doubao',
  'doubao_anthropic',
  'moonshot',
  'moonshot_anthropic',
  'zhipu',
  'zai',
  'zhipu_anthropic',
  'zai_anthropic',
  'vercel',
  'anthropic_fake',
  'openai_fake',
  'openrouter',
  'xiaomi',
  'xiaomi_anthropic',
  'xai',
  'xai_responses',
  'xai_subscription',
  'ppio',
  'siliconflow',
  'volcengine',
  'volcengine_anthropic',
  'longcat',
  'longcat_anthropic',
  'minimax',
  'minimax_anthropic',
  'aihubmix',
  'aihubmix_anthropic',
  'burncloud',
  'modelscope',
  'bailian',
  'bailian_responses',
  'bailian_anthropic',
  'moonshot_coding',
  'jina',
  'typesafe',
  'cloudflare',
  'github',
  'github_copilot',
  'claudecode',
  'antigravity',
  'cerebras',
  'nanogpt',
  'nanogpt_responses',
  'fireworks',
  'opencode_go',
  'opencode_go_anthropic',
  'ollama',
  'ollama_anthropic',
  'evolink',
  'evolink_anthropic',
  'groq',
  'zenmux',
  'zenmux_responses',
  'zenmux_anthropic',
  'zenmux_gemini',
  'zenmux_video',
  'commandcode',
  'commandcode_anthropic',
]);
export type ChannelType = z.infer<typeof channelTypeSchema>;

// Channel Status
export const channelStatusSchema = z.enum(['enabled', 'disabled', 'archived']);
export type ChannelStatus = z.infer<typeof channelStatusSchema>;

export const capabilityPolicySchema = z.enum(['unlimited', 'require', 'forbid']);
export type CapabilityPolicy = z.infer<typeof capabilityPolicySchema>;

export const apiKeyAutoDisableActionSchema = z.enum([
  'temporary_disable',
  'disable_until_cron',
  'permanent_disable',
  'permanent_disable_delete',
]);
export type APIKeyAutoDisableAction = z.infer<typeof apiKeyAutoDisableActionSchema>;

export const apiKeyAutoDisableModeSchema = z.enum(['inherit', 'custom', 'off']);
export type APIKeyAutoDisableMode = z.infer<typeof apiKeyAutoDisableModeSchema>;

/** GraphQL may still emit "" for unset mode; treat it as null before enum checks. */
export function coerceApiKeyAutoDisableMode(value: unknown): unknown {
  return value === '' ? null : value;
}

export const apiKeyAutoDisableRuleSchema = z.object({
  statusCodes: z.array(z.number().int().min(100).max(599)).optional().nullable(),
  keywordPatterns: z.array(z.string()).optional().nullable(),
  times: z.number().int().min(1),
  action: apiKeyAutoDisableActionSchema,
  disableDurationMinutes: z.number().int().positive().optional().nullable(),
  disableUntilCron: z.string().optional().nullable(),
  disableUntilTimezone: z.string().optional().nullable(),
});
export type APIKeyAutoDisableRule = z.infer<typeof apiKeyAutoDisableRuleSchema>;

export const apiKeyAutoDisableRuleFormSchema = apiKeyAutoDisableRuleSchema
  .refine((rule) => (rule.statusCodes?.length ?? 0) > 0 || (rule.keywordPatterns?.some((pattern) => pattern.trim() !== '') ?? false), {
    message: 'At least one status code or keyword pattern is required',
    path: ['statusCodes'],
  })
  .refine((rule) => rule.action !== 'temporary_disable' || (rule.disableDurationMinutes ?? 0) > 0, {
    message: 'Temporary disable requires a duration',
    path: ['disableDurationMinutes'],
  })
  .refine((rule) => rule.action !== 'disable_until_cron' || (rule.disableUntilCron ?? '').trim() !== '', {
    message: 'Scheduled recovery requires a cron expression',
    path: ['disableUntilCron'],
  });

export const channelPoliciesSchema = z.object({
  stream: capabilityPolicySchema.optional(),
  apiKeyAutoDisableMode: z.preprocess(coerceApiKeyAutoDisableMode, apiKeyAutoDisableModeSchema.optional().nullable()),
  apiKeyAutoDisableRules: z.array(apiKeyAutoDisableRuleSchema).optional().nullable(),
});
export type ChannelPolicies = z.infer<typeof channelPoliciesSchema>;

// Model Mapping
export const modelMappingSchema = z.object({
  from: z.string(),
  to: z.string(),
});
export type ModelMapping = z.infer<typeof modelMappingSchema>;

// Header Entry
export const headerEntrySchema = z.object({
  key: z.string().min(1, 'Header key is required'),
  value: z.string(),
});
export type HeaderEntry = z.infer<typeof headerEntrySchema>;

// Override Operation
export const overrideMatchSchema = z.object({
  path: z.string().trim().min(1),
  eq: z.string().trim().min(1),
});
export type OverrideMatch = z.infer<typeof overrideMatchSchema>;

export const overrideOperationSchema = z.object({
  op: z.enum(['set', 'set_if_absent', 'delete', 'rename', 'copy', 'array_append', 'array_prepend', 'array_insert', 'array_remove']),
  path: z.string().optional(),
  from: z.string().optional(),
  to: z.string().optional(),
  value: z.any().optional(),
  condition: z.string().optional(),
  match: overrideMatchSchema.nullish(),
  index: z.number().int().nullish(),
  splat: z.boolean().nullish(),
});
export type OverrideOperation = z.infer<typeof overrideOperationSchema>;

// Proxy Type
export const proxyTypeSchema = z.enum(['disabled', 'environment', 'url']);
export type ProxyType = z.infer<typeof proxyTypeSchema>;

// Proxy Config
export const proxyConfigSchema = z.object({
  type: proxyTypeSchema,
  url: z.string().optional(),
  username: z.string().optional(),
  password: z.string().optional(),
  disableConnectionReuse: z.boolean().optional(),
});
export type ProxyConfig = z.infer<typeof proxyConfigSchema>;

// Transform Options
export const reasoningEffortMappingSchema = z.object({
  from: z.string().min(1),
  to: z.string().min(1),
});
export type ReasoningEffortMapping = z.infer<typeof reasoningEffortMappingSchema>;

export const transformOptionsSchema = z.object({
  forceArrayInstructions: z.boolean().optional(),
  forceArrayInputs: z.boolean().optional(),
  replaceDeveloperRoleWithSystem: z.boolean().optional(),
  reasoningEffortMapping: z.array(reasoningEffortMappingSchema).nullish(),
});
export type TransformOptions = z.infer<typeof transformOptionsSchema>;

// Channel Probe
export const channelProbePointSchema = z.object({
  timestamp: z.number(),
  totalRequestCount: z.number(),
  successRequestCount: z.number(),
  avgTokensPerSecond: z.number().optional().nullable(),
  avgTimeToFirstTokenMs: z.number().optional().nullable(),
});
export type ChannelProbePoint = z.infer<typeof channelProbePointSchema>;

export const channelProbeDataSchema = z.object({
  channelID: z.string(),
  points: z.array(channelProbePointSchema),
});
export type ChannelProbeData = z.infer<typeof channelProbeDataSchema>;

// Channel Rate Limit
export const channelRateLimitSchema = z.object({
  rpm: z.number().int().nonnegative().optional().nullable(),
  tpm: z.number().int().nonnegative().optional().nullable(),
  maxConcurrent: z.number().int().nonnegative().optional().nullable(),
  queueSize: z.number().int().nonnegative().optional().nullable(),
  queueTimeoutMs: z.number().int().nonnegative().optional().nullable(),
});
export type ChannelRateLimit = z.infer<typeof channelRateLimitSchema>;

// Live snapshot of the per-channel concurrency limiter.
// Returned from the backend only when MaxConcurrent is configured.
export const channelLimiterStatsSchema = z.object({
  inFlight: z.number().int().nonnegative(),
  waiting: z.number().int().nonnegative(),
  capacity: z.number().int().nonnegative(),
  queueSize: z.number().int().nonnegative(),
});
export type ChannelLimiterStats = z.infer<typeof channelLimiterStatsSchema>;

export const channelLiveUsageSchema = z.object({
  inFlight: z.number().int().nonnegative(),
  rpm: z.number().int().nonnegative(),
});

export const retryableErrorPatternSchema = z.object({
  pattern: z.string().min(1),
  regex: z.boolean().optional().nullable(),
});
export type RetryableErrorPattern = z.infer<typeof retryableErrorPatternSchema>;

// Per-model outbound protocol override: forces the api formats a model may use.
// Every listed api_format must already be configured as a channel endpoint.
export const modelProtocolSchema = z.object({
  model: z.string().min(1),
  apiFormats: z.array(z.string().min(1)).min(1),
  // Older channels do not persist this flag; those overrides remain active.
  enabled: z
    .boolean()
    .nullish()
    .transform((value) => value ?? true),
});
export type ModelProtocol = z.infer<typeof modelProtocolSchema>;

// Provider quota collection settings stored inside channel settings. Mirrors the
// GraphQL `CommandCodeQuotaSettings` / `ChannelProviderQuotaSettings` types; it
// is used for the Command Code billing-quota cookie, kept separate from API
// credentials.
export const commandCodeQuotaSettingsSchema = z.object({
  authCookie: z.string().optional().nullable(),
});
export type CommandCodeQuotaSettings = z.infer<typeof commandCodeQuotaSettingsSchema>;

export const ollamaQuotaSettingsSchema = z.object({
  authCookie: z.string().optional().nullable(),
});
export type OllamaQuotaSettings = z.infer<typeof ollamaQuotaSettingsSchema>;

export const channelProviderQuotaSettingsSchema = z.object({
  commandCode: commandCodeQuotaSettingsSchema.optional().nullable(),
  ollama: ollamaQuotaSettingsSchema.optional().nullable(),
});
export type ChannelProviderQuotaSettings = z.infer<typeof channelProviderQuotaSettingsSchema>;

export const channelAddressSchema = z.object({
  id: z.string(),
  name: z.string(),
  url: z.string(),
  priority: z.number().int(),
  enabled: z.boolean(),
});
export type ChannelAddress = z.infer<typeof channelAddressSchema>;
// Channel Settings
export const channelSettingsSchema = z.object({
  upstreamAddresses: z.array(channelAddressSchema).optional(),
  addressFailover: z.boolean().optional(),
  tagPriorities: z.array(z.object({ tag: z.string(), priority: z.number().int() })).optional(),
  globalPriority: z.number().int().nullish(),
  streamFirstEventTimeoutSeconds: z.number().int().nonnegative().optional().nullable(),
  nonStreamResponseTimeoutSeconds: z.number().int().nonnegative().optional().nullable(),
  disableSessionReuse: z.boolean().optional(),
  allowImageGenerationTool: z.boolean().nullish(),
  modelContexts: z.array(channelModelContextSchema).nullable().optional(),
  healthGateFailureThreshold: z.number().int().min(0).optional().nullable(),
  modelPriceMultiplier: z.union([z.string(), z.number()]).optional().nullable(),
  extraModelPrefix: z.string().optional(),
  modelMappings: z.array(modelMappingSchema).optional().nullable(),
  autoTrimedModelPrefixes: z.array(z.string()).optional().nullable(),
  hideOriginalModels: z.boolean().optional(),
  hideMappedModels: z.boolean().optional(),
  lowercaseModelId: z.boolean().optional(),
  bodyOverrideOperations: z.array(overrideOperationSchema).optional(),
  headerOverrideOperations: z.array(overrideOperationSchema).optional(),
  proxy: proxyConfigSchema.optional().nullable(),
  transformOptions: transformOptionsSchema.optional(),
  passThroughUserAgent: z.boolean().optional().nullable(),
  passThroughBody: z.boolean().optional().nullable(),
  forceOneHourPromptCache: z.boolean().optional().nullable(),
  rateLimit: channelRateLimitSchema.optional().nullable(),
  retryableStatusCodes: z.array(z.number().int().min(400).max(599)).optional().nullable(),
  retryableErrorPatterns: z.array(retryableErrorPatternSchema).optional().nullable(),
  // The server persists this as a plain int and does not clamp it, so reads must
  // tolerate any value an API client could have stored; the editor still bounds
  // user input. A strict upper bound here can reject a whole channel response.
  maxSingleChannelRetries: z.number().int().min(0).optional().nullable(),

  modelProtocols: z.array(modelProtocolSchema).optional().nullable(),
  providerQuota: channelProviderQuotaSettingsSchema.optional().nullable(),
  primaryApiFormat: z.string().optional().nullable(),
  // The backend stores an empty strategy for "unset" and returns it verbatim, and
  // an API client may store any string. Normalize every unrecognized value to the
  // default instead of rejecting it, so such a channel still loads and the
  // inferred type stays the narrow strategy union the editor expects.
  apiKeyStrategy: z
    .enum(['sticky', 'random', 'round_robin', 'round_robin_success', 'priority', 'fixed'])
    .catch('sticky')
    .nullable()
    .optional(),
  apiKeyRoundRobinSwitchAfter: z.number().int().min(0).max(2147483647).optional().nullable(),
  quotaRoutingMode: z.enum(['INHERIT', 'IGNORE_QUOTA', 'REMOVE_ON_EXHAUSTED', 'BACKPRESSURE']).optional(),
});

export type ChannelSettings = z.infer<typeof channelSettingsSchema>;
export const channelSettingsInputSchema = channelSettingsSchema.omit({
  modelPriceMultiplier: true,
  tagPriorities: true,
  globalPriority: true,
});
export type ChannelQuotaRoutingMode = NonNullable<ChannelSettings['quotaRoutingMode']>;

// Channel Model Entry
export const channelModelEntrySchema = z.object({
  requestModel: z.string(),
  actualModel: z.string(),
  source: z.string(),
});
export type ChannelModelEntry = z.infer<typeof channelModelEntrySchema>;

// Channel Credentials
export const channelCredentialsSchema = z.object({
  apiKey: z.string().optional().nullable(),
  apiKeys: z.array(z.string()).optional().nullable(),
  // Read-side shape only: persisted credentials are echoed for the form and may
  // predate the triple, so this schema must not reject stored data. Strict
  // validation lives on the write schemas below.
  vertexApiKeys: z
    .array(
      z.object({
        projectID: z.string(),
        apiKey: z.string(),
        region: z.string(),
      })
    )
    .optional()
    .nullable(),
  // Optional provider management/console API key (e.g. ZenMux) used only for
  // server-side quota checks; inference keeps using apiKey/apiKeys.
  managementApiKey: z.string().optional().nullable(),
  oauth: z
    .object({
      accessToken: z.string().optional().nullable(),
      refreshToken: z.string().optional().nullable(),
      clientID: z.string().optional().nullable(),
      accountID: z.string().optional().nullable(),
      expiresAt: z.string().optional().nullable(),
      tokenType: z.string().optional().nullable(),
      scopes: z.array(z.string()).optional().nullable(),
    })
    .optional()
    .nullable(),
  gcp: z
    .object({
      region: z.string(),
      projectID: z.string(),
      jsonData: z.string(),
    })
    .optional()
    .nullable(),
});
export type ChannelCredentials = z.infer<typeof channelCredentialsSchema>;

export const providerQuotaStatusSchema = z.object({
  status: z.enum(['available', 'warning', 'exhausted', 'unknown']),
  nextResetAt: z.string().optional().nullable(),
  ready: z.boolean(),
  quotaData: z.record(z.string(), z.unknown()),
  providerType: z.string(),
  accountKey: z.string().optional().nullable(),
});
export type ProviderQuotaStatus = z.infer<typeof providerQuotaStatusSchema>;

// Disabled API Key
export const disabledAPIKeySchema = z.object({
  key: z.string(),
  disabledAt: z.string(),
  errorCode: z.number(),
  reason: z.string().optional().nullable(),
  expiresAt: z.string().optional().nullable(),
});
export type DisabledAPIKey = z.infer<typeof disabledAPIKeySchema>;

// Sentinel the backend uses to identify a channel's OAuth credential in the
// disable bookkeeping (see objects.OAuthCredentialRef). It is not a real key,
// so it must be labelled rather than masked, and it cannot be deleted.
export const OAUTH_CREDENTIAL_REF = '__oauth__';

export const channelHealthGateModelSchema = z.object({
  actualModel: z.string(),
  state: z.enum(['healthy', 'unstable', 'open', 'probing']),
  consecutiveFailures: z.number(),
  probeSuccesses: z.number(),
  backoffLevel: z.number(),
  lastError: z.string().optional().nullable(),
  lastStatusCode: z.number().optional().nullable(),
  lastErrorAt: z.string().optional().nullable(),
  openUntil: z.string().optional().nullable(),
});
export const channelHealthGateSummarySchema = z.object({
  disabled: z.boolean(),
  openCount: z.number(),
  unstableCount: z.number(),
});
export const channelHealthGateStatusSchema = channelHealthGateSummarySchema.extend({
  failureThreshold: z.number(),
  probeSuccessThreshold: z.number(),
  models: z.array(channelHealthGateModelSchema),
});
export type ChannelHealthGateStatus = z.infer<typeof channelHealthGateStatusSchema>;

// Channel
export const channelTodayUsageSchema = z.object({
  channelId: z.string(),
  requestCount: z.number(),
  unpricedCount: z.number(),
  totalCost: z.number(),
  timezone: z.string(),
  currencyCode: z.string(),
});

export const channelSchema = z.object({
  todayUsage: channelTodayUsageSchema.optional(),
  todayUsageError: z.boolean().optional(),
  upstreamState: channelUpstreamStateSchema.optional(),
  upstreamLoadError: z.boolean().optional(),
  id: z.string(),
  createdAt: z.string(),
  updatedAt: z.string(),
  type: channelTypeSchema,
  baseURL: z.string(),
  websiteURL: z.string().nullable().optional(),
  name: z.string(),
  status: channelStatusSchema,
  policies: channelPoliciesSchema.optional().nullable(),
  credentials: channelCredentialsSchema.optional().nullable(),
  providerQuotaStatus: providerQuotaStatusSchema.optional().nullable(),
  disabledAPIKeys: z.array(disabledAPIKeySchema).optional().nullable(),
  healthGate: channelHealthGateSummarySchema.optional().nullable(),
  supportedModels: z.array(z.string()).default([]),
  autoSyncSupportedModels: z.boolean().default(false),
  autoSyncModelPattern: z.string().optional().default(''),
  manualModels: z.array(z.string()).optional().default([]).nullable(),
  tags: z.array(z.string()).optional().default([]).nullable(),
  defaultTestModel: z.string(),
  settings: channelSettingsSchema.optional().nullable(),
  orderingWeight: z.number().optional().default(0),
  errorMessage: z.string().optional().nullable(),
  remark: z.string().optional().nullable(),
  allModelEntries: z.array(channelModelEntrySchema).optional(),
  liveLimiterStats: channelLimiterStatsSchema.optional().nullable(),
  liveUsage: channelLiveUsageSchema.optional().nullable(),
  endpoints: z.array(channelEndpointSchema).optional().default([]).nullable(),
  defaultEndpoints: z.array(channelEndpointSchema).optional().default([]).nullable(),
});
export type Channel = z.infer<typeof channelSchema>;

// Simplified schema for saveChannelEndpoints mutation response
export const channelEndpointsResponseSchema = z.object({
  id: z.string(),
  type: channelTypeSchema,
  name: z.string(),
  defaultEndpoints: z.array(channelEndpointSchema).optional().default([]).nullable(),
  endpoints: z.array(channelEndpointSchema).optional().default([]).nullable(),
});
export type ChannelEndpointsResponse = z.infer<typeof channelEndpointsResponseSchema>;

export const testAPIKeyResultSchema = z.object({
  keyPrefix: z.string(),
  success: z.boolean(),
  latency: z.number(),
  error: z.string().optional().nullable(),
  disabled: z.boolean(),
});
export type TestAPIKeyResult = z.infer<typeof testAPIKeyResultSchema>;

export const testChannelAPIKeysPayloadSchema = z.object({
  channelID: z.string(),
  total: z.number(),
  successCount: z.number(),
  failedCount: z.number(),
  results: z.array(testAPIKeyResultSchema),
});
export type TestChannelAPIKeysPayload = z.infer<typeof testChannelAPIKeysPayloadSchema>;

// Pricing Schemas
export const pricingModeSchema = z.enum(['flat_fee', 'image_fixed', 'usage_per_unit', 'usage_tiered', 'usage_volume']);
export type PricingMode = z.infer<typeof pricingModeSchema>;

export const priceItemCodes = ['prompt_tokens', 'completion_tokens', 'prompt_cached_tokens', 'prompt_write_cached_tokens', 'prompt_text_tokens', 'prompt_image_tokens', 'prompt_text_cached_tokens', 'prompt_image_cached_tokens'] as const;
export const priceItemCodeSchema = z.enum(priceItemCodes);

export function isModalityPriceItem(code: string | undefined): boolean {
  return ['prompt_text_tokens', 'prompt_image_tokens', 'prompt_text_cached_tokens', 'prompt_image_cached_tokens'].includes(code ?? '');
}

export function canAddPriceItem(code: string, items: readonly { itemCode: string }[]): boolean {
  if (items.some((item) => item.itemCode === code)) return false;
  for (const [aggregate, text, image] of [
    ['prompt_tokens', 'prompt_text_tokens', 'prompt_image_tokens'],
    ['prompt_cached_tokens', 'prompt_text_cached_tokens', 'prompt_image_cached_tokens'],
  ]) {
    if (code === aggregate && items.some((item) => item.itemCode === text || item.itemCode === image)) return false;
    if ((code === text || code === image) && items.some((item) => item.itemCode === aggregate)) return false;
  }
  return true;
}
export type PriceItemCode = z.infer<typeof priceItemCodeSchema>;

export const priceTierSchema = z.object({
  upTo: z.number().nullable().optional(),
  pricePerUnit: z.union([z.string(), z.number()]),
});
export type PriceTier = z.infer<typeof priceTierSchema>;

export const tieredPricingSchema = z.object({
  tiers: z.array(priceTierSchema),
});
export type TieredPricing = z.infer<typeof tieredPricingSchema>;

const imagePriceRuleSchema = z.object({
  name: z.string(),
  fee: z.union([z.string(), z.number()]),
  match: z.enum(['any', 'all']),
  conditions: z
    .array(
      z.object({
        dimension: z.enum(['width', 'height']),
        operator: z.enum(['gt', 'gte', 'eq', 'lte', 'lt']),
        value: z.number(),
      })
    )
    .nullable()
    .optional(),
});

export const imagePricingSchema = z.object({
  automatic: z.boolean(),
  rules: z.array(imagePriceRuleSchema),
  savedRules: z
    .array(imagePriceRuleSchema.extend({ fee: z.string() }))
    .nullable()
    .optional(),
});
export type ImagePricing = z.infer<typeof imagePricingSchema>;

export const pricingSchema = z.object({
  mode: pricingModeSchema,
  image: imagePricingSchema.nullable().optional(),
  flatFee: z.union([z.string(), z.number()]).nullable().optional(),
  usagePerUnit: z.union([z.string(), z.number()]).nullable().optional(),
  usageTiered: tieredPricingSchema.nullable().optional(),
});
export type Pricing = z.infer<typeof pricingSchema>;

export const promptWriteCacheVariantSchema = z.object({
  variantCode: z.enum(['five_min', 'one_hour']),
  pricing: pricingSchema,
});
export type PromptWriteCacheVariant = z.infer<typeof promptWriteCacheVariantSchema>;

export const modelPriceItemSchema = z.object({
  itemCode: priceItemCodeSchema,
  pricing: pricingSchema,
  promptWriteCacheVariants: z.array(promptWriteCacheVariantSchema).nullable().optional(),
});
export type ModelPriceItem = z.infer<typeof modelPriceItemSchema>;

// Time-based price schedule schemas
// DailyTimeRange uses "HH:mm" format strings (e.g. "03:00", "18:30")
export const dailyTimeRangeSchema = z.object({
  start: z.string(),
  end: z.string(),
});
export type DailyTimeRange = z.infer<typeof dailyTimeRangeSchema>;

export const dateRangeSchema = z.object({
  start: z.string(),
  end: z.string(),
});
export type DateRange = z.infer<typeof dateRangeSchema>;

export const overrideWhenSchema = z.object({
  dailyTime: dailyTimeRangeSchema.optional().nullable(),
  weekdays: z.array(z.number().int().min(1).max(7)).optional().nullable(),
  dateRange: dateRangeSchema.optional().nullable(),
});
export type OverrideWhen = z.infer<typeof overrideWhenSchema>;

export const serviceTierSettingsSchema = z.object({ enabled: z.boolean(), basis: z.enum(['upstream', 'client']) });
export const cacheWriteBillingSchema = z.enum(['reported', 'swap_5m_1h']);
export type CacheWriteBilling = z.infer<typeof cacheWriteBillingSchema>;
export type ServiceTierSettings = z.infer<typeof serviceTierSettingsSchema>;
export const serviceTierItemRuleSchema = z.object({
  referenceBasePrice: z.union([z.string(), z.number()]).nullable().optional(),
  referenceModePrice: z.union([z.string(), z.number()]).nullable().optional(),
  itemCode: priceItemCodeSchema,
  variantCode: z.string().optional().nullable(),
  method: z.enum(['multiplier', 'unit_price', 'unpriced']),
  value: z.union([z.string(), z.number()]).nullable().optional(),
});
export type ServiceTierItemRule = z.infer<typeof serviceTierItemRuleSchema>;
export const serviceTierPriceSchema = z.object({
  source: z.enum(['official', 'custom', 'model']),
  items: z.array(serviceTierItemRuleSchema).nullable().optional(),
  unpriced: z.boolean().optional(),
  application: z.string().optional(),
  contextAbove: z.number().nullable().optional(),
  contextUpTo: z.number().nullable().optional(),
  sourceURL: z.string().optional(),
  verifiedAt: z.string().optional(),
  currency: z.string().optional(),
  unit: z.string().optional(),
  multiplier: z.union([z.string(), z.number()]).nullable().optional(),
  referenceModel: z.string().optional(),
  version: z.string().optional(),
});
export const serviceTierPricesSchema = z.object({
  fast: serviceTierPriceSchema.nullable().optional(),
  ultrafast: serviceTierPriceSchema.nullable().optional(),
});
export type ServiceTierPrices = z.infer<typeof serviceTierPricesSchema>;

export const priceOverrideSchema = z.object({
  serviceTiers: serviceTierPricesSchema.optional().nullable(),
  name: z.string(),
  priority: z.number().int(),
  when: overrideWhenSchema,
  items: z.array(modelPriceItemSchema),
});
export type PriceOverride = z.infer<typeof priceOverrideSchema>;

export const priceScheduleSchema = z.object({
  timezone: z.string(),
  overrides: z.array(priceOverrideSchema),
});
export type PriceSchedule = z.infer<typeof priceScheduleSchema>;

export const modelPriceVolumeTierSchema = z.object({
  serviceTiers: serviceTierPricesSchema.optional().nullable(),
  longContext: z.boolean().optional(),
  above: z.number().int().nonnegative(),
  items: z.array(modelPriceItemSchema),
});
export type ModelPriceVolumeTier = z.infer<typeof modelPriceVolumeTierSchema>;

export const modelPriceSchema = z.object({
  serviceTiers: serviceTierPricesSchema.optional().nullable(),
  multiplier: z.union([z.string(), z.number()]).optional().nullable(),
  items: z.array(modelPriceItemSchema),
  volumeTiers: z.array(modelPriceVolumeTierSchema).optional().nullable(),
  schedule: priceScheduleSchema.optional().nullable(),
});
export type ModelPrice = z.infer<typeof modelPriceSchema>;
const pricingInputSchema = pricingSchema.transform(
  (pricing): Pricing => ({
    mode: pricing.mode,
    image: pricing.mode === 'image_fixed' ? pricing.image : null,
    flatFee: pricing.mode === 'flat_fee' ? (typeof pricing.flatFee === 'string' ? pricing.flatFee.trim() || null : pricing.flatFee) : null,
    usagePerUnit:
      pricing.mode === 'usage_per_unit'
        ? typeof pricing.usagePerUnit === 'string'
          ? pricing.usagePerUnit.trim() || null
          : pricing.usagePerUnit
        : null,
    usageTiered:
      pricing.mode === 'usage_tiered' || pricing.mode === 'usage_volume'
        ? pricing.usageTiered && {
            tiers: pricing.usageTiered.tiers.map((tier) => ({
              ...tier,
              pricePerUnit: typeof tier.pricePerUnit === 'string' ? tier.pricePerUnit.trim() : tier.pricePerUnit,
            })),
          }
        : null,
  })
);
const modelPriceItemInputSchema = modelPriceItemSchema.extend({
  pricing: pricingInputSchema,
  promptWriteCacheVariants: z
    .array(promptWriteCacheVariantSchema.extend({ pricing: pricingInputSchema }))
    .optional()
    .nullable(),
});
export const modelPriceInputSchema = modelPriceSchema.omit({ multiplier: true }).extend({
  items: z.array(modelPriceItemInputSchema),
  volumeTiers: z
    .array(modelPriceVolumeTierSchema.extend({ items: z.array(modelPriceItemInputSchema) }))
    .optional()
    .nullable(),
  schedule: priceScheduleSchema
    .extend({
      overrides: z.array(priceOverrideSchema.extend({ items: z.array(modelPriceItemInputSchema) })),
    })
    .optional()
    .nullable(),
});

export const channelModelPriceSchema = z.object({
  id: z.string(),
  modelID: z.string(),
  price: modelPriceSchema,
});
export type ChannelModelPrice = z.infer<typeof channelModelPriceSchema>;

export const saveChannelModelPriceInputSchema = z.object({
  modelId: z.string(),
  price: modelPriceInputSchema,
});
export type SaveChannelModelPriceInput = z.infer<typeof saveChannelModelPriceInputSchema>;
// Helper function to validate OAuth credentials
function validateOAuthCredentials(type: string, apiKey: string | undefined, ctx: z.RefinementCtx) {
  if (!apiKey) return;

  const isCopilot = type === 'github_copilot';
  const requiresJSON = isCopilot || type === 'xai_subscription';
  if (requiresJSON && !apiKey.trim().startsWith('{')) {
    ctx.addIssue({
      code: 'custom' as const,
      message: isCopilot ? 'channels.dialogs.oauth.errors.copilotCredentialsInvalid' : 'channels.dialogs.oauth.errors.credentialsInvalid',
      path: ['credentials', 'apiKey'],
    });
    return;
  }

  // Only enforce JSON validation if it looks like JSON (starts with '{')
  if (!apiKey.trim().startsWith('{')) return;

  const issue = {
    code: 'custom' as const,
    message: 'channels.dialogs.oauth.errors.credentialsInvalid',
    path: ['credentials', 'apiKey'],
  };

  let json: unknown;
  try {
    json = JSON.parse(apiKey);
  } catch {
    ctx.addIssue(issue);
    return;
  }

  // GitHub Copilot only requires access_token, others may require refresh_token
  const parsed = z
    .object({
      access_token: z.string().min(1),
      refresh_token: isCopilot ? z.string().optional() : z.string().min(1),
    })
    .safeParse(json);

  if (!parsed.success) {
    ctx.addIssue(issue);
  }
}

// Vertex project and region are interpolated into the upstream request path, so the
// write schemas constrain them to the characters Google permits. The backend
// validator applies the same pattern.
const vertexAPIKeyProjectIDPattern = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;
const vertexAPIKeyRegionPattern = /^[A-Za-z0-9][A-Za-z0-9-]*$/;

// vertexAPIKeyCredentialSchema validates one project-bound Vertex credential.
// defaultRegion fills an omitted region; omit it where an empty region must be
// reported as an error instead.
function vertexAPIKeyCredentialSchema(defaultRegion?: string) {
  return z.object({
    projectID: z.string().trim().regex(vertexAPIKeyProjectIDPattern, 'Vertex Project ID contains invalid characters'),
    apiKey: z.string().trim().min(1, 'Vertex API key is required'),
    region: defaultRegion
      ? z.string().trim().regex(vertexAPIKeyRegionPattern, 'Vertex region contains invalid characters').default(defaultRegion)
      : z.string().trim().regex(vertexAPIKeyRegionPattern, 'Vertex region contains invalid characters'),
  });
}

// Shape-only counterpart of vertexAPIKeyCredentialSchema. The write field schemas must
// accept a row the channel type never submits, so the strict rules are re-applied per
// channel type in validateVertexAPIKeys instead of on the field itself.
const vertexAPIKeyShapeSchema = z.object({
  projectID: z.string(),
  apiKey: z.string(),
  region: z.string().default('global'),
});

// Vertex rows only mean something on a gemini_vertex channel. Validating them behind
// the type check keeps a leftover row from blocking an ordinary channel, whose submit
// clears vertexApiKeys for the non-Vertex type.
function validateVertexAPIKeys(
  channelType: string | undefined,
  credentials: { vertexApiKeys?: { projectID: string; apiKey: string; region?: string }[] | null } | undefined,
  ctx: z.RefinementCtx
) {
  if (channelType !== 'gemini_vertex') {
    return;
  }

  const rowSchema = vertexAPIKeyCredentialSchema('global');
  const rows = credentials?.vertexApiKeys;

  // An empty list is itself an error: create always materialises the field, and an
  // update that sends one is clearing the last binding.
  if (rows && !rows.length) {
    ctx.addIssue({
      code: 'custom' as const,
      message: 'At least one Vertex API key is required',
      path: ['credentials', 'vertexApiKeys'],
    });
    return;
  }

  (rows ?? []).forEach((row, index) => {
    const parsed = rowSchema.safeParse(row);
    if (parsed.success) {
      return;
    }

    parsed.error.issues.forEach((issue) => {
      ctx.addIssue({
        code: 'custom' as const,
        message: issue.message,
        path: ['credentials', 'vertexApiKeys', index, ...issue.path],
      });
    });
  });
}

// Create Channel Input

// A saved base URL must not keep template placeholders such as <account_id>:
// the channel would be stored unresolved and only fail on its first request,
// with an upstream error that hides the cause.
const channelBaseURLSchema = z
  .string()
  .url('Please enter a valid URL')
  .refine((value) => !/[<>]/.test(value), 'Replace placeholders such as <account_id> with real values');

export const createChannelInputSchema = z
  .object({
    type: channelTypeSchema,
    baseURL: channelBaseURLSchema,
    websiteURL: z.string().trim().max(2048).optional(),
    name: z.string().min(1, 'Name is required'),
    policies: channelPoliciesSchema.optional(),
    supportedModels: z.array(z.string()).min(0, 'At least one supported model is required'),
    autoSyncSupportedModels: z.boolean().optional().default(false),
    autoSyncModelPattern: z.string().optional().default(''),
    manualModels: z.array(z.string()).optional().nullable(),
    tags: z.array(z.string()).optional().default([]),
    defaultTestModel: z.string().min(1, 'Please select a default test model'),
    remark: z.string().optional(),
    orderingWeight: z.number().int().optional(),
    settings: channelSettingsInputSchema.optional(),
    endpoints: z.array(channelEndpointSchema).optional(),
    credentials: z.object({
      // apiKey is used for OAuth credentials (JSON string with access_token, refresh_token)
      apiKey: z.string().optional(),
      // apiKeys is used for regular API keys (multiple keys for load balancing)
      apiKeys: z.array(z.string()).optional().default([]),
      // Vertex credentials are project-bound triples; an omitted region defaults to global.
      vertexApiKeys: z.array(vertexAPIKeyShapeSchema).optional().default([]),
      // Optional management key used only by the backend for quota checks
      managementApiKey: z.string().optional(),
      gcp: z
        .object({
          region: z.string().optional(),
          projectID: z.string().optional(),
          jsonData: z.string().optional(),
        })
        .optional(),
    }),
  })
  .superRefine((data, ctx) => {
    const hasApiKey = data.credentials.apiKey && data.credentials.apiKey.trim().length > 0;
    const hasApiKeys = data.credentials.apiKeys && data.credentials.apiKeys.some((k) => k.trim().length > 0);
    const isOAuthType =
      (data.type === 'codex' && !hasApiKeys) ||
      data.type === 'claudecode' ||
      data.type === 'antigravity' ||
      data.type === 'github_copilot' ||
      data.type === 'xai_subscription';

    // github_copilot requires credentials.apiKey (OAuth JSON with access_token)
    if (data.type === 'github_copilot' && !hasApiKey) {
      ctx.addIssue({
        code: 'custom' as const,
        message: 'channels.dialogs.oauth.errors.copilotCredentialsRequired',
        path: ['credentials', 'apiKey'],
      });
    }

    // Validate that at least one credential type is provided. A Vertex key only counts
    // on a gemini_vertex channel, so it must not satisfy an ordinary channel.
    if (
      !hasApiKey &&
      !hasApiKeys &&
      data.type !== 'anthropic_aws' &&
      data.type !== 'anthropic_gcp' &&
      data.type !== 'gemini_vertex'
    ) {
      ctx.addIssue({
        code: 'custom' as const,
        message: 'At least one API Key is required',
        path: ['credentials', 'apiKeys'],
      });
    }

    // For OAuth types, validate the OAuth JSON format if apiKey is provided
    if (isOAuthType && hasApiKey) {
      validateOAuthCredentials(data.type, data.credentials.apiKey, ctx);
    }
    // 如果是 anthropic_gcp 类型，GCP 字段必填（精确到字段级报错）
    if (data.type === 'anthropic_gcp') {
      const gcp = data.credentials?.gcp;
      if (!gcp?.region) {
        ctx.addIssue({
          code: 'custom',
          message: 'GCP Region is required',
          path: ['credentials', 'gcp', 'region'],
        });
      }
      if (!gcp?.projectID) {
        ctx.addIssue({
          code: 'custom',
          message: 'GCP Project ID is required',
          path: ['credentials', 'gcp', 'projectID'],
        });
      }
      if (!gcp?.jsonData) {
        ctx.addIssue({
          code: 'custom',
          message: 'GCP Service Account JSON is required',
          path: ['credentials', 'gcp', 'jsonData'],
        });
      }
    }

    // Vertex rows carry project and region, so their own rules report a missing field
    // or an invalid project/region, and an empty list is rejected.
    validateVertexAPIKeys(data.type, data.credentials, ctx);
  });
export type CreateChannelInput = z.infer<typeof createChannelInputSchema>;

// Update Channel Input
export const updateChannelInputSchema = z
  .object({
    type: channelTypeSchema.optional(),
    baseURL: channelBaseURLSchema.optional(),
    websiteURL: z.string().trim().max(2048).optional(),
    name: z.string().min(1, 'Name is required').optional(),
    policies: channelPoliciesSchema.optional(),
    supportedModels: z.array(z.string()).min(1, 'At least one supported model is required').optional(),
    autoSyncSupportedModels: z.boolean().optional(),
    autoSyncModelPattern: z.string().optional(),
    manualModels: z.array(z.string()).optional().nullable(),
    tags: z.array(z.string()).optional(),
    defaultTestModel: z.string().min(1, 'Please select a default test model').optional(),
    settings: channelSettingsInputSchema.optional(),
    errorMessage: z.string().optional().nullable(),
    remark: z.string().optional().nullable(),
    endpoints: z.array(channelEndpointSchema).optional(),
    credentials: z
      .object({
        // apiKey 用于 OAuth 凭据 (codex/claudecode/antigravity)，存储 JSON 字符串（含 access_token, refresh_token）
        apiKey: z.string().optional(),
        // apiKeys 用于普通 API Key（支持多 key 负载均衡），OAuth 类型不使用此字段
        apiKeys: z.array(z.string()).optional(),
        // Vertex credentials are project-bound triples; an omitted region defaults to global.
        vertexApiKeys: z.array(vertexAPIKeyShapeSchema).optional(),
        // Optional management key used only by the backend for quota checks
        managementApiKey: z.string().optional(),
        gcp: z
          .object({
            region: z.string().optional(),
            projectID: z.string().optional(),
            jsonData: z.string().optional(),
          })
          .optional(),
      })
      .optional(),
    orderingWeight: z.number().optional(),
  })
  .superRefine((data, ctx) => {
    const effectiveType = data.type;
    const hasApiKey = data.credentials?.apiKey && data.credentials.apiKey.trim().length > 0;
    const hasApiKeys = (data.credentials?.apiKeys?.length ?? 0) > 0;

    // For OAuth validation on updates: validate if type is OAuth, or if credentials.apiKey is provided
    // (which indicates OAuth credentials are being set)
    const isOAuthType =
      (effectiveType === 'codex' && !hasApiKeys) ||
      effectiveType === 'claudecode' ||
      effectiveType === 'antigravity' ||
      effectiveType === 'github_copilot' ||
      effectiveType === 'xai_subscription';

    const derivedType = effectiveType;

    // If we have an OAuth key but no type, check if it looks like Copilot credentials
    const isCopilotKey = hasApiKey && data.credentials?.apiKey?.trim().startsWith('{');
    const isThirdPartyCodex = effectiveType === 'codex' && hasApiKeys;

    if (!isThirdPartyCodex && (isOAuthType || derivedType === 'github_copilot' || isCopilotKey)) {
      if (isCopilotKey && !derivedType) {
        try {
          const parsed = JSON.parse(data.credentials?.apiKey ?? '');
          if (!parsed.access_token) {
            ctx.addIssue({
              code: 'custom',
              message: 'channels.dialogs.oauth.errors.copilotCredentialsInvalid',
              path: ['credentials', 'apiKey'],
            });
          }
        } catch {
          ctx.addIssue({
            code: 'custom',
            message: 'channels.dialogs.oauth.errors.copilotCredentialsInvalid',
            path: ['credentials', 'apiKey'],
          });
        }
        return;
      }
      validateOAuthCredentials(derivedType ?? '', data.credentials?.apiKey ?? '', ctx);
    }

    // 如果是 anthropic_gcp 类型且提供了 credentials，GCP 字段必填（字段级报错）
    if (data.type === 'anthropic_gcp' && data.credentials) {
      const gcp = data.credentials.gcp;
      if (!gcp?.region) {
        ctx.addIssue({
          code: 'custom',
          message: 'GCP Region is required',
          path: ['credentials', 'gcp', 'region'],
        });
      }
      if (!gcp?.projectID) {
        ctx.addIssue({
          code: 'custom',
          message: 'GCP Project ID is required',
          path: ['credentials', 'gcp', 'projectID'],
        });
      }
      if (!gcp?.jsonData) {
        ctx.addIssue({
          code: 'custom',
          message: 'GCP Service Account JSON is required',
          path: ['credentials', 'gcp', 'jsonData'],
        });
      }
    }

    // An update may clear the list, so an empty array is itself an error.
    validateVertexAPIKeys(data.type, data.credentials, ctx);
  });

export type UpdateChannelInput = z.infer<typeof updateChannelInputSchema>;

// Channel Connection (for pagination)
export const channelConnectionSchema = z.object({
  edges: z.array(
    z.object({
      node: channelSchema,
      cursor: z.string(),
    })
  ),
  pageInfo: pageInfoSchema,
  totalCount: z.number(),
});
export type ChannelConnection = z.infer<typeof channelConnectionSchema>;

// Bulk Import Schemas
export const bulkImportChannelItemSchema = z.object({
  type: channelTypeSchema,
  name: z.string().min(1, 'Name is required'),
  baseURL: channelBaseURLSchema,
  apiKey: z.string().min(1, 'API Key is required'),
  supportedModels: z.array(z.string()).min(1, 'At least one supported model is required'),
  defaultTestModel: z.string().min(1, 'Please select a default test model'),
});
export type BulkImportChannelItem = z.infer<typeof bulkImportChannelItemSchema>;

export const bulkImportChannelsInputSchema = z.object({
  channels: z.array(bulkImportChannelItemSchema).min(1, 'At least one channel is required'),
});
export type BulkImportChannelsInput = z.infer<typeof bulkImportChannelsInputSchema>;

export const bulkImportChannelsResultSchema = z.object({
  success: z.boolean(),
  created: z.number(),
  failed: z.number(),
  errors: z.array(z.string()).optional().nullable(),
  channels: z.array(channelSchema).nullable(),
});
export type BulkImportChannelsResult = z.infer<typeof bulkImportChannelsResultSchema>;

// Raw text input for bulk import
export const bulkImportTextSchema = z.object({
  text: z.string().min(1, 'Please enter data to import'),
});
export type BulkImportText = z.infer<typeof bulkImportTextSchema>;

// Bulk Ordering Schemas
export const channelOrderingItemSchema = z.object({
  id: z.string(),
  name: z.string(),
  type: channelTypeSchema,
  status: channelStatusSchema,
  baseURL: z.string(),
  orderingWeight: z.number(),
  tags: z.array(z.string()).optional().default([]).nullable(),
  supportedModels: z.array(z.string()).optional().default([]).nullable(),
  allModelEntries: z.array(channelModelEntrySchema).optional(),
});
export type ChannelOrderingItem = z.infer<typeof channelOrderingItemSchema>;

export const channelOrderingConnectionSchema = z.object({
  edges: z.array(
    z.object({
      node: channelOrderingItemSchema,
    })
  ),
  totalCount: z.number(),
});
export type ChannelOrderingConnection = z.infer<typeof channelOrderingConnectionSchema>;

export const channelSummarySchema = z.object({
  id: z.string(),
  name: z.string(),
  type: channelTypeSchema,
  status: channelStatusSchema,
  baseURL: z.string(),
  orderingWeight: z.number(),
  tags: z.array(z.string()).optional().default([]).nullable(),
  endpoints: z.array(channelEndpointSchema).optional().default([]).nullable(),
  allModelEntries: z.array(channelModelEntrySchema).optional().default([]),
});
export type ChannelSummary = z.infer<typeof channelSummarySchema>;

export const channelSummaryConnectionSchema = z.object({
  edges: z.array(
    z.object({
      node: channelSummarySchema,
    })
  ),
  totalCount: z.number(),
});
export type ChannelSummaryConnection = z.infer<typeof channelSummaryConnectionSchema>;

export const bulkUpdateChannelOrderingInputSchema = z.object({
  channels: z
    .array(
      z.object({
        id: z.string(),
        orderingWeight: z.number(),
      })
    )
    .min(1, 'At least one channel is required'),
});
export type BulkUpdateChannelOrderingInput = z.infer<typeof bulkUpdateChannelOrderingInputSchema>;

export const bulkUpdateChannelOrderingResultSchema = z.object({
  success: z.boolean(),
  updated: z.number(),
  channels: z.array(channelSchema),
});
export type BulkUpdateChannelOrderingResult = z.infer<typeof bulkUpdateChannelOrderingResultSchema>;

export const bulkAutoDisableActionSchema = z.enum(['write_rules', 'inherit', 'off']);
export type BulkAutoDisableAction = z.infer<typeof bulkAutoDisableActionSchema>;

export const bulkUpdateChannelAutoDisableInputSchema = z.object({
  channelIDs: z.array(z.string()).min(1),
  action: bulkAutoDisableActionSchema,
  rules: z.array(apiKeyAutoDisableRuleSchema).optional(),
});
export type BulkUpdateChannelAutoDisableInput = z.infer<typeof bulkUpdateChannelAutoDisableInputSchema>;

export const channelAutoDisableCopySourceSchema = z.object({
  id: z.string(),
  name: z.string(),
  policies: channelPoliciesSchema.optional().nullable(),
});
export type ChannelAutoDisableCopySource = z.infer<typeof channelAutoDisableCopySourceSchema>;

// Mutation only selects id/name/policies; do not parse with the full channelSchema.
export const bulkUpdateChannelAutoDisablePayloadSchema = z.object({
  success: z.boolean(),
  updated: z.number(),
  channels: z.array(channelAutoDisableCopySourceSchema),
});
export type BulkUpdateChannelAutoDisablePayload = z.infer<typeof bulkUpdateChannelAutoDisablePayloadSchema>;

// Re-export template types from templates.ts
export type {
  ChannelOverrideTemplate,
  ChannelOverrideTemplateConnection,
  CreateChannelOverrideTemplateInput,
  UpdateChannelOverrideTemplateInput,
  ApplyChannelOverrideTemplateInput,
  ApplyChannelOverrideTemplatePayload,
  ClearChannelOverrideTemplatesInput,
  ClearChannelOverrideTemplatesPayload,
} from './templates';
