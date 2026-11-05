import { useTranslation } from 'react-i18next';
import { formatNumber } from '@/utils/format-number';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';

export function CompactTokens({ value }: { value: number }) {
  const { i18n } = useTranslation();
  const exact = new Intl.NumberFormat(i18n.language).format(value);
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span tabIndex={0} className='cursor-help tabular-nums' aria-label={`${exact} Token`}>
          {formatNumber(value, { digits: 2 })}
        </span>
      </TooltipTrigger>
      <TooltipContent>{exact} Token</TooltipContent>
    </Tooltip>
  );
}
