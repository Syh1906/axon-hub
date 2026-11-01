export function getPayloadState(body: unknown): string | undefined {
  if (!body || typeof body !== 'object' || !('_axon_payload' in body)) return undefined;
  const value = body._axon_payload;
  if (!value || typeof value !== 'object' || !('version' in value) || value.version !== 1 || !('state' in value)) return undefined;
  return typeof value.state === 'string' ? value.state : undefined;
}
