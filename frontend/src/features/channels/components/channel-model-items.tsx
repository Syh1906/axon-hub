import { ManualModelBadge } from './manual-model-badge';
import { memo } from 'react';
import { X } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';

export const FetchedModelItem = memo(
  ({
    model,
    isAdded,
    isSelected,
    onToggle,
    addedLabel,
    willRemoveLabel,
  }: {
    model: string;
    isAdded: boolean;
    isSelected: boolean;
    onToggle: () => void;
    addedLabel: string;
    willRemoveLabel: string;
  }) => (
    <div
      className={`flex items-center gap-2 rounded-md p-2 text-sm transition-colors ${
        isAdded && !isSelected
          ? 'bg-muted/50 text-muted-foreground'
          : isSelected
            ? 'bg-primary/10 border-primary/30 border'
            : 'hover:bg-accent cursor-pointer'
      }`}
    >
      <Checkbox checked={isSelected} onCheckedChange={onToggle} />
      <Tooltip>
        <TooltipTrigger asChild>
          <span className='flex-1 cursor-pointer truncate' onClick={onToggle}>
            {model}
          </span>
        </TooltipTrigger>
        <TooltipContent>
          <p className='max-w-xs break-all'>{model}</p>
        </TooltipContent>
      </Tooltip>
      {isAdded && !isSelected && (
        <Badge variant='secondary' className='shrink-0 text-xs'>
          {addedLabel}
        </Badge>
      )}
      {isAdded && isSelected && (
        <Badge variant='destructive' className='shrink-0 text-xs'>
          {willRemoveLabel}
        </Badge>
      )}
    </div>
  )
);

FetchedModelItem.displayName = 'FetchedModelItem';

export const SupportedModelItem = memo(({ model, isManual, onRemove }: { model: string; isManual: boolean; onRemove: () => void }) => (
  <div className='hover:bg-accent flex items-center gap-2 rounded-md p-2 text-sm'>
    <Tooltip>
      <TooltipTrigger asChild>
        <span className='w-0 flex-1 cursor-help truncate'>{model}</span>
      </TooltipTrigger>
      <TooltipContent>
        <p className='max-w-xs break-all'>{model}</p>
      </TooltipContent>
    </Tooltip>
    <ManualModelBadge isManual={isManual} />
    <Button type='button' variant='ghost' size='sm' className='hover:text-destructive h-6 w-6 shrink-0 p-0' onClick={onRemove}>
      <X className='h-3 w-3' />
    </Button>
  </div>
));

SupportedModelItem.displayName = 'SupportedModelItem';
