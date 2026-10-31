import type { ComponentProps } from 'react';
import { TableRow } from '@/components/ui/table';
import { cn } from '@/lib/utils';

// Keep the existing expand button as the keyboard entry point. Other controls
// (including content rendered in portals) must not also toggle the row.
export function ExpandableTableRow({ expanded, onToggle, className, ...props }: Omit<ComponentProps<typeof TableRow>, 'onClick'> & {
  expanded: boolean;
  onToggle?: () => void;
}) {
  return (
    <TableRow
      {...props}
      aria-expanded={onToggle ? expanded : undefined}
      className={cn(onToggle && 'cursor-pointer', className)}
      onClick={(event) => {
        const target = event.target;
        if (!onToggle || event.defaultPrevented || !(target instanceof Element) || !event.currentTarget.contains(target)) return;
        if (target.closest('button, a, input, select, textarea, [role="button"], [role="checkbox"], [role="switch"], [role="menuitem"], [contenteditable="true"]')) return;
        onToggle();
      }}
    />
  );
}
