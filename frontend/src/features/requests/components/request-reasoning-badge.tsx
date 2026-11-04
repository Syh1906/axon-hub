import { Badge } from '@/components/ui/badge';

const REASONING_EFFORT_STYLES = new Map<string, string>([
  ['none', 'border-zinc-200 bg-zinc-50 text-zinc-600 dark:border-zinc-700 dark:bg-zinc-900/40 dark:text-zinc-300'],
  ['minimal', 'border-stone-200 bg-stone-50 text-stone-700 dark:border-stone-700 dark:bg-stone-900/40 dark:text-stone-300'],
  [
    'auto',
    'border-sky-300 bg-gradient-to-r from-cyan-50 via-sky-50 to-indigo-50 text-sky-800 dark:border-sky-700 dark:from-cyan-950/40 dark:via-sky-950/40 dark:to-indigo-950/40 dark:text-sky-200',
  ],
  ['low', 'border-slate-200 bg-slate-50 text-slate-700 dark:border-slate-700 dark:bg-slate-900/40 dark:text-slate-300'],
  ['medium', 'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-300'],
  ['high', 'border-rose-200 bg-rose-50 text-rose-700 dark:border-rose-800 dark:bg-rose-950/30 dark:text-rose-300'],
  ['xhigh', 'border-red-200 bg-red-50 text-red-700 dark:border-red-800 dark:bg-red-950/30 dark:text-red-300'],
  ['max', 'border-red-300 bg-red-100 text-red-800 dark:border-red-700 dark:bg-red-950/40 dark:text-red-200'],
]);

const DEFAULT_REASONING_STYLE =
  'border-muted-foreground/20 bg-muted/40 text-muted-foreground dark:border-muted-foreground/30 dark:bg-muted/20';

export function RequestReasoningBadge({ effort }: { effort: string }) {
  return (
    <Badge variant='outline' className={REASONING_EFFORT_STYLES.get(effort.trim().toLowerCase()) ?? DEFAULT_REASONING_STYLE}>
      {effort}
    </Badge>
  );
}
