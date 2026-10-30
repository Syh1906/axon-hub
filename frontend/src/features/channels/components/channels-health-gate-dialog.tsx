import { useSystemTime } from '@/hooks/use-system-time';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { usePermissions } from '@/hooks/usePermissions';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { useChannels } from '../context/channels-context';
import { useChannelHealthGate, useResetChannelHealthGate } from '../data/channels';
import { OAUTH_CREDENTIAL_REF } from '../data/schema';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function ChannelsHealthGateDialog({ open, onOpenChange }: Props) {
  const { format: formatTime } = useSystemTime();
  const { t } = useTranslation();
  const { currentRow } = useChannels();
  const { channelPermissions } = usePermissions();
  const { data, isLoading, error } = useChannelHealthGate(currentRow?.id, open);
  const reset = useResetChannelHealthGate();
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    if (!open) return;
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [open]);

  if (!currentRow) return null;

  const health = data?.healthGate;
  const models = health?.models ?? [];
  const resetModels = async (actualModel?: string) => {
    if (!channelPermissions.canWrite || !currentRow) return;
    try {
      await reset.mutateAsync({ channelID: currentRow.id, actualModel });
      toast.success(t('channels.healthGate.resetSuccess'));
    } catch {
      toast.error(t('channels.healthGate.resetError'));
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='max-h-[85vh] sm:max-w-4xl'>
        <DialogHeader>
          <DialogTitle>{t('channels.healthGate.title')}</DialogTitle>
          <DialogDescription>{t('channels.healthGate.description', { name: currentRow.name })}</DialogDescription>
        </DialogHeader>
        <ScrollArea className='max-h-[65vh]'>
          <div className='space-y-5 pr-4'>
            {isLoading && <p className='text-muted-foreground'>{t('common.loading')}</p>}
            {error && (
              <p role='alert' className='text-destructive'>
                {t('channels.healthGate.loadError')}
              </p>
            )}
            {data && !health && <p className='text-muted-foreground text-sm'>{t('channels.healthGate.unavailable')}</p>}
            {health && (
              <>
                {health.disabled && (
                  <p className='rounded-md border border-amber-500 p-3 text-amber-600'>{t('channels.healthGate.disabled')}</p>
                )}
                <div className='flex items-center justify-between gap-3'>
                  <p className='text-muted-foreground text-sm'>
                    {t('channels.healthGate.summary', { open: health.openCount, unstable: health.unstableCount })}
                  </p>
                  {channelPermissions.canWrite && models.length > 0 && (
                    <Button type='button' size='sm' variant='outline' disabled={reset.isPending} onClick={() => resetModels()}>
                      {t('channels.healthGate.resetAll')}
                    </Button>
                  )}
                </div>
                {models.length === 0 ? (
                  <p className='text-muted-foreground text-sm'>{t('channels.healthGate.noModels')}</p>
                ) : (
                  <div className='overflow-x-auto rounded-md border'>
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>{t('channels.healthGate.model')}</TableHead>
                          <TableHead>{t('channels.healthGate.state')}</TableHead>
                          <TableHead>{t('channels.healthGate.failures')}</TableHead>
                          <TableHead>{t('channels.healthGate.probeProgress')}</TableHead>
                          <TableHead>{t('channels.healthGate.nextProbe')}</TableHead>
                          <TableHead>{t('channels.healthGate.lastError')}</TableHead>
                          {channelPermissions.canWrite && <TableHead>{t('channels.healthGate.action')}</TableHead>}
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {models.map((model) => {
                          const remaining = model.openUntil
                            ? Math.max(0, Math.ceil((new Date(model.openUntil).getTime() - now) / 1000))
                            : 0;
                          return (
                            <TableRow key={model.actualModel}>
                              <TableCell className='font-mono text-xs'>{model.actualModel}</TableCell>
                              <TableCell>
                                <Badge
                                  variant={model.state === 'open' ? 'destructive' : 'outline'}
                                  className={
                                    model.state === 'unstable' || model.state === 'probing' ? 'border-amber-500 text-amber-600' : ''
                                  }
                                >
                                  {t(`channels.healthGate.states.${model.state}`)}
                                </Badge>
                              </TableCell>
                              <TableCell>
                                {model.consecutiveFailures}/{health.failureThreshold}
                              </TableCell>
                              <TableCell>
                                {model.state === 'probing'
                                  ? `${model.probeSuccesses}/${health.probeSuccessThreshold}`
                                  : t('channels.healthGate.empty')}
                              </TableCell>
                              <TableCell className='text-xs'>
                                {model.openUntil && (model.state === 'open' || model.state === 'probing') ? (
                                  <div>
                                    {model.state === 'open' && (
                                      <div>
                                        {t('channels.healthGate.remaining', {
                                          time: `${Math.floor(remaining / 60)}:${String(remaining % 60).padStart(2, '0')}`,
                                        })}
                                      </div>
                                    )}
                                    {model.state === 'probing' && <div>{t('channels.healthGate.readyToProbe')}</div>}
                                    <div>{t('channels.healthGate.nextProbeAt', { time: formatTime(model.openUntil) })}</div>
                                  </div>
                                ) : (
                                  t('channels.healthGate.empty')
                                )}
                              </TableCell>
                              <TableCell className='max-w-64 text-xs break-words whitespace-normal'>
                                {model.lastError || model.lastStatusCode || model.lastErrorAt ? (
                                  <div>
                                    {model.lastError && <div>{model.lastError}</div>}
                                    <div className='text-muted-foreground'>
                                      {model.lastStatusCode ? t('channels.healthGate.statusCode', { code: model.lastStatusCode }) : null}
                                      {model.lastErrorAt ? ` · ${formatTime(model.lastErrorAt)}` : null}
                                    </div>
                                  </div>
                                ) : (
                                  t('channels.healthGate.empty')
                                )}
                              </TableCell>
                              {channelPermissions.canWrite && (
                                <TableCell>
                                  <Button
                                    type='button'
                                    size='sm'
                                    variant='outline'
                                    disabled={reset.isPending}
                                    onClick={() => resetModels(model.actualModel)}
                                  >
                                    {t('channels.healthGate.reset')}
                                  </Button>
                                </TableCell>
                              )}
                            </TableRow>
                          );
                        })}
                      </TableBody>
                    </Table>
                  </div>
                )}
              </>
            )}
            {data?.disabledAPIKeys && (
              <section className='space-y-2 border-t pt-4'>
                <h3 className='font-medium'>{t('channels.dialogs.disabledAPIKeys.title')}</h3>
                {data.disabledAPIKeys.length === 0 ? (
                  <p className='text-muted-foreground text-sm'>{t('channels.dialogs.disabledAPIKeys.noDisabledKeys')}</p>
                ) : (
                  <ul className='space-y-2 text-sm'>
                    {data.disabledAPIKeys.map((key) => (
                      <li key={key.key} className='rounded-md border p-2'>
                        <span className='font-mono'>
                          {key.key === OAUTH_CREDENTIAL_REF
                            ? t('channels.dialogs.disabledAPIKeys.oauthCredential')
                            : `****${key.key.slice(-4)}`}
                        </span>
                        {' · '}
                        {t('channels.healthGate.statusCode', { code: key.errorCode })}
                        {' · '}
                        {formatTime(key.disabledAt)}
                        {key.reason && <p className='text-muted-foreground break-words'>{key.reason}</p>}
                      </li>
                    ))}
                  </ul>
                )}
              </section>
            )}
          </div>
        </ScrollArea>
      </DialogContent>
    </Dialog>
  );
}
