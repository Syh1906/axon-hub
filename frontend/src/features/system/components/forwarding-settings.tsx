'use client';

import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { usePermissions } from '@/hooks/usePermissions';
import { Switch } from '@/components/ui/switch';
import {
  useUserAgentPassThroughSettings,
  useUpdateUserAgentPassThroughSettings,
  usePassThroughSettings,
  useUpdatePassThroughSettings,
  useUsageCostInjectionSettings,
  useUpdateUsageCostInjectionSettings,
  useUpstreamTransportSettings,
  useUpdateUpstreamTransportSettings,
  type UpstreamHTTPProtocol,
} from '../data/system';

// How requests and responses are forwarded between clients and upstream
// channels. Every switch saves immediately.
export function ForwardingSettings() {
  const { t } = useTranslation();
  const { hasSystemScope } = usePermissions();
  const { data: transport } = useUpstreamTransportSettings();
  const updateTransport = useUpdateUpstreamTransportSettings();
  const transportDisabled = !transport || !hasSystemScope('write_settings') || updateTransport.isPending;

  // User-Agent Pass-Through settings
  const { data: uaSettings, isLoading: isLoadingUASettings } = useUserAgentPassThroughSettings();
  const updateUASettings = useUpdateUserAgentPassThroughSettings();
  const [uaPassThroughEnabled, setUaPassThroughEnabled] = useState(false);

  // Pass-Through (request/response body) settings
  const { data: ptSettings, isLoading: isLoadingPTSettings } = usePassThroughSettings();
  const updatePTSettings = useUpdatePassThroughSettings();
  const [passThroughEnabled, setPassThroughEnabled] = useState(false);

  const { data: usageCostSettings, isLoading: isLoadingUsageCostSettings } = useUsageCostInjectionSettings();
  const updateUsageCostSettings = useUpdateUsageCostInjectionSettings();
  const [usageCostInjectionEnabled, setUsageCostInjectionEnabled] = useState(false);

  // Update UA pass-through state when loaded
  useEffect(() => {
    if (uaSettings) {
      setUaPassThroughEnabled(uaSettings.enabled);
    }
  }, [uaSettings]);

  // Update pass-through state when loaded
  useEffect(() => {
    if (ptSettings) {
      setPassThroughEnabled(ptSettings.enabled);
    }
  }, [ptSettings]);

  useEffect(() => {
    if (usageCostSettings) {
      setUsageCostInjectionEnabled(usageCostSettings.enabled);
    }
  }, [usageCostSettings]);

  const handleUAPassThroughChange = async (enabled: boolean) => {
    const previousValue = uaPassThroughEnabled;
    setUaPassThroughEnabled(enabled);
    try {
      await updateUASettings.mutateAsync({ enabled });
    } catch {
      // Revert state on error
      setUaPassThroughEnabled(previousValue);
    }
  };

  const handlePassThroughChange = async (enabled: boolean) => {
    const previousValue = passThroughEnabled;
    setPassThroughEnabled(enabled);
    try {
      await updatePTSettings.mutateAsync({ enabled });
    } catch {
      // Revert state on error
      setPassThroughEnabled(previousValue);
    }
  };

  const handleUsageCostInjectionChange = async (enabled: boolean) => {
    const previousValue = usageCostInjectionEnabled;
    setUsageCostInjectionEnabled(enabled);
    try {
      await updateUsageCostSettings.mutateAsync({ enabled });
    } catch {
      setUsageCostInjectionEnabled(previousValue);
    }
  };

  return (
    <div className='space-y-6'>
      <Card>
        <CardHeader>
          <CardTitle>{t('system.upstreamTransport.title')}</CardTitle>
          <CardDescription>{t('system.upstreamTransport.description')}</CardDescription>
        </CardHeader>
        <CardContent className='space-y-4'>
          <div className='flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between'>
            <div className='space-y-0.5'>
              <Label htmlFor='upstream-http-protocol'>{t('system.upstreamTransport.protocol')}</Label>
              <p id='upstream-protocol-help' className='text-muted-foreground text-sm'>{t('system.upstreamTransport.protocolHelp')}</p>
            </div>
            <Select value={transport?.protocol ?? ''} disabled={transportDisabled} onValueChange={(value: UpstreamHTTPProtocol) => updateTransport.mutate({ protocol: value })}>
              <SelectTrigger id='upstream-http-protocol' aria-describedby='upstream-protocol-help' className='w-full sm:w-56'><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value='AUTO'>{t('system.upstreamTransport.autoProtocol')}</SelectItem>
                <SelectItem value='PREFER_HTTP2'>{t('system.upstreamTransport.preferHTTP2')}</SelectItem>
                <SelectItem value='HTTP1'>HTTP/1.1</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className='flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between'>
            <div className='space-y-0.5'>
              <Label htmlFor='upstream-compression'>{t('system.upstreamTransport.compression')}</Label>
              <p id='upstream-compression-help' className='text-muted-foreground text-sm'>{t('system.upstreamTransport.compressionHelp')}</p>
            </div>
            <Select value={transport ? (transport.automaticCompression ? 'auto' : 'off') : ''} disabled={transportDisabled} onValueChange={(value) => updateTransport.mutate({ automaticCompression: value === 'auto' })}>
              <SelectTrigger id='upstream-compression' aria-describedby='upstream-compression-help' className='w-full sm:w-56'><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value='auto'>{t('system.upstreamTransport.autoCompression')}</SelectItem>
                <SelectItem value='off'>{t('system.upstreamTransport.off')}</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>{t('system.passThroughGroup.title')}</CardTitle>
          <CardDescription>{t('system.passThroughGroup.description')}</CardDescription>
        </CardHeader>
        <CardContent className='space-y-4'>
          <div className='flex items-center justify-between'>
            <div className='space-y-0.5'>
              <Label htmlFor='ua-pass-through'>{t('system.userAgentPassThrough.label')}</Label>
              <div className='text-muted-foreground text-sm'>{t('system.userAgentPassThrough.helpText')}</div>
            </div>
            <Switch
              id='ua-pass-through'
              checked={uaPassThroughEnabled}
              onCheckedChange={handleUAPassThroughChange}
              disabled={isLoadingUASettings || updateUASettings.isPending}
            />
          </div>
          <div className='flex items-center justify-between'>
            <div className='space-y-0.5'>
              <Label htmlFor='pass-through'>{t('system.passThrough.label')}</Label>
              <div className='text-muted-foreground text-sm'>{t('system.passThrough.helpText')}</div>
            </div>
            <Switch
              id='pass-through'
              checked={passThroughEnabled}
              onCheckedChange={handlePassThroughChange}
              disabled={isLoadingPTSettings || updatePTSettings.isPending}
            />
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t('system.usageCostInjection.title')}</CardTitle>
          <CardDescription>{t('system.usageCostInjection.description')}</CardDescription>
        </CardHeader>
        <CardContent>
          <div className='flex items-center justify-between'>
            <div className='space-y-0.5'>
              <Label htmlFor='usage-cost-injection'>{t('system.usageCostInjection.label')}</Label>
              <div className='text-muted-foreground text-sm'>{t('system.usageCostInjection.helpText')}</div>
            </div>
            <Switch
              id='usage-cost-injection'
              checked={usageCostInjectionEnabled}
              onCheckedChange={handleUsageCostInjectionChange}
              disabled={isLoadingUsageCostSettings || updateUsageCostSettings.isPending}
            />
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
