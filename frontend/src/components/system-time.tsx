import type { TimePattern } from '@/utils/system-time';
import { useSystemTime } from '@/hooks/use-system-time';

export function SystemTime({ value, pattern = 'yyyy-MM-dd HH:mm:ss' }: { value: Date | string | number; pattern?: TimePattern }) {
  const { timezone, format } = useSystemTime();
  return <time title={timezone}>{format(value, pattern)}</time>;
}
