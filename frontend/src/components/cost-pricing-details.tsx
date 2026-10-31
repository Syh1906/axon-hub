import { useTranslation } from 'react-i18next';
import type { CostPricingEvidence } from './cost-pricing-evidence';

export function CostPricingDetails({ evidence }: { evidence: CostPricingEvidence | 'mixed' | null }) {
  const { t } = useTranslation();
  if (!evidence) return <p>{t('serviceTier.cost.legacy')}</p>;
  if (evidence === 'mixed') return <p>{t('serviceTier.cost.mixed')}</p>;
  const { serviceTier: tier, context } = evidence;
  return (
    <div className='flex flex-col gap-1 border-b pb-2'>
      <div className='flex justify-between gap-3'>
        <span>{t('serviceTier.cost.mode')}</span>
        <span>{t(`serviceTier.mode.${tier.mode}`, { defaultValue: tier.mode })}</span>
      </div>
      <div className='flex justify-between gap-3'>
        <span>{t('serviceTier.cost.client')}</span>
        <span>{tier.client || t('serviceTier.cost.missing')}</span>
      </div>
      {tier.outbound !== tier.client && (
        <div className='flex justify-between gap-3'>
          <span>{t('serviceTier.cost.outbound')}</span>
          <span>{tier.outbound || t('serviceTier.cost.missing')}</span>
        </div>
      )}
      <div className='flex justify-between gap-3'>
        <span>{t('serviceTier.cost.upstream')}</span>
        <span>{tier.upstream || t('serviceTier.cost.missing')}</span>
      </div>
      <p>{t(`serviceTier.reason.${tier.reason}`, { defaultValue: tier.reason })}</p>
      {(tier.mode === 'flex' || tier.mode === 'scale') && <p>{t('serviceTier.cost.basePriceOnly')}</p>}
      {tier.basis === 'client' && !tier.confirmed && <p>{t('serviceTier.cost.unconfirmed')}</p>}
      <div className='flex justify-between gap-3'>
        <span>{t('serviceTier.cost.context')}</span>
        <span>
          {t(`serviceTier.context.${context.kind}`)}
          {context.longContext ? ' · 1M' : ''}
        </span>
      </div>
      {context.threshold != null && (
        <p>
          {context.inputTokens.toLocaleString()} {context.kind === 'standard' ? '≤' : '>'} {context.threshold.toLocaleString()} ·{' '}
          {t('serviceTier.context.full')}
        </p>
      )}
      {context.kind === 'schedule' && <p>{context.name}</p>}
      {tier.source && <p>{[t(`serviceTier.prices.${tier.source}`), tier.referenceModel, tier.version].filter(Boolean).join(' · ')}</p>}
    </div>
  );
}
