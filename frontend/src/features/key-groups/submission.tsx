import { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { useSelectedProjectId } from '@/stores/projectStore';
import { extractNumberID } from '@/lib/utils';
import { usePermissions } from '@/hooks/usePermissions';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from '@/components/ui/dialog';
import { useKeyGroups } from '@/features/apikeys/data/groups';
import { useManagementAPI, type ManagementRow, type Result } from './data';
import { csvCell } from './drafts';
import { managementError } from './errors';
import { notifyPendingRecovery, pendingKey, readPending, savePending } from './pending';

export function useSubmission(
  restore = false,
  options: { verifyGroups?: boolean; verifyKey?: { ownerID: number; groupRef: number }; notifyCompletion?: boolean } = {}
) {
  const { t } = useTranslation();
  const api = useManagementAPI();
  const project = useSelectedProjectId();
  const { user } = usePermissions();
  const storageKey = pendingKey(project, user?.id);
  const [restored] = useState(() => (restore ? readPending(storageKey) : null));
  const running = useRef(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [preview, setPreview] = useState<Result[]>([]);
  const [fingerprint, setFingerprint] = useState('');
  const [previewRows, setPreviewRows] = useState<ManagementRow[]>([]);
  const [results, setResults] = useState<Result[]>([]);
  const [uncertain, setUncertain] = useState(!!restored);
  const pending = useRef(restored);
  const [submitted, setSubmitted] = useState<ManagementRow[]>(restored?.rows ?? []);
  const inspect = async (rows: ManagementRow[]) => {
    if (running.current) return;
    running.current = true;
    setBusy(true);
    setError('');
    try {
      const invalid = rows.filter((r) =>
        [r.budget, ...(r.keys?.map((k) => k.budget) ?? [])].some((b) => b?.setExpiry && b.expiresAt === undefined)
      );
      const valid = rows.filter((r) => !invalid.includes(r));
      const checked = valid.length ? await api.preview(valid) : [];
      setPreview([
        ...checked.map((r) => ({ ...r, error: managementError(r.error) })),
        ...invalid.map((r) => ({ rowID: r.rowID, success: false, error: t('kg.expiryRequired'), groupID: 0, keyIDs: [] })),
      ]);
      setPreviewRows(structuredClone(rows));
      setFingerprint(JSON.stringify(rows));
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
      running.current = false;
    }
  };
  const execute = async (recover = false, rows: ManagementRow[] = []) => {
    if (running.current) return;
    if (!recover && readPending(storageKey)) {
      setError(t('kg.pendingBeforeSubmit'));
      return;
    }
    running.current = true;
    setBusy(true);
    setError('');
    if (!recover) {
      pending.current = { id: crypto.randomUUID(), rows: structuredClone(rows) };
      savePending(storageKey, pending.current);
      setSubmitted(structuredClone(rows));
    }
    const batch = pending.current;
    try {
      if (!batch) return;
      const known = recover ? await api.recover(batch.id) : [];
      const remaining = batch.rows.filter((r) => !known.some((k) => k.rowID === r.rowID));
      const received = remaining.length ? await api.apply(batch.id, remaining) : [];
      setResults((previous) => {
        const byID = new Map(previous.map((r) => [r.rowID, r]));
        [...known, ...received].forEach((r) => byID.set(r.rowID, { ...r, error: managementError(r.error) }));
        return [...byID.values()];
      });
      setUncertain(false);
      setFingerprint('');
      pending.current = null;
      savePending(storageKey, null);
      if (options.notifyCompletion) notifyPendingRecovery();
    } catch (e) {
      setUncertain(true);
      setError((e as Error).message);
      if (!recover) notifyPendingRecovery();
    } finally {
      setBusy(false);
      running.current = false;
    }
    // 已收到服务端结果后，刷新失败不应把已完成的批次重新标为未知。
    if (!pending.current) await api.refresh(options.verifyGroups, options.verifyKey);
  };
  return {
    busy,
    error,
    preview,
    results,
    uncertain,
    submitted,
    inspect,
    execute,
    valid: (rows: ManagementRow[]) => fingerprint === JSON.stringify(rows),
    statusFor: (row?: ManagementRow) =>
      row && JSON.stringify(previewRows.find((r) => r.rowID === row.rowID)) === JSON.stringify(row)
        ? preview.find((r) => r.rowID === row.rowID)
        : undefined,
    clear: () => {
      setResults([]);
      setPreview([]);
      setFingerprint('');
      setSubmitted([]);
    },
  };
}
export type Submission = ReturnType<typeof useSubmission>;
export function PendingRecovery({ initiallyClosed = false }: { initiallyClosed?: boolean }) {
  const { t } = useTranslation();
  const flow = useSubmission(true);
  const [closed, setClosed] = useState(initiallyClosed);
  if (!flow.uncertain && (closed || !flow.results.length)) return null;
  if (closed) {
    return (
      <Button className='self-start' onClick={() => setClosed(false)}>
        {t('kg.resumePending')}
      </Button>
    );
  }
  const labels = new Map(flow.submitted.map((r) => [r.rowID, r.name ?? r.keys?.map((k) => k.name).join(' / ') ?? String(r.targetID)]));
  return (
    <Dialog
      open
      onOpenChange={(v) => {
        if (!v && !flow.busy) setClosed(true);
      }}
    >
      <DialogContent className='sm:max-w-2xl' showCloseButton={!flow.busy}>
        <DialogHeader>
          <DialogTitle>{t('kg.recover')}</DialogTitle>
          <DialogDescription>{t(flow.uncertain ? 'kg.recoverHelp' : 'kg.recovered')}</DialogDescription>
        </DialogHeader>
        <ResultsPanel flow={flow} labels={labels} />
        {flow.uncertain ? (
          <>
            <SubmissionActions flow={flow} rows={[]} />
            <Button variant='outline' disabled={flow.busy} onClick={() => setClosed(true)}>
              {t('kg.deferRecovery')}
            </Button>
          </>
        ) : (
          <Button onClick={() => setClosed(true)}>{t('common.buttons.close')}</Button>
        )}
      </DialogContent>
    </Dialog>
  );
}
export function SubmissionActions({ flow, rows, directLabel }: { flow: Submission; rows: ManagementRow[]; directLabel?: string }) {
  const { t } = useTranslation();
  const valid = flow.valid(rows);
  const approved = rows.filter((r) => flow.preview.some((p) => p.rowID === r.rowID && p.success));
  return (
    <div className='flex flex-col gap-2'>
      {flow.error && (
        <p role='alert' className='text-destructive text-sm'>
          {flow.error}
        </p>
      )}
      {flow.uncertain ? (
        <>
          <p role='status' className='text-sm'>
            {t('kg.uncertain')}
          </p>
          <Button disabled={flow.busy} onClick={() => flow.execute(true)}>
            {t('kg.recover')}
          </Button>
        </>
      ) : (
        <div className='flex flex-wrap items-center justify-end gap-3'>
          {!directLabel && valid && (
            <span className='text-sm'>{t('kg.previewCount', { valid: approved.length, invalid: rows.length - approved.length })}</span>
          )}
          {!directLabel && (
            <Button variant='outline' disabled={flow.busy || rows.length === 0 || rows.length > 100} onClick={() => flow.inspect(rows)}>
              {t('kg.preflight')}
            </Button>
          )}
          <Button
            disabled={flow.busy || (directLabel ? rows.length === 0 : !valid || approved.length === 0)}
            onClick={() => flow.execute(false, directLabel ? rows : approved)}
          >
            {flow.busy ? t('kg.working') : (directLabel ?? t('kg.submitValid', { count: approved.length }))}
          </Button>
        </div>
      )}
    </div>
  );
}
export function ResultsPanel({ flow, labels, onContinue }: { flow: Submission; labels: Map<string, string>; onContinue?: () => void }) {
  const { t } = useTranslation();
  const api = useManagementAPI();
  const groups = useKeyGroups();
  const [selected, setSelected] = useState<string[]>([]);
  const [copying, setCopying] = useState(false);
  const successful = flow.results.filter((r) => r.success);
  const visible = flow.results;
  const exportRows = async (rows: Result[], download = false) => {
    setCopying(true);
    try {
      const ids = rows.flatMap((r) => r.keyIDs);
      const records = [];
      for (let i = 0; i < ids.length; i += 100) records.push(...(await api.credentials(ids.slice(i, i + 100))));
      if (records.length !== ids.length) throw new Error(t('kg.resultsUnavailable'));
      const texts = records.map((k) => {
        const row = rows.find((r) => r.keyIDs.includes(Number(extractNumberID(k.id))));
        const group =
          groups.data?.find((g) => g.id === k.groupRef)?.name ??
          (k.groupRef ? (labels.get(row?.rowID ?? '') ?? String(k.groupRef)) : t('kg.ungrouped'));
        return [group, k.name, k.key];
      });
      const content = [[t('kg.group'), t('kg.keyName'), 'API KEY'], ...texts].map((r) => r.map(csvCell).join(',')).join('\r\n');
      if (download) {
        const url = URL.createObjectURL(new Blob(['\ufeff' + content], { type: 'text/csv;charset=utf-8' }));
        const a = document.createElement('a');
        a.href = url;
        a.download = 'axon-keys.csv';
        a.click();
        URL.revokeObjectURL(url);
      } else await navigator.clipboard.writeText(content);
      toast.success(t(download ? 'kg.exported' : 'kg.copied'));
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setCopying(false);
    }
  };
  if (!visible.length) return null;
  return (
    <section className='flex flex-col gap-3 rounded-md border p-3'>
      <h3 className='font-semibold'>{t('kg.results', { success: successful.length, failed: visible.length - successful.length })}</h3>
      <div className='max-h-48 overflow-auto'>
        {visible.map((r) => (
          <div key={r.rowID} className='flex min-h-10 items-center gap-3 border-b py-2 last:border-0'>
            <Checkbox
              aria-label={labels.get(r.rowID) ?? r.rowID}
              disabled={!r.success || !r.keyIDs.length}
              checked={selected.includes(r.rowID)}
              onCheckedChange={(v) => setSelected((s) => (v ? [...s, r.rowID] : s.filter((id) => id !== r.rowID)))}
            />
            <span className='min-w-0 flex-1 truncate'>{labels.get(r.rowID) ?? r.rowID}</span>
            {r.success ? (
              <>
                <span className='text-muted-foreground text-sm'>
                  {t('kg.done')} {r.keyIDs.length > 0 ? `· ${r.keyIDs.length} · ••••••••` : ''}
                </span>
                {r.keyIDs.length > 0 && (
                  <Button variant='outline' size='sm' disabled={copying} onClick={() => exportRows([r])}>
                    {t('kg.copyGroup')}
                  </Button>
                )}
              </>
            ) : (
              <span role='alert' className='text-destructive text-sm'>
                {r.error}
              </span>
            )}
          </div>
        ))}
      </div>
      <div className='flex flex-wrap gap-2'>
        <Button variant='outline' size='sm' onClick={() => setSelected(successful.filter((r) => r.keyIDs.length).map((r) => r.rowID))}>
          {t('kg.selectSuccess')}
        </Button>
        <Button
          variant='outline'
          size='sm'
          disabled={copying || !selected.length}
          onClick={() => exportRows(successful.filter((r) => selected.includes(r.rowID)))}
        >
          {t('kg.copySelected')}
        </Button>
        <Button
          variant='outline'
          size='sm'
          disabled={copying || !selected.length}
          onClick={() =>
            exportRows(
              successful.filter((r) => selected.includes(r.rowID)),
              true
            )
          }
        >
          {t('kg.exportSelected')}
        </Button>
        <Button
          variant='ghost'
          size='sm'
          onClick={() =>
            navigator.clipboard
              .writeText(location.origin + '/v1')
              .then(() => toast.success(t('kg.copied')))
              .catch((e) => toast.error(e.message))
          }
        >
          {t('kg.copyAPI')}
        </Button>
        <Button
          variant='ghost'
          size='sm'
          onClick={() =>
            navigator.clipboard
              .writeText(location.origin + '/self-usage')
              .then(() => toast.success(t('kg.copied')))
              .catch((e) => toast.error(e.message))
          }
        >
          {t('kg.copyUsage')}
        </Button>
        {onContinue && (
          <Button size='sm' onClick={onContinue}>
            {t('kg.continue')}
          </Button>
        )}
      </div>
      <p className='text-muted-foreground text-xs'>{t('kg.secretHelp')}</p>
    </section>
  );
}
