import type { ChannelEndpoint } from './schema';

const paths: Record<string, [string, string]> = {
  'openai/chat_completions': ['Chat Completions', '/chat/completions'],
  'openai/responses': ['Responses', '/responses'],
  'openai/responses-ws': ['Responses WebSocket', '/responses'],
  'anthropic/messages': ['Messages', '/messages'],
  'openai/image_generation': ['Images · Generations', '/images/generations'],
  'openai/image_edit': ['Images · Edits', '/images/edits'],
  'openai/image_variation': ['Images · Variations', '/images/variations'],
  'openai/embeddings': ['Embeddings', '/embeddings'],
  'openai/moderations': ['Moderations', '/moderations'],
  'openai/audio_speech': ['Audio · Speech', '/audio/speech'],
  'openai/audio_transcriptions': ['Audio · Transcriptions', '/audio/transcriptions'],
  'openai/audio_translations': ['Audio · Translations', '/audio/translations'],
};

export const endpointName = (format: string) => paths[format]?.[0] ?? format;
export const endpointDefaultPath = (format: string) => paths[format]?.[1] ?? '';

// 与常见 OpenAI/Anthropic transformer 的版本补全及自定义路径规则一致。
export function endpointRequestURL(base: string, endpoint: ChannelEndpoint) {
  const path = endpoint.path || endpointDefaultPath(endpoint.apiFormat);
  if (base.endsWith('##')) return base.slice(0, -2).replace(/\/+$/, '');
  if (!path) return null;
  const skipVersion = !!endpoint.path || base.endsWith('#');
  let normalized = base.replace(/#$/, '').replace(/\/+$/, '');
  if (!skipVersion && !normalized.endsWith('/v1') && !normalized.includes('/v1/')) normalized += '/v1';
  return normalized + path;
}

export function mergeEndpoints(defaults: ChannelEndpoint[], overrides: ChannelEndpoint[]) {
  const merged = new Map(defaults.map((endpoint) => [endpoint.apiFormat, endpoint]));
  overrides.forEach((endpoint) => merged.set(endpoint.apiFormat, endpoint));
  return [...merged.values()];
}
