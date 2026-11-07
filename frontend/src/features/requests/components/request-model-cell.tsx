import { getRequestModelAuditTooltip, getUpstreamModelAudit } from '../utils/upstream-model-audit';
import { IconArrowsExchange, IconClockHour1, IconRoute, IconAlertTriangle, IconCheck, IconQuestionMark } from '@tabler/icons-react';
import { Copy } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { copyTextToClipboard } from '@/lib/clipboard';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { RequestReasoningBadge } from './request-reasoning-badge';

export type RequestModelDisplay = {
  modelID: string;
  status?: 'pending' | 'processing' | 'completed' | 'failed' | 'canceled';
  reasoningEffort?: string | null;
  format?: string;
  executions?: {
    edges: { node?: { modelID?: string; upstreamModelID?: string | null; status?: RequestModelDisplay['status']; reasoningEffort?: string | null; format?: string; passThroughApplied?: boolean | null } | null }[];
  } | null;
  oneHourPromptCacheUsageLogs?: { totalCount?: number };
};

export function RequestModelCell({ request }: { request: RequestModelDisplay }) {
  const { t } = useTranslation();
  const originalModelId = request.modelID || t('requests.columns.unknown');
  const executions = request.executions?.edges?.flatMap((edge) => (edge.node ? [edge.node] : [])) ?? [];
  const executionModelIds = Array.from(new Set(executions.map((exe) => exe.modelID || ''))).filter((id) => id && id !== originalModelId);
        const modelAudit = getUpstreamModelAudit(executions);
        const upstreamModelMatches = modelAudit.status === 'matched';
        const requestIsProcessing = request.status === 'pending' || request.status === 'processing';
        const requestFailed = request.status === 'failed' || request.status === 'canceled';
        const upstreamModelAuditIconClass = requestIsProcessing
          ? 'text-sky-600 dark:text-sky-400 motion-safe:animate-pulse'
          : requestFailed
            ? 'text-red-600 dark:text-red-400'
            : modelAudit.status === 'unknown'
              ? 'text-amber-600 dark:text-amber-400'
              : upstreamModelMatches
                ? 'text-emerald-700 dark:text-emerald-300'
                : 'text-red-700 dark:text-red-400';
        const upstreamModelAuditTooltip = getRequestModelAuditTooltip(modelAudit, request.status, t);

  const reasoningEffort = executions[0]?.reasoningEffort ?? request.reasoningEffort;
  const inboundFormat = request.format;
  const outboundFormat = executions[0]?.format;
  const passThroughApplied = executions.some((execution) => execution.passThroughApplied);
  const oneHourPromptCache = (request.oneHourPromptCacheUsageLogs?.totalCount ?? 0) > 0;
  // Orange is reserved for a confirmed mismatch: a missing format on either
  // side is "unknown" and stays muted.
  const formatsComparable = Boolean(inboundFormat && outboundFormat);
  const outboundProtocolMatches = formatsComparable && outboundFormat === inboundFormat;
  const outboundProtocolTooltip = !formatsComparable
    ? t('requests.tooltips.outboundProtocolUnknown')
    : outboundProtocolMatches
      ? t('requests.tooltips.outboundProtocolMatching', { protocol: outboundFormat })
      : t('requests.tooltips.outboundProtocolConverted', { protocol: outboundFormat });

  const copyModelID = async () => {
    try {
      await copyTextToClipboard(request.modelID);
      toast.success(t('requests.actions.copied'));
    } catch {
      toast.error(t('requests.diagnostics.copyFailed'));
    }
  };
  const modelLabel =
    executionModelIds.length > 0 ? (
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type='button'
            onClick={copyModelID}
            aria-label={t('requests.diagnostics.copyModel', { model: request.modelID })}
            className='flex max-w-[180px] cursor-pointer items-center gap-1.5 rounded-lg border border-amber-200 bg-amber-50 px-2 py-0.5 font-mono text-xs font-medium text-amber-700 transition-colors hover:bg-amber-100 dark:border-amber-800/50 dark:bg-amber-900/30 dark:text-amber-300 dark:hover:bg-amber-900/50'
          >
            <span className='truncate'>{originalModelId}</span>
            <IconRoute className='h-3.5 w-3.5 opacity-80' />
          </button>
        </TooltipTrigger>
        <TooltipContent side='right' className='border-amber-200 bg-white dark:bg-zinc-900'>
          <div className='flex items-center gap-2 p-2'>
            <span className='text-muted-foreground text-xs whitespace-nowrap'>{t('requests.columns.executedModelId')}:</span>
            <span className='rounded bg-amber-100 px-2 py-0.5 text-xs font-medium whitespace-nowrap text-amber-800 dark:bg-amber-900/40 dark:text-amber-200'>
              {executionModelIds.join(' / ')}
            </span>
          </div>
        </TooltipContent>
      </Tooltip>
    ) : (
      <button
        type='button'
        onClick={copyModelID}
        className='flex max-w-[180px] items-center gap-1.5 text-left font-mono text-xs font-medium'
        aria-label={t('requests.diagnostics.copyModel', { model: request.modelID })}
      >
        <span className='truncate'>{originalModelId}</span>
        <Copy className='text-muted-foreground size-3 shrink-0' />
      </button>
    );

  return (
    <div className='flex min-w-[160px] flex-col gap-1'>
      {modelLabel}
      <div className='flex items-center gap-1.5'>
              <Tooltip>
                <TooltipTrigger asChild>
                  <span
                    className={`inline-flex h-5 w-5 items-center justify-center ${upstreamModelAuditIconClass}`}
                    tabIndex={0}
                    role='img'
                    aria-label={upstreamModelAuditTooltip}
                  >
                    {requestIsProcessing ? (
                      <IconQuestionMark className='h-3.5 w-3.5' />
                    ) : requestFailed ? (
                      <IconAlertTriangle className='h-3.5 w-3.5' />
                    ) : modelAudit.status === 'unknown' ? (
                      <IconQuestionMark className='h-3.5 w-3.5' />
                    ) : upstreamModelMatches ? (
                      <IconCheck className='h-3.5 w-3.5' />
                    ) : (
                      <IconAlertTriangle className='h-3.5 w-3.5' />
                    )}
                  </span>
                </TooltipTrigger>
                <TooltipContent>{upstreamModelAuditTooltip}</TooltipContent>
              </Tooltip>
        {reasoningEffort && <RequestReasoningBadge effort={reasoningEffort} />}
        <Tooltip>
          <TooltipTrigger asChild>
            <span
              className={`inline-flex h-5 w-5 items-center justify-center ${
                !formatsComparable
                  ? 'text-muted-foreground/45'
                  : outboundProtocolMatches
                    ? 'text-emerald-700 dark:text-emerald-300'
                    : 'text-orange-700 dark:text-orange-300'
              }`}
              tabIndex={0}
              role='img'
              aria-label={outboundProtocolTooltip}
            >
              <IconArrowsExchange className='h-3.5 w-3.5' />
            </span>
          </TooltipTrigger>
          <TooltipContent>{outboundProtocolTooltip}</TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger asChild>
            <span
              className={`inline-flex h-5 w-5 items-center justify-center ${
                passThroughApplied ? 'text-amber-700 dark:text-amber-300' : 'text-muted-foreground/45'
              }`}
              tabIndex={0}
              role='img'
              aria-label={t(passThroughApplied ? 'requests.tooltips.passThroughApplied' : 'requests.tooltips.passThroughNotApplied')}
            >
              <IconRoute className='h-3.5 w-3.5' />
            </span>
          </TooltipTrigger>
          <TooltipContent>
            {t(passThroughApplied ? 'requests.tooltips.passThroughApplied' : 'requests.tooltips.passThroughNotApplied')}
          </TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger asChild>
            <span
              className={`inline-flex h-5 w-5 items-center justify-center ${
                oneHourPromptCache ? 'text-violet-700 dark:text-violet-300' : 'text-muted-foreground/45'
              }`}
              tabIndex={0}
              role='img'
              aria-label={t(
                oneHourPromptCache ? 'requests.tooltips.oneHourPromptCacheApplied' : 'requests.tooltips.oneHourPromptCacheNotApplied'
              )}
            >
              <IconClockHour1 className='h-3.5 w-3.5' />
            </span>
          </TooltipTrigger>
          <TooltipContent>
            {t(oneHourPromptCache ? 'requests.tooltips.oneHourPromptCacheApplied' : 'requests.tooltips.oneHourPromptCacheNotApplied')}
          </TooltipContent>
        </Tooltip>
      </div>
    </div>
  );
}
