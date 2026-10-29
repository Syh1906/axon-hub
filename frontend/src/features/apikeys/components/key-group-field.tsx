import { useTranslation } from 'react-i18next';
import { usePermissions } from '@/hooks/usePermissions';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { useKeyGroups } from '../data/groups';

export function KeyGroupField({
  value,
  onChange,
  container,
}: {
  value?: number | null;
  onChange: (id?: number) => void;
  container: HTMLDivElement | null;
}) {
  const { t } = useTranslation();
  const { hasProjectScope } = usePermissions();
  const groups = useKeyGroups();
  if (!hasProjectScope('write_users') || !hasProjectScope('write_roles')) return null;
  return (
    <div className='flex flex-col gap-2'>
      <Label htmlFor='api-key-group'>{t('apikeys.groups.title')}</Label>
      <Select value={String(value ?? 0)} onValueChange={(id) => onChange(Number(id) || undefined)}>
        <SelectTrigger id='api-key-group'>
          <SelectValue />
        </SelectTrigger>
        <SelectContent container={container}>
          <SelectGroup>
            <SelectItem value='0'>{t('apikeys.groups.none')}</SelectItem>
            {groups.data?.map((group) => (
              <SelectItem key={group.id} value={String(group.id)}>
                {group.name}
              </SelectItem>
            ))}
          </SelectGroup>
        </SelectContent>
      </Select>
      {groups.error && <p role='alert'>{groups.error.message}</p>}
    </div>
  );
}
