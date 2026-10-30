import {
  getDefaultBaseURL,
  CHANNEL_CONFIGS,
  OPENAI_CHAT_COMPLETIONS,
  OPENAI_RESPONSES,
  OPENAI_IMAGE_GENERATION,
} from '../data/config_channels';
import { getInitialApiFormatForChannel } from '../data/protocol-options';
import { Channel, ChannelType, ApiFormat, RetryableErrorPattern } from '../data/schema';

export interface Props {
  currentRow?: Channel;
  duplicateFromRow?: Channel;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  showModelsPanel?: boolean;
}

export const MAX_MODELS_DISPLAY = 2;

const duplicateNameRegex = /^(.*) \((\d+)\)$/;

export type ApiFormatOption = ApiFormat;

export type ResponsesTransport = 'http' | 'websocket';

export const OPENAI_RESPONSES_WEBSOCKET: ApiFormatOption = 'openai/responses-ws';

const OPENAI_RESPONSES_WEBSOCKET_BASE_URL = 'https://api.openai.com/v1#';

const CODEX_RESPONSES_WEBSOCKET_BASE_URL = 'https://chatgpt.com/backend-api/codex#';

function getResponsesTransportFromBaseURL(baseURL?: string): ResponsesTransport {
  return baseURL?.trim().toLowerCase().startsWith('ws') ? 'websocket' : 'http';
}

export function baseURLMatchesResponsesTransport(baseURL: string | undefined, _transport: ResponsesTransport): boolean {
  const normalized = baseURL?.trim().toLowerCase() ?? '';
  return /^(https?|wss?):\/\//.test(normalized);
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

export function getResponsesTransportFromChannel(channel?: Pick<Channel, 'baseURL' | 'endpoints'>): ResponsesTransport {
  const responsesEndpoint = channel?.endpoints?.find((endpoint) => endpoint.apiFormat === OPENAI_RESPONSES);
  if (responsesEndpoint?.supportsWebSockets != null) return responsesEndpoint.supportsWebSockets ? 'websocket' : 'http';
  if (responsesEndpoint?.transport === 'http' || responsesEndpoint?.transport === 'websocket') return responsesEndpoint.transport;
  return getResponsesTransportFromBaseURL(responsesEndpoint?.baseURL || channel?.baseURL);
}

export function getResponsesWebSocketBaseURL(channelType: ChannelType): string | undefined {
  if (channelType === 'codex') return CODEX_RESPONSES_WEBSOCKET_BASE_URL;
  if (channelType === 'openai_responses') return OPENAI_RESPONSES_WEBSOCKET_BASE_URL;
  return undefined;
}

function getDuplicateBaseName(name: string) {
  const match = name.match(duplicateNameRegex);
  if (match?.[1]) {
    return match[1];
  }
  return name;
}

export function getNextDuplicateName(name: string, existingNames: Set<string>) {
  const baseName = getDuplicateBaseName(name);
  let i = 1;
  for (;;) {
    const candidate = `${baseName} (${i})`;
    if (!existingNames.has(candidate)) {
      return candidate;
    }
    i++;
  }
}

export const alwaysOAuthProviderKeys = ['antigravity', 'github_copilot', 'xai_subscription'];

export function isOfficialCodexChannel(channel: { credentials?: { apiKey?: string } }): boolean {
  try {
    const apiKey = channel.credentials?.apiKey || '';
    const json = JSON.parse(apiKey);
    return !!((json.access_token && json.refresh_token) || (json.tokens?.access_token && json.tokens?.refresh_token));
  } catch {
    return false;
  }
}

export function isOfficialClaudeCodeChannel(channel: { credentials?: { apiKey?: string }; baseURL: string }): boolean {
  const apiKey = channel.credentials?.apiKey || '';
  const defaultURL = getDefaultBaseURL('claudecode');
  return apiKey.includes('sk-ant-oat') || apiKey.includes('sk-ant-api03') || channel.baseURL === defaultURL;
}

export function extractCodexAuthJSONText(apiKey: string | undefined): string | undefined {
  if (!apiKey) return apiKey;

  try {
    const parsed = JSON.parse(apiKey);
    return parsed?.tokens?.access_token ? apiKey : undefined;
  } catch {
    return undefined;
  }
}
