'use client';

import React, { useState, useEffect } from 'react';
import { Loader2, Save } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { AutoCompleteSelect } from '@/components/auto-complete-select';
import { useSystemContext } from '../context/system-context';
import { currencyCodes } from '../data/currencies';
import {
  useGeneralSettings,
  useUpdateGeneralSettings,
} from '../data/system';
import { GMTTimeZoneOptions } from '../data/timezones';

export function GeneralSettings() {
  const { t } = useTranslation();
  const { data: settings, isLoading: isLoadingSettings } = useGeneralSettings();
  const updateSettings = useUpdateGeneralSettings();
  const { isLoading, setIsLoading } = useSystemContext();

  const [currencyCode, setCurrencyCode] = useState('USD');
  const [timezone, setTimezone] = useState('UTC');
  const [currencyDisplayDecimals, setCurrencyDisplayDecimals] = useState<number | null>(null);

  const currencyItems = React.useMemo(
    () =>
      currencyCodes.map((code) => ({
        value: code,
        label: t(`currencies.${code}`),
      })),
    [t]
  );

  const timezoneItems = React.useMemo(() => GMTTimeZoneOptions, []);

  // Update local state when settings are loaded
  useEffect(() => {
    if (settings) {
      setCurrencyCode(settings.currencyCode || 'USD');
      setTimezone(settings.timezone || 'UTC');
      setCurrencyDisplayDecimals(settings.currencyDisplayDecimals);
    }
  }, [settings]);

  const handleSave = async () => {
    setIsLoading(true);
    try {
      await updateSettings.mutateAsync({
        currencyCode: currencyCode.trim(),
        timezone: timezone.trim(),
        currencyDisplayDecimals,
      });
    } finally {
      setIsLoading(false);
    }
  };

  const hasChanges = settings
    ? settings.currencyCode !== currencyCode ||
      settings.timezone !== timezone ||
      settings.currencyDisplayDecimals !== currencyDisplayDecimals
    : false;

  if (isLoadingSettings) {
    return (
      <div className='flex h-32 items-center justify-center'>
        <Loader2 className='h-6 w-6 animate-spin' />
        <span className='text-muted-foreground ml-2'>{t('common.loading')}</span>
      </div>
    );
  }

  return (
    <div className='space-y-6'>
      <Card>
        <CardHeader>
          <CardTitle>{t('system.general.title')}</CardTitle>
          <CardDescription>{t('system.general.description')}</CardDescription>
        </CardHeader>
        <CardContent className='space-y-6'>
          <div className='space-y-2'>
            <Label htmlFor='currency-code'>{t('system.general.currencyCode.label')}</Label>
            <div className='max-w-md'>
              <AutoCompleteSelect
                selectedValue={currencyCode}
                onSelectedValueChange={setCurrencyCode}
                items={currencyItems}
                placeholder={t('system.general.currencyCode.placeholder')}
                isLoading={isLoadingSettings}
              />
            </div>
            <div className='text-muted-foreground text-sm'>{t('system.general.currencyCode.description')}</div>
          </div>

          <div className='space-y-2'>
            <Label htmlFor='currency-display-decimals'>{t('system.general.currencyDisplayDecimals.label')}</Label>
            <div className='max-w-md'>
              <Select
                value={currencyDisplayDecimals === null ? 'default' : String(currencyDisplayDecimals)}
                onValueChange={(value) => setCurrencyDisplayDecimals(value === 'default' ? null : Number(value))}
              >
                <SelectTrigger id='currency-display-decimals' className='w-full'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value='default'>{t('system.general.currencyDisplayDecimals.default')}</SelectItem>
                  {Array.from({ length: 9 }, (_, digits) => (
                    <SelectItem key={digits} value={String(digits)}>
                      {t('system.general.currencyDisplayDecimals.digits', { digits })}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className='text-muted-foreground text-sm'>{t('system.general.currencyDisplayDecimals.description')}</div>
            <div className='text-muted-foreground text-sm'>{t('system.general.currencyDisplayDecimals.options')}</div>
          </div>

          <div className='space-y-2'>
            <Label htmlFor='timezone'>{t('system.general.timezone.label')}</Label>
            <div className='max-w-md'>
              <AutoCompleteSelect
                selectedValue={timezone}
                onSelectedValueChange={setTimezone}
                items={timezoneItems}
                placeholder={t('system.general.timezone.placeholder')}
                isLoading={isLoadingSettings}
              />
            </div>
            <div className='text-muted-foreground text-sm'>{t('system.general.timezone.description')}</div>
          </div>
        </CardContent>
      </Card>


      {hasChanges && (
        <div className='flex justify-end'>
          <Button onClick={handleSave} disabled={isLoading || updateSettings.isPending} className='min-w-[100px]'>
            {isLoading || updateSettings.isPending ? (
              <>
                <Loader2 className='mr-2 h-4 w-4 animate-spin' />
                {t('system.buttons.saving')}
              </>
            ) : (
              <>
                <Save className='mr-2 h-4 w-4' />
                {t('system.buttons.save')}
              </>
            )}
          </Button>
        </div>
      )}
    </div>
  );
}
