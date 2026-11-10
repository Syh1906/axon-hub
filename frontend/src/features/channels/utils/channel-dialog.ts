import { getDefaultBaseURL, CHANNEL_CONFIGS, OPENAI_CHAT_COMPLETIONS, OPENAI_IMAGE_GENERATION } from '../data/config_channels';
import { getInitialApiFormatForChannel, getCodexResponsesWebSocketState, getResponsesWebSocketEnabled, toHttpBaseURL } from '../data/protocol-options';
import { Channel, ChannelType, ApiFormat, RetryableErrorPattern, ChannelSettings, ChannelQuotaRoutingMode } from '../data/schema';

export interface Props {
  currentRow?: Channel;
  duplicateFromRow?: Channel;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  showModelsPanel?: boolean;
}

export const MAX_MODELS_DISPLAY = 2;


export type ApiFormatOption = ApiFormat;

export type ResponsesTransport = 'http' | 'websocket';

export const OPENAI_RESPONSES_WEBSOCKET: ApiFormatOption = 'openai/responses-ws';

const OPENAI_RESPONSES_WEBSOCKET_BASE_URL = 'https://api.openai.com/v1#';

const CODEX_RESPONSES_WEBSOCKET_BASE_URL = 'https://chatgpt.com/backend-api/codex#';

export function baseURLMatchesResponsesTransport(baseURL: string | undefined, _transport: ResponsesTransport): boolean {
  const normalized = baseURL?.trim().toLowerCase() ?? '';
  return _transport === 'http' ? /^https?:\/\//.test(normalized) : /^(https?|wss?):\/\//.test(normalized);
}

export function getInitialApiFormatForRow(row: Channel): ApiFormat {
  if (row.settings?.primaryApiFormat === OPENAI_IMAGE_GENERATION) {
    return OPENAI_IMAGE_GENERATION;
  }

  return getInitialApiFormatForChannel(
    row.type,
    CHANNEL_CONFIGS[row.type as ChannelType]?.apiFormat || OPENAI_CHAT_COMPLETIONS,
    row.settings?.modelProtocols
  );
}

export function getResponsesTransportBaseURLError(transport: ResponsesTransport): string {
  return transport === 'websocket'
    ? 'channels.dialogs.fields.baseURL.errors.websocketScheme'
    : 'channels.dialogs.fields.baseURL.errors.httpScheme';
}

export function formatRetryableStatusCodes(codes: number[] | null | undefined): string {
  return (codes ?? []).join(', ');
}

export function parseRetryableStatusCodesInput(value: string): number[] | null {
  const tokens = value.split(/[,\s]+/).filter(Boolean);

  if (tokens.length === 0) {
    return [];
  }

  const codes: number[] = [];
  for (const token of tokens) {
    if (!/^\d+$/.test(token)) {
      return null;
    }

    const code = Number(token);
    if (code < 400 || code > 599) {
      return null;
    }

    codes.push(code);
  }

  return Array.from(new Set(codes)).sort((a, b) => a - b);
}

export function formatRetryableErrorPatterns(patterns: RetryableErrorPattern[] | null | undefined): string {
  return (patterns ?? []).map(({ pattern, regex }) => (regex ? `regex:${pattern}` : pattern)).join('\n');
}

export function parseRetryableErrorPatternsInput(value: string): RetryableErrorPattern[] | null {
  const patterns: RetryableErrorPattern[] = [];
  const seen = new Set<string>();

  for (const rawLine of value.split(/\r?\n/)) {
    let line = rawLine.trim();
    if (!line) {
      continue;
    }

    const regex = line.toLowerCase().startsWith('regex:');
    if (regex) {
      line = line.slice('regex:'.length).trim();
    }

    if (!line) {
      return null;
    }

    const key = `${regex}\0${line}`;
    if (!seen.has(key)) {
      seen.add(key);
      patterns.push({ pattern: line, regex });
    }
  }

  return patterns;
}

export function getResponsesTransportFromChannel(channel?: Pick<Channel, 'type' | 'baseURL' | 'endpoints'>): ResponsesTransport {
  if (channel?.type === 'codex') return getCodexResponsesWebSocketState(channel).enabled ? 'websocket' : 'http';
  return channel && getResponsesWebSocketEnabled(channel) ? 'websocket' : 'http';
}

export function getResponsesWebSocketBaseURL(channelType: ChannelType): string | undefined {
  if (channelType === 'codex') return CODEX_RESPONSES_WEBSOCKET_BASE_URL;
  if (channelType === 'openai_responses') return OPENAI_RESPONSES_WEBSOCKET_BASE_URL;
  return undefined;
}

export const alwaysOAuthProviderKeys = ['antigravity', 'github_copilot', 'xai_subscription'];

export function isOfficialCodexChannel(channel: { credentials?: { apiKey?: string | null } | null }): boolean {
  try {
    const apiKey = channel.credentials?.apiKey || '';
    const json = JSON.parse(apiKey);
    return !!((json.access_token && json.refresh_token) || (json.tokens?.access_token && json.tokens?.refresh_token));
  } catch {
    return false;
  }
}

export function isOfficialClaudeCodeChannel(channel: { credentials?: { apiKey?: string | null } | null; baseURL: string }): boolean {
  const apiKey = channel.credentials?.apiKey || '';
  const defaultURL = getDefaultBaseURL('claudecode');
  return apiKey.includes('sk-ant-oat') || apiKey.includes('sk-ant-api03') || channel.baseURL === defaultURL;
}

export function extractCodexAuthJSONText(apiKey: string | null | undefined): string | undefined {
  if (!apiKey) return undefined;

  try {
    const parsed = JSON.parse(apiKey);
    return parsed?.tokens?.access_token ? apiKey : undefined;
  } catch {
    return undefined;
  }
}




export function recallQuotaRoutingMode(settings: ChannelSettings | null | undefined): ChannelQuotaRoutingMode {
  return settings?.quotaRoutingMode ?? 'INHERIT';
}

export function quotaRoutingModeSettingsPatch(mode: ChannelQuotaRoutingMode): Partial<ChannelSettings> {
  return mode === 'INHERIT' ? { quotaRoutingMode: 'INHERIT' } : { quotaRoutingMode: mode };
}

export function formatMaxSingleChannelRetries(value: number | null | undefined): string {
  return value == null ? '' : String(value);
}

export function parseMaxSingleChannelRetriesInput(value: string): { valid: boolean; value: number | null } {
  const trimmed = value.trim();
  if (!trimmed) {
    return { valid: true, value: null };
  }

  if (!/^\d+$/.test(trimmed)) {
    return { valid: false, value: null };
  }

  // The editor bounds the override it accepts; reads stay permissive so an
  // out-of-range value stored through the API cannot break the channel list.
  const parsed = Number(trimmed);
  if (!Number.isSafeInteger(parsed) || parsed > 65535) {
    return { valid: false, value: null };
  }

  return { valid: true, value: parsed };
}

export function isVertexChannelType(channelType: ChannelType | undefined): boolean {
  return channelType === 'gemini_vertex';
}

export function emptyVertexAPIKey(): VertexAPIKeyCredential {
  return { projectID: '', apiKey: '', region: 'global' };
}

export function getInitialBaseURL(row: Pick<Channel, 'type' | 'baseURL' | 'credentials'>): string {
  if (row.type !== 'codex') return row.baseURL;
  return isOfficialCodexChannel(row) ? getDefaultBaseURL('codex') : toHttpBaseURL(row.baseURL);
}

export function parseOauthToken(oauthApiKey: string): string {
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
}

export function getCodexThirdPartySubmitCredentials<T extends { apiKey?: string | null; apiKeys?: string[] | null }>(
  credentials: T,
  channel: { type?: ChannelType | null; authMode: string }
): T {
  if (channel.type !== 'codex' || channel.authMode !== 'third-party') return credentials;

  const { apiKey: _inheritedOAuthCredentials, ...thirdPartyCredentials } = credentials;
  return thirdPartyCredentials as T;
}

export function getFetchModelsApiKey(input: {
  type?: string | null;
  authMode: string;
  oauthApiKey?: string | null;
  apiKeys?: (string | null | undefined)[] | null;
}): string {
  const plainApiKey = (input.apiKeys || []).find((key) => (key || '').trim().length > 0)?.trim() || '';
  const oauthApiKey = input.oauthApiKey || '';

  if (input.type === 'codex' && input.authMode === 'third-party') {
    return plainApiKey;
  }

  // OAuth JSON is sent whole so the backend can detect isOAuthJSON.
  if (oauthApiKey.trimStart().startsWith('{')) {
    return oauthApiKey;
  }

  return parseOauthToken(oauthApiKey) || plainApiKey;
}

export function getCodexThirdPartyCredentialError(channel: {
  type?: string | null;
  authMode: string;
  apiKeys?: (string | null | undefined)[] | null;
}): string | undefined {
  if (channel.type !== 'codex' || channel.authMode !== 'third-party') return undefined;

  const apiKeys = (channel.apiKeys || []).map((key) => (key || '').trim()).filter((key) => key.length > 0);
  if (apiKeys.length === 0) {
    return 'channels.dialogs.oauth.errors.codexThirdPartyApiKeyRequired';
  }
  if (apiKeys.some((key) => key.startsWith('{'))) {
    return 'channels.dialogs.oauth.errors.codexThirdPartyApiKeyInvalid';
  }

  return undefined;
}

type VertexAPIKeyCredential = NonNullable<NonNullable<Channel["credentials"]>["vertexApiKeys"]>[number];
