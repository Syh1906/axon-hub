import { Loader2, Pause, Play } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useSystemTime } from '@/hooks/use-system-time';
import { usePermissions } from '@/hooks/usePermissions';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Progress } from '@/components/ui/progress';
import { useCleanupTask, usePauseCleanupTask } from '../data/maintenance';

export function CleanupTaskProgress() {
  const { t } = useTranslation();
  const { format } = useSystemTime();
  const { hasSystemScope } = usePermissions();
  const query = useCleanupTask();
  const pause = usePauseCleanupTask();
  const task = query.data;
  if (!task)
    return query.isError ? (
      <Alert variant='destructive'>
        <AlertDescription>{t('system.maintenance.statusError')}</AlertDescription>
      </Alert>
    ) : null;
  const working = ['queued', 'running', 'waiting'].includes(task.status);
  const canResume = ['paused', 'failed'].includes(task.status);
  const completed = task.stages.filter((stage) => stage.completed).length;
  return (
    <Card>
      <CardHeader className='flex flex-row flex-wrap items-start justify-between gap-3'>
        <div className='flex flex-col gap-2'>
          <CardTitle className='flex items-center gap-2'>
            {working && <Loader2 className='size-4 animate-spin' />}
            {t('system.maintenance.taskTitle')}
            <Badge variant='secondary'>{t(`system.maintenance.status.${task.status}`)}</Badge>
          </CardTitle>
          <CardDescription>{t('system.maintenance.backgroundHint')}</CardDescription>
        </div>
        {hasSystemScope('write_settings') && (working || canResume) && (
          <Button
            variant='outline'
            size='sm'
            disabled={pause.isPending || (working && task.pauseRequested)}
            onClick={() => pause.mutate({ id: task.id, paused: working })}
          >
            {working ? <Pause /> : <Play />}
            {t(
              task.pauseRequested && working
                ? 'system.maintenance.pausing'
                : working
                  ? 'system.maintenance.pause'
                  : 'system.maintenance.resume'
            )}
          </Button>
        )}
      </CardHeader>
      <CardContent className='flex flex-col gap-3'>
        <div className='flex justify-between text-sm'>
          <span>{t('system.maintenance.completedStages', { count: completed, total: task.stages.length })}</span>
          <span className='text-muted-foreground'>{format(task.updatedAt, 'yyyy-MM-dd HH:mm:ss')}</span>
        </div>
        <Progress value={task.stages.length ? (completed * 100) / task.stages.length : 0} aria-label={t('system.maintenance.taskTitle')} />
        <div className='divide-y rounded-md border'>
          {task.stages.map((stage, index) => (
            <div className='flex flex-wrap items-center justify-between gap-2 px-3 py-2 text-sm' key={stage.resourceType}>
              <span className={index === task.currentStage && working ? 'text-primary font-medium' : ''}>
                {t(`system.storage.policy.resourceTypes.${stage.resourceType}`)}
              </span>
              <span className='text-muted-foreground tabular-nums'>
                {t('system.maintenance.stageCounts', {
                  scanned: stage.scannedCount.toLocaleString(),
                  processed: stage.processedCount.toLocaleString(),
                })}
                {stage.completed ? ` · ${t('system.maintenance.status.completed')}` : ''}
              </span>
            </div>
          ))}
        </div>
        {task.waitReason && task.status !== 'completed' && (
          <p className='text-muted-foreground text-sm'>
            {t(`system.maintenance.reason.${task.waitReason}`, { defaultValue: t('system.maintenance.reason.batch_failed') })}
          </p>
        )}
        {(pause.isError || query.isError) && (
          <Alert variant='destructive'>
            <AlertDescription>{t('system.maintenance.statusError')}</AlertDescription>
          </Alert>
        )}
      </CardContent>
    </Card>
  );
}
