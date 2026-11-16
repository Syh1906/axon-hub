import { useId } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Checkbox } from '@/components/ui/checkbox';
import { Label } from '@/components/ui/label';
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group';
import { Separator } from '@/components/ui/separator';
import { CLIENT_PROTOCOLS, protocolLabel } from '../data/client-protocols';

export type ProtocolPolicyDraft = { allowedProtocols: string[]; sameProtocolOnly: boolean };

export function ChannelProtocolPolicyEditor({
  value,
  onChange,
  restricted,
  onRestrictedChange,
}: {
  value: ProtocolPolicyDraft;
  onChange: (value: ProtocolPolicyDraft) => void;
  restricted: boolean;
  onRestrictedChange: (value: boolean) => void;
}) {
  const { t } = useTranslation();
  const id = useId();
  const allowed = restricted ? value.allowedProtocols : CLIENT_PROTOCOLS.map((p) => p.value);
  return (
    <div className='flex flex-col gap-5 py-2' data-testid='channel-protocol-policy'>
      <div className='flex flex-col gap-3'>
        <div>
          <h3 className='font-medium'>{t('protocolPolicy.allowed')}</h3>
          <p className='text-muted-foreground text-sm'>{t('protocolPolicy.allowedHint')}</p>
        </div>
        <RadioGroup
          value={restricted ? 'selected' : 'all'}
          onValueChange={(v) => onRestrictedChange(v === 'selected')}
          aria-label={t('protocolPolicy.allowed')}
        >
          <Label className='flex items-center gap-2'>
            <RadioGroupItem value='all' />
            {t('protocolPolicy.all')}
          </Label>
          <Label className='flex items-center gap-2'>
            <RadioGroupItem value='selected' />
            {t('protocolPolicy.selected')}
          </Label>
        </RadioGroup>
        {restricted && (
          <div className='grid grid-cols-1 gap-3 pl-6 sm:grid-cols-2'>
            {CLIENT_PROTOCOLS.map((p) => (
              <Label key={p.value} htmlFor={`${id}-${p.value}`} className='flex items-center gap-2'>
                <Checkbox
                  id={`${id}-${p.value}`}
                  checked={value.allowedProtocols.includes(p.value)}
                  onCheckedChange={(checked) =>
                    onChange({
                      ...value,
                      allowedProtocols: checked
                        ? [...value.allowedProtocols, p.value]
                        : value.allowedProtocols.filter((f) => f !== p.value),
                    })
                  }
                />
                {p.label}
              </Label>
            ))}
          </div>
        )}
      </div>
      <Separator />
      <div className='flex flex-col gap-3'>
        <h3 className='font-medium'>{t('protocolPolicy.conversion')}</h3>
        <RadioGroup
          value={value.sameProtocolOnly ? 'same' : 'auto'}
          onValueChange={(v) => onChange({ ...value, sameProtocolOnly: v === 'same' })}
          aria-label={t('protocolPolicy.conversion')}
        >
          <Label className='flex items-start gap-2'>
            <RadioGroupItem value='auto' />
            <span>
              {t('protocolPolicy.auto')}
              <span className='text-muted-foreground mt-1 block text-xs'>{t('protocolPolicy.autoHint')}</span>
            </span>
          </Label>
          <Label className='flex items-start gap-2'>
            <RadioGroupItem value='same' />
            <span>
              {t('protocolPolicy.same')}
              <span className='text-muted-foreground mt-1 block text-xs'>{t('protocolPolicy.sameHint')}</span>
            </span>
          </Label>
        </RadioGroup>
      </div>
      <Alert>
        <AlertTitle>{t('protocolPolicy.current')}</AlertTitle>
        <AlertDescription className='flex flex-col gap-1'>
          <span>
            {allowed.length
              ? allowed.map((f) => (value.sameProtocolOnly ? `${protocolLabel(f)} → ${protocolLabel(f)}` : protocolLabel(f))).join(' · ')
              : t('protocolPolicy.required')}
          </span>
          <span>{t(value.sameProtocolOnly ? 'protocolPolicy.summarySame' : 'protocolPolicy.summaryAuto')}</span>
        </AlertDescription>
      </Alert>
      <p className='text-muted-foreground text-xs'>{t('protocolPolicy.transportHint')}</p>
    </div>
  );
}
