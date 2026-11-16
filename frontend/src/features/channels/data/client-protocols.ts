export const CLIENT_PROTOCOLS = [
  { value: 'openai/chat_completions', label: 'Chat Completions', path: 'POST /v1/chat/completions' },
  { value: 'openai/responses', label: 'Responses', path: 'POST /v1/responses · HTTP / WebSocket' },
  { value: 'anthropic/messages', label: 'Messages', path: 'POST /v1/messages' },
  { value: 'gemini/contents', label: 'Gemini', path: 'POST /v1beta/models/{model}:generateContent' },
] as const;

export function protocolLabel(format: string): string {
  if (format === 'openai/responses-ws') return 'Responses · WebSocket';
  return CLIENT_PROTOCOLS.find((p) => p.value === format)?.label ?? format;
}
