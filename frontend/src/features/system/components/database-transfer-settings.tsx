import { useEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Download, Loader2, Upload } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useSystemTime } from '@/hooks/use-system-time';
import { usePermissions } from '@/hooks/usePermissions';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Progress } from '@/components/ui/progress';
import {
  type TransferJob,
  readTransferManifest,
  transferActive,
  transferID,
  transferKey,
  transferRequest,
  transferReserved,
  uploadTransfer,
  useTransferJob,
  useTransferStatus,
} from '../data/database-transfer';

const sizeText = (bytes: number) => `${(bytes / 1024 / 1024).toLocaleString(undefined, { maximumFractionDigits: 1 })} MiB`;

export function DatabaseTransferSettings() {
  const { isOwner } = usePermissions();
  return isOwner ? <DatabaseTransferPanel /> : null;
}

function DatabaseTransferPanel() {
  const { t } = useTranslation();
  const { format } = useSystemTime();
  const cache = useQueryClient();
  const status = useTransferStatus();
  const jobs = useTransferJob(status.data?.supported === true);
  const job = jobs.data === undefined ? status.data?.job : jobs.data;
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [file, setFile] = useState<File | null>(null);
  const [uploadPercent, setUploadPercent] = useState<number | null>(null);
  const [confirmJob, setConfirmJob] = useState<string | null>(null);
  const [confirmed, setConfirmed] = useState(false);
  const [target, setTarget] = useState(
    () =>
      `axonhub_import_${new Date()
        .toISOString()
        .replace(/[-:TZ.]/g, '')
        .slice(0, 14)}`
  );
  const running = useRef(false);
  const exportID = useRef<string | null>(null);
  const uploadID = useRef<string | null>(null);
  const upload = useRef<AbortController | null>(null);
  const chooser = useRef<HTMLInputElement>(null);
  useEffect(() => () => upload.current?.abort(), []);

  const explain = (code?: string) => t(`system.transfer.errors.${code}`, { defaultValue: t('system.transfer.errors.operation_failed') });
  const remember = (value: TransferJob) => cache.setQueryData([...transferKey, 'job'], value);
  const execute = async (operation: () => Promise<void>) => {
    if (running.current) return;
    running.current = true;
    setBusy(true);
    setError('');
    setNotice('');
    try {
      await operation();
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : 'operation_failed');
    } finally {
      running.current = false;
      setBusy(false);
      void cache.invalidateQueries({ queryKey: transferKey });
    }
  };

  const startExport = () =>
    execute(async () => {
      exportID.current ??= transferID();
      remember(await transferRequest<TransferJob>('/export', 'POST', { id: exportID.current }));
      exportID.current = null;
    });

  const startUpload = () =>
    execute(async () => {
      if (!file || !status.data) return;
      if (file.size > status.data.maxFileBytes + 128 * 1024) throw new Error('file_too_large');
      const manifest = await readTransferManifest(file);
      uploadID.current ??= job?.kind === 'import' && job.phase === 'awaiting_upload' ? job.id : transferID();
      const prepared = await transferRequest<TransferJob>('/uploads', 'POST', { id: uploadID.current, bytes: file.size, manifest });
      remember(prepared);
      if (prepared.phase !== 'awaiting_upload') return;
      const controller = new AbortController();
      upload.current = controller;
      setUploadPercent(0);
      try {
        remember(await uploadTransfer(prepared.id, file, controller.signal, setUploadPercent));
      } finally {
        upload.current = null;
        setUploadPercent(null);
      }
    });

  const download = () =>
    execute(async () => {
      if (!job) return;
      const { url } = await transferRequest<{ url: string }>(`/jobs/${job.id}/download-ticket`, 'POST', {});
      const anchor = document.createElement('a');
      anchor.href = url;
      anchor.download = '';
      document.body.appendChild(anchor);
      anchor.click();
      anchor.remove();
      setNotice(t('system.transfer.downloadStarted'));
    });

  const discard = () =>
    execute(async () => {
      if (!job) return;
      await transferRequest(`/jobs/${job.id}`, 'DELETE');
      uploadID.current = null;
      exportID.current = null;
      setFile(null);
      if (chooser.current) chooser.current.value = '';
    });

  const startImport = () =>
    execute(async () => {
      if (!confirmed || !confirmJob || job?.id !== confirmJob || job.phase !== 'ready') return;
      remember(await transferRequest<TransferJob>('/imports', 'POST', { id: confirmJob, targetDatabase: target, confirmed: true }));
      setConfirmJob(null);
      setConfirmed(false);
    });

  const active = transferActive(job);
  const reserved = transferReserved(job);
  const ready = job?.kind === 'import' && job.phase === 'ready';
  const canStart = status.data?.supported && !busy && !reserved && !jobs.isError;
  const canUpload = status.data?.importAllowed && !busy && (!reserved || job?.phase === 'awaiting_upload') && !jobs.isError;
  const targetValid = /^[A-Za-z_][A-Za-z0-9_]{0,62}$/.test(target);

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('system.transfer.title')}</CardTitle>
        <CardDescription>{t('system.transfer.description')}</CardDescription>
      </CardHeader>
      <CardContent className='flex flex-col gap-4'>
        <p className='text-muted-foreground text-sm'>{t('system.transfer.scope')}</p>
        {status.isPending && <p className='text-muted-foreground text-sm'>{t('system.transfer.detecting')}</p>}
        {status.data && !status.data.supported && (
          <Alert>
            <AlertDescription>{explain(status.data.reason)}</AlertDescription>
          </Alert>
        )}
        {status.data?.supported && (
          <>
            <div className='flex flex-wrap items-center gap-3'>
              <Button disabled={!canStart} onClick={startExport}>
                <Download />
                {t('system.transfer.export')}
              </Button>
              <span className='text-muted-foreground text-sm'>
                {t('system.transfer.fileLimit', { size: sizeText(status.data.maxFileBytes) })}
              </span>
            </div>
            <div className='flex flex-col gap-2 rounded-md border p-4'>
              <Label htmlFor='database-transfer-file'>{t('system.transfer.selectFile')}</Label>
              <Input
                ref={chooser}
                id='database-transfer-file'
                type='file'
                accept='.tar'
                disabled={!canUpload}
                onChange={(event) => {
                  setFile(event.target.files?.[0] ?? null);
                  uploadID.current = null;
                  setError('');
                }}
              />
              <p className='text-muted-foreground text-sm'>{t('system.transfer.uploadHint')}</p>
              {!status.data.importAllowed && <p className='text-muted-foreground text-sm'>{explain(status.data.importReason)}</p>}
              <div className='flex flex-wrap gap-2'>
                <Button variant='outline' disabled={!canUpload || !file} onClick={startUpload}>
                  <Upload />
                  {t('system.transfer.upload')}
                </Button>
                {uploadPercent !== null && (
                  <Button variant='outline' onClick={() => upload.current?.abort()}>
                    {t('system.transfer.cancelUpload')}
                  </Button>
                )}
              </div>
              {uploadPercent !== null && (
                <>
                  <Progress value={uploadPercent} aria-label={t('system.transfer.upload')} />
                  <p className='text-sm tabular-nums'>
                    {t(uploadPercent === 100 ? 'system.transfer.validating' : 'system.transfer.uploadProgress', {
                      value: Math.floor(uploadPercent),
                    })}
                  </p>
                </>
              )}
            </div>
          </>
        )}
        {job && (
          <div className='flex flex-col gap-3 rounded-md border p-4' aria-live='polite'>
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <span className='flex items-center gap-2 font-medium'>
                {active && <Loader2 className='size-4 animate-spin motion-reduce:animate-none' />}
                {t(`system.transfer.kind.${job.kind}`)}
                <Badge variant='secondary'>
                  {t(
                    job.kind === 'import' && job.phase === 'completed'
                      ? 'system.transfer.importReady'
                      : `system.transfer.phase.${job.phase}`
                  )}
                </Badge>
              </span>
              <span className='text-muted-foreground text-xs'>
                {t('system.transfer.updated', { time: format(job.updatedAt, 'yyyy-MM-dd HH:mm:ss') })}
              </span>
            </div>
            {active && (
              <p className='text-muted-foreground text-sm'>
                {t(job.phase === 'uploading' ? 'system.transfer.uploadHint' : 'system.transfer.backgroundHint')}
              </p>
            )}
            {job.phase === 'exporting' && (
              <p className='text-sm tabular-nums'>{t('system.transfer.exportBytes', { size: sizeText(job.bytes) })}</p>
            )}
            {job.phase === 'importing' && <p className='text-sm'>{t('system.transfer.restoreProgress')}</p>}
            {job.manifest && (
              <dl className='grid grid-cols-1 gap-2 text-sm sm:grid-cols-2'>
                <div>
                  <dt className='text-muted-foreground'>{t('system.transfer.version')}</dt>
                  <dd>
                    {job.manifest.appVersion} · PostgreSQL {job.manifest.postgresMajor}
                  </dd>
                </div>
                <div>
                  <dt className='text-muted-foreground'>{t('system.transfer.sizes')}</dt>
                  <dd>
                    {sizeText(job.manifest.dumpBytes)} / {sizeText(job.manifest.databaseBytes)}
                  </dd>
                </div>
                <div>
                  <dt className='text-muted-foreground'>{t('system.transfer.exported')}</dt>
                  <dd>{format(job.manifest.exportedAt, 'yyyy-MM-dd HH:mm:ss')}</dd>
                </div>
              </dl>
            )}
            {job.error && (
              <Alert variant='destructive'>
                <AlertDescription>{explain(job.error)}</AlertDescription>
              </Alert>
            )}
            {job.targetDatabase && (
              <p className='text-sm break-all'>
                {t('system.transfer.target')}: <code>{job.targetDatabase}</code>
              </p>
            )}
            {job.kind === 'import' && job.phase === 'completed' && (
              <Alert>
                <AlertDescription className='whitespace-pre-line'>
                  {t('system.transfer.switchSteps', { database: job.targetDatabase })}
                </AlertDescription>
              </Alert>
            )}
            {job.kind === 'import' && job.phase === 'failed' && job.targetDatabase && (
              <p className='text-muted-foreground text-sm'>{t('system.transfer.failedDatabase')}</p>
            )}
            <div className='flex flex-wrap gap-2'>
              {job.downloadAvailable && (
                <Button disabled={busy} onClick={download}>
                  <Download />
                  {t('system.transfer.download')}
                </Button>
              )}
              {ready && (
                <Button
                  disabled={busy || !status.data?.importAllowed}
                  onClick={() => {
                    setConfirmJob(job.id);
                    setConfirmed(false);
                  }}
                >
                  {t('system.transfer.reviewImport')}
                </Button>
              )}
              {!active && job.phase !== 'expired' && (
                <Button variant='outline' disabled={busy} onClick={discard}>
                  {t('system.transfer.discard')}
                </Button>
              )}
            </div>
          </div>
        )}
        {(error || status.isError || jobs.isError) && (
          <Alert variant='destructive'>
            <AlertDescription>{error ? explain(error) : t('system.transfer.errors.status_unknown')}</AlertDescription>
          </Alert>
        )}
        {(status.isError || jobs.isError) && (
          <Button variant='outline' disabled={busy} onClick={() => void cache.invalidateQueries({ queryKey: transferKey })}>
            {t('system.transfer.retryStatus')}
          </Button>
        )}
        {notice && (
          <p className='text-muted-foreground text-sm' role='status'>
            {notice}
          </p>
        )}
      </CardContent>
      <Dialog
        open={confirmJob !== null}
        onOpenChange={(open) => {
          if (!open) {
            setConfirmJob(null);
            setConfirmed(false);
          }
        }}
      >
        <DialogContent className='flex max-h-[85dvh] flex-col sm:max-w-lg'>
          <DialogHeader>
            <DialogTitle>{t('system.transfer.reviewImport')}</DialogTitle>
            <DialogDescription>{t('system.transfer.confirmDescription')}</DialogDescription>
          </DialogHeader>
          <div className='flex min-h-0 flex-col gap-4 overflow-y-auto'>
            <Alert>
              <AlertDescription>{t('system.transfer.trustedSource')}</AlertDescription>
            </Alert>
            <div className='flex flex-col gap-2'>
              <Label htmlFor='database-transfer-target'>{t('system.transfer.target')}</Label>
              <Input
                id='database-transfer-target'
                value={target}
                onChange={(event) => setTarget(event.target.value)}
                aria-invalid={!targetValid}
                disabled={busy}
              />
              <p className='text-muted-foreground text-sm'>{t('system.transfer.targetHint')}</p>
            </div>
            <p className='text-muted-foreground text-sm'>{t('system.transfer.cutoverHint')}</p>
            <div className='flex items-start gap-2'>
              <Checkbox
                id='database-transfer-confirmed'
                checked={confirmed}
                onCheckedChange={(value) => setConfirmed(value === true)}
                disabled={busy}
              />
              <Label htmlFor='database-transfer-confirmed' className='leading-relaxed'>
                {t('system.transfer.confirmCheck')}
              </Label>
            </div>
            {error && (
              <Alert variant='destructive'>
                <AlertDescription>{explain(error)}</AlertDescription>
              </Alert>
            )}
          </div>
          <DialogFooter>
            <Button variant='outline' onClick={() => setConfirmJob(null)}>
              {t('system.transfer.cancel')}
            </Button>
            <Button disabled={busy || !confirmed || !targetValid || !ready || job?.id !== confirmJob} onClick={startImport}>
              {busy && <Loader2 className='animate-spin' />}
              {t('system.transfer.startImport')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  );
}
