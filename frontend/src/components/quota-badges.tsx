import { useEffect, useMemo, useState } from 'react';
import { Loader2, RefreshCw, BatteryWarning, ArrowUpDown } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Checkbox } from '@/components/ui/checkbox';
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuRadioGroup, DropdownMenuRadioItem } from '@/components/ui/dropdown-menu';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { useProviderQuotaStatuses, ProviderQuotaChannel } from '@/features/system/data/quotas';
import { useQuotaRoutingSettings } from '@/features/system/data/system';
import { buildEffectiveModeByChannel, BatteryLevel, getBatteryIcon, getBatteryLevel, isOpenaiType, isCommandCodeType, isOllamaType, isMinimaxType, isZenmuxType, getChannelPercentage } from './quota-display';
import { QuotaRow } from './quota-row';

function QuotaBadgeTrigger({ channels, isLoading, isError }: { channels: ProviderQuotaChannel[]; isLoading?: boolean; isError?: boolean }) {
  if (isLoading) {
    return <Loader2 className='text-muted-foreground h-5 w-5 animate-spin' />;
  }

  if (isError) {
    return <BatteryWarning className='h-5 w-5 text-red-500 transition-colors' />;
  }

  const highestUsed = Math.max(...channels.map(getChannelPercentage));

  const hasExhausted = channels.some((c) => c.quotaStatus.status === 'exhausted');
  const hasWarning = channels.some((c) => c.quotaStatus.status === 'warning');

  let level: BatteryLevel = 'full';
  if (hasExhausted) level = 'warning';
  else if (hasWarning) level = 'low';
  else level = getBatteryLevel(highestUsed, 'available');

  const BatteryIcon = getBatteryIcon(level);
  const isWarning = level === 'warning';
  const textColor = isWarning ? 'text-red-500' : level === 'low' ? 'text-yellow-500' : 'text-muted-foreground';

  return <BatteryIcon className={`h-5 w-5 ${textColor} transition-colors`} />;
}

// The popover never exposes t() publicly, so the contract stays local to this
// module: a plain translate function from useTranslation().
type TranslateFn = ReturnType<typeof useTranslation>['t'];

type QuotaSortMode = 'preferred' | 'alphabetical' | 'created';

const QUOTA_GROUPED_STORAGE_KEY = 'provider-quota-grouped';
const QUOTA_SORT_MODE_STORAGE_KEY = 'provider-quota-sort-mode';
// Above this many channels the popover widens into the two-column layout and
// grows the grouping controls; every visibility gate reads this one constant.
const WIDE_QUOTA_POPOVER_CHANNEL_COUNT = 4;

// Preferences persist the same way the channels table view options do:
// best-effort localStorage reads at mount and writes on change, silently
// ignoring unavailable or full storage so the in-memory choice still works.
function readStoredBoolean(key: string, fallback: boolean): boolean {
  try {
    const stored = localStorage.getItem(key);
    if (stored === 'true') return true;
    if (stored === 'false') return false;
    return fallback;
  } catch {
    return fallback;
  }
}

function readStoredSortMode(key: string): QuotaSortMode {
  try {
    const stored = localStorage.getItem(key);
    return stored === 'alphabetical' || stored === 'created' ? stored : 'preferred';
  } catch {
    return 'preferred';
  }
}

function writeStoredPreference(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Storage can be disabled or full; the in-memory preference still works.
  }
}

function getQuotaSortLabelKey(sortMode: QuotaSortMode): string {
  switch (sortMode) {
    case 'preferred':
      return 'system.providerQuota.sort.channelList';
    case 'alphabetical':
      return 'system.providerQuota.sort.alphabetical';
    case 'created':
      return 'system.providerQuota.sort.created';
  }
}

type QuotaCategory = {
  key: string;
  label: string;
};

function getChannelCategory(channel: ProviderQuotaChannel, t: TranslateFn): QuotaCategory {
  const channelType = channel.type as string;
  if (channelType === 'antigravity') {
    return { key: 'antigravity', label: t('channels.types.antigravity') || 'Antigravity' };
  }
  if (channelType === 'codex') {
    return { key: 'codex', label: t('channels.types.codex') || 'Codex' };
  }
  if (channelType === 'claudecode') {
    return { key: 'claudecode', label: t('channels.types.claudecode') || 'Claude Code' };
  }
  if (channelType === 'github_copilot') {
    return { key: 'github_copilot', label: t('channels.types.github_copilot') || 'GitHub Copilot' };
  }
  if (channelType === 'cline') {
    return { key: 'cline', label: t('channels.types.cline') || 'Cline' };
  }
  if (channelType === 'xai_subscription') {
    return { key: 'xai_subscription', label: t('channels.types.xai_subscription') || 'xAI' };
  }
  if (channelType === 'nanogpt' || channelType === 'nanogpt_responses') {
    return { key: 'nanogpt', label: 'NanoGPT' };
  }
  if (isOllamaType(channelType)) {
    return { key: 'ollama', label: t('channels.types.ollama') || 'Ollama' };
  }
  if (isCommandCodeType(channelType)) {
    return { key: 'commandcode', label: t('channels.types.commandcode') || 'Command Code' };
  }
  if (channelType === 'moonshot_coding') {
    return { key: 'moonshot_coding', label: t('channels.types.moonshot_coding') || 'Kimi Coding' };
  }
  if (isMinimaxType(channelType)) {
    return { key: 'minimax', label: t('channels.types.minimax') || 'Minimax' };
  }
  if (channelType === 'zhipu' || channelType === 'zhipu_anthropic' || channelType === 'zai' || channelType === 'zai_anthropic') {
    return { key: 'zhipu', label: t('channels.types.zhipu') || '智谱' };
  }
  if (isZenmuxType(channelType)) {
    return { key: 'zenmux', label: 'ZenMux' };
  }
  if (channelType === 'opencode_go' || channelType === 'opencode_go_anthropic') {
    return { key: 'opencode_go', label: t('channels.types.opencode_go') || 'OpenCode Go' };
  }
  if (isOpenaiType(channelType)) {
    // OpenAI-compatible providers have dedicated labels in the locales
    // (charm_hyper → "Hyper"), so a raw-slug capitalization would mangle
    // underscored names.
    if (channel.providerType) {
      const key = `system.quota.collection.providers.${channel.providerType}`;
      const providerLabel = t(key);
      return { key: channel.providerType, label: providerLabel !== key ? providerLabel : channel.providerType };
    }
    return { key: 'openai', label: t('channels.types.openai') || 'OpenAI' };
  }
  const typeKey = `channels.types.${channelType}`;
  const translated = t(typeKey);
  const label = translated !== typeKey ? translated : channelType;
  return { key: channelType, label };
}

// A category filter only applies while its controls are visible (grouped view
// in the wide popover). Otherwise rows would vanish with no way to clear the
// selection — for example when the channel count drops below the wide
// threshold while a category is selected.
function resolveActiveQuotaCategory(
  grouped: boolean,
  channelCount: number,
  selectedCategory: string,
  categories: ReadonlyArray<QuotaCategory>
): string {
  if (!grouped || channelCount <= WIDE_QUOTA_POPOVER_CHANNEL_COUNT) return 'all';
  return selectedCategory !== 'all' && categories.some((c) => c.key === selectedCategory) ? selectedCategory : 'all';
}

// Channel-list-order mode mirrors the operator's channel list: the load
// balancer ranks higher OrderingWeight higher, ties fall back to the backend
// default (Channel.ID ASC). Alphabetical mode: category label, then channel
// name; the former hardcoded category priorities had no operational rationale
// and made pill order diverge from what users expect when scanning for a
// provider. Created mode: backend default order (Channel.ID ASC), i.e. the
// pre-pills-pr creation order.
function sortQuotaChannels(channels: readonly ProviderQuotaChannel[], sortMode: QuotaSortMode, t: TranslateFn): ProviderQuotaChannel[] {
  const byCreated = (a: ProviderQuotaChannel, b: ProviderQuotaChannel) => a.id.localeCompare(b.id, 'en', { numeric: true });
  const compare =
    sortMode === 'alphabetical'
      ? (a: ProviderQuotaChannel, b: ProviderQuotaChannel) => {
          const catA = getChannelCategory(a, t);
          const catB = getChannelCategory(b, t);
          if (catA.label === catB.label) {
            return catA.key.localeCompare(catB.key) || a.name.localeCompare(b.name);
          }
          return catA.label.localeCompare(catB.label) || a.name.localeCompare(b.name);
        }
      : sortMode === 'preferred'
        ? (a: ProviderQuotaChannel, b: ProviderQuotaChannel) => b.orderingWeight - a.orderingWeight || byCreated(a, b)
        : byCreated;

  return [...channels].sort(compare);
}

// One display item per grid cell: either a full-width section header or a
// channel row. Multi-channel categories need a header as the visual boundary
// (and a pill-filter target); single-channel categories skip the header so
// the two-column grid keeps packing channel rows side-by-side across category
// boundaries instead of stranding an empty cell beside every one-of-a-kind
// channel — which also stopped the "NANOGPT" header / "NanoGPT" row repeat.
type QuotaDisplayItem = { kind: 'header'; cat: QuotaCategory & { count: number } } | { kind: 'row'; channel: ProviderQuotaChannel };

function buildQuotaDisplayItems(
  channels: ProviderQuotaChannel[],
  categoryList: Array<QuotaCategory & { count: number }>,
  activeCategory: string,
  t: TranslateFn
): QuotaDisplayItem[] {
  if (activeCategory !== 'all') {
    return channels.map((channel) => ({ kind: 'row' as const, channel }));
  }

  const byCategory = new Map<string, ProviderQuotaChannel[]>();
  for (const channel of channels) {
    const key = getChannelCategory(channel, t).key;
    const members = byCategory.get(key);
    if (members) {
      members.push(channel);
    } else {
      byCategory.set(key, [channel]);
    }
  }

  const items: QuotaDisplayItem[] = [];
  for (const cat of categoryList) {
    const members = byCategory.get(cat.key) ?? [];
    if (members.length > 1) {
      items.push({ kind: 'header', cat });
    }
    for (const channel of members) {
      items.push({ kind: 'row', channel });
    }
  }
  return items;
}

export function QuotaBadges({ isRefreshing, onRefresh }: { isRefreshing: boolean; onRefresh: () => void }) {
  const { t } = useTranslation();
  const { channels, isLoading, isError, error } = useProviderQuotaStatuses();
  const { data: routingSettings } = useQuotaRoutingSettings();
  const [selectedCategory, setSelectedCategory] = useState<string>('all');
  const [grouped, setGrouped] = useState(() => readStoredBoolean(QUOTA_GROUPED_STORAGE_KEY, true));
  const [sortMode, setSortMode] = useState<QuotaSortMode>(() => readStoredSortMode(QUOTA_SORT_MODE_STORAGE_KEY));

  useEffect(() => {
    writeStoredPreference(QUOTA_GROUPED_STORAGE_KEY, String(grouped));
  }, [grouped]);

  useEffect(() => {
    writeStoredPreference(QUOTA_SORT_MODE_STORAGE_KEY, sortMode);
  }, [sortMode]);

  const dedupedChannels = useMemo(
    () =>
      channels.reduce((acc: ProviderQuotaChannel[], channel: ProviderQuotaChannel) => {
        if (channel.type === 'nanogpt_responses') {
          const existing = acc.find((c) => c.type === 'nanogpt');
          if (!existing) {
            acc.push(channel);
          }
        } else if (isOpenaiType(channel.type) && channel.providerType) {
          const existing = acc.find((c) => isOpenaiType(c.type) && c.providerType === channel.providerType);
          if (!existing) {
            acc.push(channel);
          }
        } else if (channel.accountKey) {
          // Channels sharing one ZenMux account (same non-empty accountKey) collapse
          // into a single row: the first channel carries the shared quota, later
          // members only contribute their names to the shared-account note. The
          // representative is a shallow copy so the parsed query data stays untouched.
          const existing = acc.find((c) => c.accountKey === channel.accountKey);
          if (existing) {
            existing.sharedAccountNames = [...(existing.sharedAccountNames ?? [existing.name]), channel.name];
          } else {
            acc.push({ ...channel, sharedAccountNames: [channel.name] });
          }
        } else {
          acc.push(channel);
        }
        return acc;
      }, [] as ProviderQuotaChannel[]),
    [channels]
  );

  const sortedChannels = useMemo(() => sortQuotaChannels(dedupedChannels, sortMode, t), [dedupedChannels, sortMode, t]);

  const categories = useMemo(() => {
    const categoryCounts = sortedChannels.reduce(
      (acc, ch) => {
        const cat = getChannelCategory(ch, t);
        acc[cat.key] = (acc[cat.key] || 0) + 1;
        return acc;
      },
      {} as Record<string, number>
    );

    return Array.from(
      new Map(
        sortedChannels.map((ch) => {
          const cat = getChannelCategory(ch, t);
          return [cat.key, { key: cat.key, label: cat.label, count: categoryCounts[cat.key] }];
        })
      ).values()
    );
  }, [sortedChannels, t]);

  const activeCategory = resolveActiveQuotaCategory(grouped, sortedChannels.length, selectedCategory, categories);

  const groupedChannels = useMemo(
    () => (activeCategory === 'all' ? sortedChannels : sortedChannels.filter((ch) => getChannelCategory(ch, t).key === activeCategory)),
    [activeCategory, sortedChannels, t]
  );
  const displayItems = useMemo(
    () =>
      grouped
        ? buildQuotaDisplayItems(groupedChannels, categories, activeCategory, t)
        : groupedChannels.map((channel) => ({ kind: 'row' as const, channel })),
    [grouped, groupedChannels, categories, activeCategory, t]
  );
  const effectiveModeByChannel = useMemo(
    () => buildEffectiveModeByChannel(channels, dedupedChannels, routingSettings?.defaultMode),
    [channels, dedupedChannels, routingSettings?.defaultMode]
  );

  if (!isLoading && !isError && channels.length === 0) return null;
  const renderContent = () => {
    if (isLoading) {
      return (
        <div className='text-muted-foreground flex items-center gap-2 px-1 py-3 text-sm'>
          <Loader2 className='h-4 w-4 animate-spin' />
          <span>{t('system.providerQuota.loading')}</span>
        </div>
      );
    }

    if (isError) {
      return (
        <div className='rounded bg-red-500/10 p-2 text-xs break-words text-red-500'>
          <span className='font-medium'>{t('system.providerQuota.error')}:</span>{' '}
          {error instanceof Error ? error.message : t('quota.label.unavailable')}
        </div>
      );
    }

    return (
      <div
        className={`max-h-[60vh] overflow-y-auto pr-1 pl-1 ${sortedChannels.length > WIDE_QUOTA_POPOVER_CHANNEL_COUNT ? 'grid grid-cols-1 gap-x-4 sm:grid-cols-2' : ''}`}
      >
        {displayItems.map((item) =>
          item.kind === 'header' ? (
            <div key={`header-${item.cat.key}`} className='col-span-full flex items-center gap-2 pt-3 pb-1 first:pt-1'>
              <span className='text-muted-foreground/80 text-[11px] font-semibold tracking-wider uppercase'>{item.cat.label}</span>
              <span className='bg-muted/70 py-0.2 text-muted-foreground/70 rounded-full px-1.5 text-[10px] font-medium'>
                {item.cat.count}
              </span>
              <div className='bg-border/40 h-[1px] flex-1' />
            </div>
          ) : (
            // Deliberately unmemoized: rows derive reset countdowns and
            // elapsed-time markers from Date.now(), and React Query's
            // structural sharing keeps channel objects identical across
            // unchanged polls — memoizing here froze those clocks.
            <QuotaRow key={item.channel.id} channel={item.channel} effectiveMode={effectiveModeByChannel.get(item.channel.id) ?? null} />
          )
        )}
      </div>
    );
  };

  return (
    <Popover>
      <PopoverTrigger asChild>
        <button type='button' className='hover:bg-muted relative rounded-md p-2 transition-colors'>
          <QuotaBadgeTrigger channels={sortedChannels} isLoading={isLoading} isError={isError} />
        </button>
      </PopoverTrigger>
      <PopoverContent
        className={
          !isLoading && !isError && sortedChannels.length > WIDE_QUOTA_POPOVER_CHANNEL_COUNT
            ? 'w-[640px] max-w-[calc(100vw-2rem)]'
            : 'w-80 max-w-[calc(100vw-2rem)]'
        }
        align='end'
      >
        <div className='space-y-1.5'>
          <div className='flex items-center justify-between'>
            <div className='text-muted-foreground text-xs font-medium tracking-wide uppercase'>{t('system.providerQuota.title')}</div>
            <div className='flex items-center gap-1.5'>
              {sortedChannels.length > WIDE_QUOTA_POPOVER_CHANNEL_COUNT && (
                // Same wide-popover gate as the pill strip: grouping only
                // matters once the list is big enough to scan, and the narrow
                // header stays uncluttered.
                <label className='text-muted-foreground flex cursor-pointer items-center gap-1.5 text-xs select-none'>
                  <Checkbox className='size-3.5' checked={grouped} onCheckedChange={(checked) => setGrouped(checked === true)} />
                  <span>{t('system.providerQuota.grouped')}</span>
                </label>
              )}
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <button
                    type='button'
                    className='text-muted-foreground hover:bg-muted flex items-center gap-1 rounded-md px-1.5 py-1 text-xs transition-colors'
                    aria-label={t('system.providerQuota.sort.label')}
                  >
                    <ArrowUpDown className='h-3.5 w-3.5' />
                    <span>{t(getQuotaSortLabelKey(sortMode))}</span>
                  </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align='end' className='min-w-40'>
                  <DropdownMenuRadioGroup value={sortMode} onValueChange={(value) => setSortMode(value as typeof sortMode)}>
                    <DropdownMenuRadioItem value='preferred'>{t('system.providerQuota.sort.channelList')}</DropdownMenuRadioItem>
                    <DropdownMenuRadioItem value='alphabetical'>{t('system.providerQuota.sort.alphabetical')}</DropdownMenuRadioItem>
                    <DropdownMenuRadioItem value='created'>{t('system.providerQuota.sort.created')}</DropdownMenuRadioItem>
                  </DropdownMenuRadioGroup>
                </DropdownMenuContent>
              </DropdownMenu>
              <button
                onClick={onRefresh}
                disabled={isRefreshing || isLoading}
                className='text-muted-foreground hover:text-foreground transition-colors disabled:opacity-50'
                aria-label={t('system.providerQuota.refresh.label')}
              >
                {isRefreshing ? <Loader2 className='h-4 w-4 animate-spin' /> : <RefreshCw className='h-4 w-4' />}
              </button>
            </div>
          </div>

          {grouped && !isLoading && !isError && sortedChannels.length > WIDE_QUOTA_POPOVER_CHANNEL_COUNT && (
            // Pills as their own single scrollable line below the header —
            // nothing competes with the title row, and the line can never
            // wrap into the multi-row strip that ate the popover before.
            // Ungrouped view drops them with the section headers.
            <div className='flex items-center gap-1 overflow-x-auto pb-1.5'>
              <button
                type='button'
                aria-pressed={activeCategory === 'all'}
                onClick={() => setSelectedCategory('all')}
                className={`shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium whitespace-nowrap transition-colors ${
                  activeCategory === 'all'
                    ? 'bg-primary text-primary-foreground'
                    : 'bg-muted/70 text-muted-foreground hover:bg-muted hover:text-foreground'
                }`}
              >
                {t('common.all')} ({sortedChannels.length})
              </button>
              {categories.map((cat) => (
                <button
                  key={cat.key}
                  type='button'
                  aria-pressed={activeCategory === cat.key}
                  onClick={() => setSelectedCategory(cat.key)}
                  className={`shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium whitespace-nowrap transition-colors ${
                    activeCategory === cat.key
                      ? 'bg-primary text-primary-foreground'
                      : 'bg-muted/70 text-muted-foreground hover:bg-muted hover:text-foreground'
                  }`}
                >
                  {cat.label} ({cat.count})
                </button>
              ))}
            </div>
          )}

          {renderContent()}
        </div>
      </PopoverContent>
    </Popover>
  );
}
