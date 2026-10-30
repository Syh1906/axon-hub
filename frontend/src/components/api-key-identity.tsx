import { formatUserName } from '@/lib/utils';

export type APIKeyIdentityValue = {
  name?: string | null;
  groupName?: string | null;
  user?: { firstName?: string | null; lastName?: string | null } | null;
};
export function apiKeyOwnerLabel(key: APIKeyIdentityValue) {
  return key.groupName || formatUserName(key.user?.firstName, key.user?.lastName);
}
export function apiKeyLabel(key: APIKeyIdentityValue) {
  const owner = apiKeyOwnerLabel(key);
  return [key.name, owner].filter(Boolean).join(' · ');
}
export function APIKeyIdentity({ value }: { value: APIKeyIdentityValue }) {
  const owner = apiKeyOwnerLabel(value);
  return (
    <div className='flex min-w-0 flex-col gap-0.5'>
      <span className='truncate font-medium' title={owner || value.name || undefined}>
        {owner || value.name || '—'}
      </span>
      {owner && (
        <span className='text-muted-foreground truncate text-xs' title={value.name ?? undefined}>
          {value.name || '—'}
        </span>
      )}
    </div>
  );
}
