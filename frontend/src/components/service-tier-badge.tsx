import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';

export function ServiceTierBadge({ mode, placement }: { mode: 'fast' | 'ultrafast'; placement: 'model' | 'cost' }) {
  const { t } = useTranslation();
  const tier = t(`serviceTier.mode.${mode}`);
  const sent = placement === 'model';
  const description = t(sent ? 'serviceTier.badge.sent' : 'serviceTier.badge.billed', { tier });
  const badge = (
    <Badge
      variant='outline'
      role='img'
      aria-label={description}
      tabIndex={sent ? 0 : undefined}
      className='border-orange-200 bg-orange-50 px-1 font-mono text-[10px] leading-tight text-orange-700 dark:border-orange-800 dark:bg-orange-950/30 dark:text-orange-300'
    >
      {tier}
    </Badge>
  );

  // Cost badges already open the amount's pricing breakdown on hover or focus.
  if (!sent) return badge;
  return (
    <Tooltip>
      <TooltipTrigger asChild>{badge}</TooltipTrigger>
      <TooltipContent>{description}</TooltipContent>
    </Tooltip>
  );
}
