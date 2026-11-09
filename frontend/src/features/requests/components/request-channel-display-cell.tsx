import { CirclePlus, Link2, RotateCcw } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { isRequestChannelSessionState } from '../data/schema';

export type ChannelDisplay = {
  name?: string | null;
  provider?: string;
  tags: string[];
  sessionState?: string;
  multiplier?: number | 'mixed' | null;
};

export function RequestChannelDisplayCell({
  channel,
  onOpen,
  credentials,
}: {
  channel: ChannelDisplay | null | undefined;
  onOpen?: () => void;
  credentials?: { keyHint?: string | null; keyRemark?: string | null };
}) {
  const { t } = useTranslation();
  if (!channel?.name) return <span className='text-muted-foreground'>—</span>;
  const { name, tags, provider, multiplier, sessionState: state } = channel;
  const sessionLabel = isRequestChannelSessionState(state) ? t(`requests.diagnostics.sessions.${state}`) : undefined;
  const multiplierLabel = multiplier === 'mixed' ? t('requests.diagnostics.mixedMultiplier') : multiplier == null ? '—' : `×${multiplier}`;
  const Trigger = onOpen ? 'button' : 'div';
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Trigger
          type={onOpen ? 'button' : undefined}
          tabIndex={onOpen ? undefined : 0}
          onClick={onOpen}
          className='flex w-[170px] min-w-0 flex-col gap-1.5 rounded-md text-left'
          aria-label={onOpen ? t('requests.diagnostics.openRouting', { name }) : `${t('requests.columns.channel')}: ${name}`}
        >
          <span className='flex w-full items-center gap-1.5'>
            {state === 'new' && <CirclePlus className='size-4 shrink-0 text-emerald-600 dark:text-emerald-400' />}
            {state === 'reused' && <Link2 className='size-4 shrink-0 text-violet-600 dark:text-violet-400' />}
            {state === 'returned' && <RotateCcw className='text-primary size-4 shrink-0' />}
            <span className='truncate text-sm font-medium'>{name}</span>
            <Badge variant='outline' className='shrink-0 px-1 text-[10px]'>
              {multiplierLabel}
            </Badge>
          </span>
          <span className='flex h-5 w-full min-w-0 items-center gap-1'>
            {tags.length ? (
              <>
                <Badge variant='secondary' className='max-w-[106px] truncate'>
                  {tags[0]}
                </Badge>
                {tags.length > 1 && <span className='text-muted-foreground shrink-0 text-xs'>+{tags.length - 1}</span>}
              </>
            ) : (
              <span className='text-muted-foreground text-xs'>—</span>
            )}
          </span>
        </Trigger>
      </TooltipTrigger>
      <TooltipContent
        side='bottom'
        align='start'
        className='bg-popover text-popover-foreground max-w-[min(390px,90vw)] border p-3 shadow-md'
      >
        <div className='flex flex-col gap-2 text-xs'>
          <strong className='text-sm break-words'>
            {name}
            {provider ? ` · ${t(`channels.providers.${provider}`, { defaultValue: provider })}` : ''}
          </strong>
          {sessionLabel && (
            <p>
              {t('requests.diagnostics.session')}: {sessionLabel}
            </p>
          )}
          {credentials && (
            <p className='break-all'>
              {t('requests.diagnostics.key')}:{' '}
              {credentials.keyHint
                ? [credentials.keyRemark, credentials.keyHint].filter(Boolean).join(' · ')
                : t('requests.diagnostics.unrecorded')}
            </p>
          )}
          <p className='break-words'>
            {t('requests.diagnostics.tags')}: {tags.join(' / ') || '—'}
          </p>
          <p>
            {t('requests.diagnostics.requestMultiplier')}: {multiplier == null ? t('requests.diagnostics.unrecorded') : multiplierLabel}
          </p>
          {onOpen && <p className='text-muted-foreground'>{t('requests.diagnostics.openRoutingHint')}</p>}
        </div>
      </TooltipContent>
    </Tooltip>
  );
}
