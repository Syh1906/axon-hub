const key = 'channels-default-provider-v1';

export function resolveChannelProviderSelection(value: string, typeToProvider: Record<string, string>) {
  if (value === 'all') return undefined;
  const isImage = value.endsWith(':image');
  const provider = isImage ? value.slice(0, -6) : value;
  const types = Object.keys(typeToProvider).filter((type) => typeToProvider[type] === provider);
  return types.length > 0 ? { provider, types, isImage } : undefined;
}

export function readDefaultProvider(typeToProvider: Record<string, string>): string {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(key) ?? '"all"');
    return typeof value === 'string' && resolveChannelProviderSelection(value, typeToProvider) ? value : 'all';
  } catch {
    return 'all';
  }
}

export function saveDefaultProvider(provider: string) {
  localStorage.setItem(key, JSON.stringify(provider));
}
