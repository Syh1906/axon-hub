import { useMemo, memo } from 'react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { useHorizontalScroll } from '@/hooks/use-horizontal-scroll';
import { CHANNEL_CONFIGS } from '../data/config_channels';
import { PROVIDER_CONFIGS } from '../data/config_providers';
import type { ChannelType } from '../data/schema';
import type { ChannelTypeGroup } from '../utils/group-channel-types';

interface ChannelsTypeTabsProps {
  groups: ChannelTypeGroup[];
  selectedTab: string;
  onTabChange: (tab: string) => void;
  className?: string;
}

const MAX_VISIBLE_GROUPS = 8;

export const ChannelsTypeTabs = memo(function ChannelsTypeTabs({ groups, selectedTab, onTabChange, className }: ChannelsTypeTabsProps) {
  const { t } = useTranslation();
  const scrollRef = useHorizontalScroll<HTMLDivElement>();

  const visibleGroups = useMemo(() => {
    const visible = groups.slice(0, MAX_VISIBLE_GROUPS);
    const selected = groups.find((group) => group.key === selectedTab);
    if (selected && !visible.includes(selected)) visible[visible.length - 1] = selected;
    return visible;
  }, [groups, selectedTab]);

  const totalCount = useMemo(() => groups.reduce((sum, { totalCount }) => sum + totalCount, 0), [groups]);

  if (groups.length === 0) {
    return null;
  }

  const getIcon = (group: ChannelTypeGroup) => {
    return PROVIDER_CONFIGS[group.provider]?.icon ?? CHANNEL_CONFIGS[group.types[0] as ChannelType]?.icon;
  };

  const getLabel = (group: ChannelTypeGroup) => {
    const providerKey = `channels.providers.${group.provider}`;
    const translated = t(providerKey);
    const vendorLabel = translated !== providerKey ? translated : t(`channels.types.${group.types[0]}`, { defaultValue: group.types[0] });
    if (group.primaryApiFormat) {
      return `${vendorLabel} · ${t('channels.tabs.imageSuffix')}`;
    }
    return vendorLabel;
  };

  return (
    <div className={cn('mb-6 w-full overflow-hidden', className)}>
      <div
        ref={scrollRef}
        className='hide-scroll flex flex-nowrap items-center gap-2 overflow-x-auto scroll-smooth'
      >
        {/* All tab */}
        <button
          onClick={() => onTabChange('all')}
          className={cn(
            'flex shrink-0 items-center gap-2 rounded-full px-4 py-1.5 text-sm font-medium whitespace-nowrap transition-all',
            selectedTab === 'all'
              ? 'bg-primary text-primary-foreground shadow-primary/20 shadow-md'
              : 'bg-card border-border text-foreground hover:border-primary hover:text-primary border'
          )}
        >
          {t('channels.tabs.all')}{' '}
          <span
            className={cn(
              'bg-muted text-muted-foreground ml-1 rounded-full px-1.5 text-xs',
              selectedTab === 'all' && 'bg-primary-foreground/20 text-primary-foreground'
            )}
          >
            {totalCount}
          </span>
        </button>

        {/* Provider tabs */}
        {visibleGroups.map((group) => {
          const Icon = getIcon(group);
          return (
            <button
              key={group.key}
              onClick={() => onTabChange(group.key)}
              className={cn(
                'flex shrink-0 items-center gap-2 rounded-full px-4 py-1.5 text-sm font-medium whitespace-nowrap transition-all',
                selectedTab === group.key
                  ? 'bg-primary text-primary-foreground shadow-primary/20 shadow-md'
                  : 'bg-card border-border text-foreground hover:border-primary hover:text-primary border'
              )}
            >
              {Icon && <Icon size={16} />}
              {getLabel(group)}{' '}
              <span
                className={cn(
                  'bg-muted text-muted-foreground rounded-full px-1.5 text-xs',
                  selectedTab === group.key && 'bg-primary-foreground/20 text-primary-foreground'
                )}
              >
                {group.totalCount}
              </span>
            </button>
          );
        })}
      </div>
    </div>
  );
});
