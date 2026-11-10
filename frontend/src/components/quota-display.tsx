import { type ReactNode } from 'react';
import { Battery, BatteryLow, BatteryMedium, BatteryFull, BatteryWarning } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import type { ChannelQuotaRoutingMode } from '@/features/channels/data/schema';
import { ProviderQuotaChannel, ProviderQuotaLimit, ProviderSyntheticQuotaData, ProviderNeuralWattQuotaData, ProviderApertisQuotaData, ProviderCharmHyperQuotaData, ProviderKimiCodeQuotaData, ProviderMinimaxQuotaData, ProviderZhipuQuotaData, ClineQuotaWindow, isClineActivePassQuotaData, ProviderCommandCodeQuotaData, ProviderOllamaQuotaData } from '@/features/system/data/quotas';
import { useGeneralSettings, type QuotaRoutingMode } from '@/features/system/data/system';
import { getZenmuxUsagePercentage } from '@/features/system/data/zenmux-quota-display';

export const syntheticWeeklyRegenTickPct = 0.02;

export const BADGE_COLOR_CLASSES: Record<string, string> = {
  green: 'bg-green-500/10 text-green-500 border-green-500/20 hover:bg-green-500/20',
  red: 'bg-red-500/10 text-red-500 border-red-500/20 hover:bg-red-500/20',
  amber: 'bg-amber-500/10 text-amber-500 border-amber-500/20 hover:bg-amber-500/20',
  blue: 'bg-blue-500/10 text-blue-500 border-blue-500/20 hover:bg-blue-500/20',
};

export const STATUS_LABELS = {
  available: 'quota.status.available',
  warning: 'quota.status.warning',
  exhausted: 'quota.status.exhausted',
  unknown: 'quota.status.unknown',
} as const;

// Codex reset credits expire (typically 30 days after they are granted). Show
// the redeem action shortly before the earliest credit lapses even while the
// channel still has quota left, otherwise the credit is wasted.
export const RESET_CREDIT_EXPIRING_SOON_MS = 72 * 60 * 60 * 1000;

export function isResetCreditExpiringSoon(expiresAt?: string, now = Date.now()): boolean {
  if (!expiresAt) return false;
  const expiresAtMs = new Date(expiresAt).getTime();
  if (Number.isNaN(expiresAtMs)) return false;
  // A credit that already lapsed is stale cached quota data, not a redemption
  // reminder: only a credit that is still valid can be "expiring soon".
  return expiresAtMs > now && expiresAtMs - now <= RESET_CREDIT_EXPIRING_SOON_MS;
}

// Effective routing mode for a channel: its own mode wins unless it defers
// via INHERIT to the global default. Null when the global default is
// unavailable (e.g. the viewer lacks read_settings scope) — the mode badge is
// then omitted instead of guessing.
export function resolveEffectiveRoutingMode(
  channelMode: ChannelQuotaRoutingMode,
  globalDefaultMode?: QuotaRoutingMode | null
): QuotaRoutingMode | null {
  if (channelMode && channelMode !== 'INHERIT') return channelMode;
  return globalDefaultMode ?? null;
}

// Most restrictive mode wins for grouped labels: a shared account advertises
// the strictest effective mode across every channel sharing it
// (BACKPRESSURE > REMOVE_ON_EXHAUSTED > IGNORE_QUOTA).
export function mostRestrictiveRoutingMode(modes: Array<QuotaRoutingMode | null>): QuotaRoutingMode | null {
  const restrictiveness: Record<QuotaRoutingMode, number> = { BACKPRESSURE: 3, REMOVE_ON_EXHAUSTED: 2, IGNORE_QUOTA: 1 };
  let best: QuotaRoutingMode | null = null;
  for (const mode of modes) {
    if (mode && (!best || restrictiveness[mode] > restrictiveness[best])) best = mode;
  }
  return best;
}

// Resolve each displayed row once. Filtering the complete channel list for
// every grouped row made quota rendering quadratic as the channel count grew.
export function buildEffectiveModeByChannel(
  channels: readonly ProviderQuotaChannel[],
  displayChannels: readonly ProviderQuotaChannel[],
  defaultMode?: QuotaRoutingMode | null
): Map<string, QuotaRoutingMode | null> {
  const modesByAccount = new Map<string, Array<QuotaRoutingMode | null>>();
  for (const channel of channels) {
    if (!channel.accountKey) continue;
    const modes = modesByAccount.get(channel.accountKey);
    const mode = resolveEffectiveRoutingMode(channel.quotaRoutingMode, defaultMode);
    if (modes) modes.push(mode);
    else modesByAccount.set(channel.accountKey, [mode]);
  }

  const modesByChannel = new Map<string, QuotaRoutingMode | null>();
  for (const channel of displayChannels) {
    const mode = channel.sharedAccountNames
      ? mostRestrictiveRoutingMode(channel.accountKey ? (modesByAccount.get(channel.accountKey) ?? []) : [])
      : resolveEffectiveRoutingMode(channel.quotaRoutingMode, defaultMode);
    modesByChannel.set(channel.id, mode);
  }
  return modesByChannel;
}

export type BatteryLevel = 'full' | 'medium' | 'low' | 'empty' | 'warning';

export function getBatteryIcon(level: BatteryLevel) {
  switch (level) {
    case 'full':
      return BatteryFull;
    case 'medium':
      return BatteryMedium;
    case 'low':
      return BatteryLow;
    case 'warning':
      return BatteryWarning;
    default:
      return Battery;
  }
}

export function getBatteryLevel(percentage: number, status: string): BatteryLevel {
  if (status === 'exhausted') return 'warning';
  const remaining = 100 - percentage;
  if (remaining < 5) return 'empty';
  if (remaining < 20) return 'low';
  if (remaining < 80) return 'medium';
  return 'full';
}

export function isOpenaiType(t: string): t is 'openai' | 'openai_responses' {
  return t === 'openai' || t === 'openai_responses';
}

export function isCommandCodeType(t: string): t is 'commandcode' | 'commandcode_anthropic' {
  return t === 'commandcode' || t === 'commandcode_anthropic';
}

export function isOllamaType(t: string): t is 'ollama' | 'ollama_anthropic' {
  return t === 'ollama' || t === 'ollama_anthropic';
}
export const COMMAND_CODE_PLAN_LABELS: Record<string, string> = {
  'individual-go': 'Go',
  'individual-goat': 'GOAT',
  'individual-pro': 'Pro',
  'individual-max': 'Max 10x',
  'individual-ultra': 'Max 20x',
};

export function getCommandCodePlanLabel(planId?: string, planLabel?: string): string {
  const normalizedPlanId = planId?.trim().toLowerCase();
  return (normalizedPlanId && COMMAND_CODE_PLAN_LABELS[normalizedPlanId]) || planLabel?.trim() || planId?.trim() || '';
}

export function isMinimaxType(t: string): t is 'minimax' | 'minimax_anthropic' {
  return t === 'minimax' || t === 'minimax_anthropic';
}

export function isZenmuxType(t: string): t is 'zenmux' | 'zenmux_responses' | 'zenmux_anthropic' | 'zenmux_gemini' | 'zenmux_video' {
  return t === 'zenmux' || t === 'zenmux_responses' || t === 'zenmux_anthropic' || t === 'zenmux_gemini' || t === 'zenmux_video';
}

export function getDurationPercent(startAt?: string | null, endAt?: string | null): number | undefined {
  if (!startAt || !endAt) return undefined;
  const start = new Date(startAt).getTime();
  const end = new Date(endAt).getTime();
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) return undefined;
  return Math.max(0, Math.min(100, ((Date.now() - start) / (end - start)) * 100));
}

// Fraction of a limit's reset window that has elapsed, derived from the
// PeriodStart/NextResetAt pair the backend stamps on windowed limits.
export function getLimitDurationPercent(limit: ProviderQuotaLimit): number | undefined {
  return getDurationPercent(limit.periodStart, limit.nextResetAt);
}

export function getClineUsagePercent(window?: ClineQuotaWindow): number {
  return window?.usage_percent ?? (window?.usage_ratio ?? 0) * 100;
}

export function getChannelPercentage(channel: ProviderQuotaChannel): number {
  let percentage = 0;
  if (channel.type === 'claudecode') {
    const qd = channel.quotaStatus.quotaData;
    const util5h = qd.windows?.['5h']?.utilization || 0;
    const util7d = qd.windows?.['7d']?.utilization || 0;
    percentage = Math.max(util5h, util7d) * 100;
  } else if (channel.type === 'codex') {
    const qd = channel.quotaStatus.quotaData;
    percentage = qd.rate_limit?.primary_window?.used_percent || 0;
  } else if (channel.type === 'xai_subscription') {
    const qd = channel.quotaStatus.quotaData;
    percentage = Math.max(qd.billing?.weekly?.usage_percent ?? 0, qd.billing?.monthly?.usage_percent ?? 0);
  } else if (channel.type === 'cline') {
    const qd = channel.quotaStatus.quotaData;
    percentage = isClineActivePassQuotaData(qd)
      ? Math.max(getClineUsagePercent(qd.windows.last5h), getClineUsagePercent(qd.windows.last7d), getClineUsagePercent(qd.windows.last30d))
      : 0;
  } else if (channel.type === 'github_copilot') {
    const qd = channel.quotaStatus.quotaData;
    let lowestRemaining = 100;
    const limitedQuotas = qd.limited_user_quotas;
    const totalQuotas = qd.total_quotas;

    if (limitedQuotas) {
      Object.entries(limitedQuotas).forEach(([key, remaining]) => {
        if (typeof remaining === 'number') {
          const total = totalQuotas?.[key] ?? remaining;
          if (total > 0) {
            lowestRemaining = Math.min(lowestRemaining, (remaining / total) * 100);
          }
        }
      });
    }

    if (qd.quota_snapshots) {
      Object.values(qd.quota_snapshots).forEach((snapshot) => {
        if (snapshot && !snapshot.unlimited && typeof snapshot.percent_remaining === 'number') {
          lowestRemaining = Math.min(lowestRemaining, snapshot.percent_remaining);
        }
      });
    }

    percentage = 100 - lowestRemaining;
  } else if (channel.type === 'nanogpt' || channel.type === 'nanogpt_responses') {
    const qd = channel.quotaStatus.quotaData;
    let maxPercent = 0;
    if (qd.windows?.weeklyInputTokens) maxPercent = Math.max(maxPercent, (qd.windows.weeklyInputTokens.percentUsed ?? 0) * 100);
    if (qd.windows?.dailyInputTokens) maxPercent = Math.max(maxPercent, (qd.windows.dailyInputTokens.percentUsed ?? 0) * 100);
    if (qd.windows?.dailyImages) maxPercent = Math.max(maxPercent, (qd.windows.dailyImages.percentUsed ?? 0) * 100);
    percentage = maxPercent;
  } else if (channel.type === 'opencode_go' || channel.type === 'opencode_go_anthropic') {
    percentage = Math.max(0, ...channel.quotaStatus.limits.map((limit) => limit.usageRatio * 100));
  } else if (isOllamaType(channel.type)) {
    const qd = channel.quotaStatus.quotaData as ProviderOllamaQuotaData | undefined;
    percentage = Math.max(
      qd?.windows?.monthly?.usage_percent ?? 0,
      qd?.windows?.['5h']?.usage_percent ?? 0,
      qd?.windows?.weekly?.usage_percent ?? 0
    );
    // Purchased credits keep the channel usable, so the battery stops at "low" instead of "empty".
    if ((qd?.credits?.purchased_usd ?? 0) > 0) {
      percentage = Math.min(percentage, 94);
    }
  } else if (isCommandCodeType(channel.type)) {
    const qd = channel.quotaStatus.quotaData as ProviderCommandCodeQuotaData | undefined;
    percentage = Math.max(
      qd?.windows?.five_hour?.usage_percent ?? 0,
      qd?.windows?.weekly?.usage_percent ?? 0,
      qd?.credits?.monthly_limit_usd != null && qd.credits.monthly_limit_usd > 0
        ? ((qd.credits.monthly_limit_usd - (qd.credits.monthly_remaining_usd ?? 0)) / qd.credits.monthly_limit_usd) * 100
        : 0
    );
  } else if (channel.type === 'moonshot_coding') {
    const qd = channel.quotaStatus.quotaData as ProviderKimiCodeQuotaData | undefined;
    percentage = Math.max(0, ...(qd?.rows ?? []).map((row) => (row.limit > 0 ? (row.used / row.limit) * 100 : 0)));
  } else if (channel.type === 'minimax' || channel.type === 'minimax_anthropic') {
    const qd = channel.quotaStatus.quotaData as ProviderMinimaxQuotaData | undefined;
    percentage = Math.max(0, ...(qd?.rows ?? []).map((row) => Math.max(row.intervalPercent, row.weeklyPercent)));
  } else if (channel.type === 'zhipu' || channel.type === 'zhipu_anthropic' || channel.type === 'zai' || channel.type === 'zai_anthropic') {
    const qd = channel.quotaStatus.quotaData as ProviderZhipuQuotaData | undefined;
    percentage = Math.max(0, ...(qd?.rows ?? []).map((row) => row.usedPercent));
  } else if (isOpenaiType(channel.type) && channel.providerType === 'synthetic') {
    const qd = channel.quotaStatus.quotaData as ProviderSyntheticQuotaData | undefined;
    const weeklyPct = qd?.planWeeklyLimit?.percentRemaining ?? qd?.weeklyTokenLimit?.percentRemaining ?? 100;
    percentage = 100 - weeklyPct;
  } else if (isOpenaiType(channel.type) && channel.providerType === 'neuralwatt') {
    const qd = channel.quotaStatus.quotaData as ProviderNeuralWattQuotaData | undefined;
    const kwhIncluded = qd?.subscription?.kwh_included ?? 0;
    const kwhUsed = qd?.subscription?.kwh_used ?? 0;
    if (kwhIncluded > 0) {
      percentage = (kwhUsed / kwhIncluded) * 100;
    }
  } else if (isOpenaiType(channel.type) && channel.providerType === 'apertis') {
    percentage = getApertisPercentage(channel.quotaStatus.quotaData as ProviderApertisQuotaData | undefined);
  } else if (isOpenaiType(channel.type) && channel.providerType === 'charm_hyper') {
    const qd = channel.quotaStatus.quotaData as ProviderCharmHyperQuotaData | undefined;
    const balance = qd?.balance;
    if (typeof balance === 'number') {
      percentage = Math.max(0, Math.min(100, (1.0 - balance / 100) * 100));
    }
  } else if (isZenmuxType(channel.type)) {
    percentage = getZenmuxUsagePercentage(channel.quotaStatus.limits);
  }
  return percentage;
}
export function getApertisPercentage(qd: ProviderApertisQuotaData | undefined): number {
  if (!qd) return 0;
  if (qd.is_subscriber && qd.subscription?.cycle_quota_limit) {
    return ((qd.subscription.cycle_quota_used ?? 0) / (qd.subscription.cycle_quota_limit ?? 1)) * 100;
  }
  if (qd.payg && !qd.payg.token_is_unlimited && typeof qd.payg.token_total === 'number' && typeof qd.payg.token_used === 'number') {
    return (qd.payg.token_used / qd.payg.token_total) * 100;
  }
  return 0;
}

// UsageTimeBar shows usage on a single progress bar with a small triangle below
// it marking how far the reset window has elapsed (time progress). Hovering
// reveals the detailed figures via tooltip, keeping the row compact.
export function UsageTimeBar({ usagePercent, durationPercent, tooltip }: { usagePercent: number; durationPercent?: number; tooltip: ReactNode }) {
  const clamped = Math.min(Math.max(usagePercent || 0, 0), 100);
  const markerLeft = durationPercent === undefined ? undefined : Math.min(Math.max(durationPercent, 0), 100);
  const u = clamped / 100;
  let severity = u;
  if (durationPercent !== undefined && durationPercent > 0) {
    const d = Math.max(durationPercent / 100, 0.01);
    severity = u * (u / d);
  }
  severity = Math.min(1, Math.max(0, severity));

  // Tailwind 500 colors approximation for a modern, theme-friendly gradient:
  // Green (142, 71%, 45%), Yellow (45, 93%, 47%), Red (0, 84%, 60%)
  let h: number;
  let s: number;
  let l: number;
  if (severity < 0.5) {
    const n = severity * 2; // 0 to 1
    h = 142 - n * (142 - 45);
    s = 71 + n * (93 - 71);
    l = 45 + n * (47 - 45);
  } else {
    const n = (severity - 0.5) * 2; // 0 to 1
    h = 45 - n * 45;
    s = 93 - n * (93 - 84);
    l = 47 + n * (60 - 47);
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <div className='relative cursor-default pb-1.5' tabIndex={0}>
          <div className='bg-muted/60 h-1.5 w-full overflow-hidden rounded-full'>
            <div
              className='h-full transition-all duration-500'
              style={{ width: `${clamped}%`, backgroundColor: `hsl(${Math.round(h)}, ${Math.round(s)}%, ${Math.round(l)}%)` }}
            />
          </div>
          {markerLeft !== undefined && (
            <div className='absolute top-2 -translate-x-1/2' style={{ left: `${markerLeft}%` }} aria-hidden>
              {/* upward triangle pointing at the bar, marking elapsed time */}
              <div className='border-b-muted-foreground h-0 w-0 border-x-[3px] border-b-[4px] border-x-transparent' />
            </div>
          )}
        </div>
      </TooltipTrigger>
      <TooltipContent side='top'>{tooltip}</TooltipContent>
    </Tooltip>
  );
}

export function formatTokenCount(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return `${n}`;
}

export const WINDOW_LABEL_KEYS: Record<string, string> = {
  '5h': 'quota.window.5h',
  '7d': 'quota.window.7d',
  '30d': 'quota.window.30d',
  daily: 'quota.window.daily',
  weekly: 'quota.window.weekly',
  monthly: 'quota.window.monthly',
  payg: 'quota.label.token_usage',
  credits: 'quota.label.credits_remaining',
  overage: 'quota.label.overage_window',
  cycle: 'quota.label.subscription',
  kwh: 'quota.label.kwh_remaining',
};

// periodStart only exists because the backend priced the window from usage
// logs: only limits with a pinned period start take part in the cost
// aggregation, and that pricing runs for no provider that reports the window's
// money itself (Synthetic regenerates its weekly pool in ticks, and its limits
// carry a regen time, not a window start). Its presence is therefore the
// documented marker of a log-derived figure; a limit without one shows
// provider-reported money.
export function periodMoneyIsProviderReported(limit: Pick<ProviderQuotaLimit, 'periodStart'>): boolean {
  return limit.periodStart == null;
}

// PeriodQuotaEstimate prices each limit window: the backend sums what the
// channel cost during the window from AxonHub usage logs and divides by the
// usage ratio the provider reported, which yields what the whole window is
// worth — or the provider reports the window's money itself (Synthetic's
// weekly credits), in which case those figures are shown directly. Windows
// without a money figure are simply absent. The tooltip explains whichever
// source produced the displayed rows, so a channel that mixes both never
// suggests one explains the other's rows.
export function PeriodQuotaEstimate({ limits, currency }: { limits: ProviderQuotaLimit[]; currency?: string }) {
  const { t, i18n } = useTranslation();
  const { data: generalSettings } = useGeneralSettings();

  const priced = limits.filter((limit) => limit.periodQuota != null);
  if (priced.length === 0) return null;

  // The tooltip must describe the rows it hangs over: when some rows are
  // provider-reported and others log-derived, the shared dual-source wording
  // is chosen so neither reading is dropped.
  const sources = priced.map((limit) => (periodMoneyIsProviderReported(limit) ? 'provider' : 'log'));
  const hintKey =
    sources.includes('provider') && sources.includes('log')
      ? 'quota.label.period_quota_hint_mixed'
      : sources[0] === 'provider'
        ? 'quota.label.period_quota_hint_provider'
        : 'quota.label.period_quota_hint';

  const formatCurrency = (val: number) =>
    t('currencies.format', {
      val,
      currency: currency || generalSettings?.currencyCode || 'USD',
      locale: i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US',
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    });

  return (
    <div className='border-border/60 mt-3 space-y-2 border-t border-dashed pt-3'>
      <Tooltip>
        <TooltipTrigger asChild>
          <span className='text-muted-foreground cursor-default text-xs font-medium'>{t('quota.label.period_quota')}</span>
        </TooltipTrigger>
        <TooltipContent side='top' className='max-w-[260px]'>
          {t(hintKey)}
        </TooltipContent>
      </Tooltip>

      {priced.map((limit, index) => {
        const labelKey = limit.window ? WINDOW_LABEL_KEYS[limit.window] : undefined;
        const label = labelKey
          ? t(labelKey)
          : limit.window === 'primary' || limit.window === 'secondary'
            ? t('quota.label.token_usage')
            : limit.window || t('quota.label.token_usage');

        // A mark next to the window name identifies provider-reported rows
        // while a sibling row of the same channel is log-derived, so the two
        // readings stay separately attributable.
        const sourceMark =
          sources.includes('provider') && sources.includes('log') && periodMoneyIsProviderReported(limit)
            ? ` · ${t('quota.label.period_quota_provider_source')}`
            : '';

        return (
          <div key={`${limit.window ?? limit.type}-${index}`} className='flex items-center justify-between text-xs'>
            <span className='text-muted-foreground'>
              {label}
              {sourceMark}
            </span>
            <span className='text-foreground font-medium'>
              {t('quota.label.period_quota_value', {
                // The backend only estimates a quota once it has a cost, so
                // both figures are always available here.
                used: formatCurrency(limit.periodCost ?? 0),
                total: formatCurrency(limit.periodQuota as number),
              })}
            </span>
          </div>
        );
      })}
    </div>
  );
}

// Cached quota data can outlive the reset credit it describes: a credit that
// lapsed after the last quota poll is no longer redeemable. Excluding those
// keeps the "expires at" row from advertising a date in the past.
export function isResetCreditUnexpired(reset: { expiresAt?: string }, now = Date.now()): boolean {
  if (!reset.expiresAt) return false;
  const expiresAtMs = new Date(reset.expiresAt).getTime();
  return !Number.isNaN(expiresAtMs) && expiresAtMs > now;
}
