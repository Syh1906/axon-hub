import i18n from '@/lib/i18n';

export function managementError(message: string) {
  const known: Record<string, string> = {
    'group name already exists': 'duplicateGroup',
    'group changed; reload before saving': 'stale',
    'key changed; reload before saving': 'stale',
    'budget changed; reload before saving': 'stale',
    'group name required': 'nameRequired',
    'name must contain 1 to 128 characters': 'nameRequired',
    'key name must contain 1 to 128 characters': 'nameRequired',
    'service account scopes must be explicitly selected': 'scopesRequired',
  };
  if (known[message]) return i18n.t('kg.error.' + known[message]);
  if (message.startsWith('duplicate name in submitted scope:')) return i18n.t('kg.error.duplicateRow');
  const name = message.match(/name '(.*)' already exists/)?.[1] ?? '';
  if (message.startsWith('cannot move group members to ungrouped:')) return i18n.t('kg.error.deleteConflict', { name });
  if (message.includes('API Key in target group') && message.endsWith('already exists')) return i18n.t('kg.error.duplicateKey', { name });
  if (message.startsWith('duplicate key name in group:')) return i18n.t('kg.error.duplicateKey');
  return message;
}
