import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { useSelectedProjectId } from '@/stores/projectStore';
import { extractNumberID } from '@/lib/utils';
import { usePermissions } from '@/hooks/usePermissions';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { useKeyGroups } from '@/features/apikeys/data/groups';
import type { Group, Member, ManagementRow, BudgetPatch } from './data';
import { useManagementAPI } from './data';
import { BudgetPatchFields, Choice, GroupChoice, TemplateChoice } from './editors';
import { ResultsPanel, SubmissionActions, useSubmission } from './submission';

export type Selection = { kind: 'group'; items: Group[] } | { kind: 'key'; items: Member[] };
export function ManageDialog({
  selection,
  onClose,
  initialAction = 'edit',
  initialGroup,
}: {
  selection: Selection;
  onClose: () => void;
  initialAction?: string;
  initialGroup?: number;
}) {
  const { t } = useTranslation();
  const groups = selection.kind === 'group';
  const single = selection.items.length === 1;
  const first = selection.items[0];
  const [action, setAction] = useState(initialAction);
  const [fields, setFields] = useState<string[]>(initialGroup === undefined ? [] : ['group']);
  const [name, setName] = useState(first?.name ?? '');
  const [note, setNote] = useState(groups && single ? (first as Group).note : '');
  const [noteMode, setNoteMode] = useState('set');
  const [enabled, setEnabled] = useState('true');
  const [groupRef, setGroupRef] = useState(initialGroup ?? 0);
  const [template, setTemplate] = useState<number>();
  const [ips, setIPs] = useState(single && !groups ? (first as Member).allowedIps.join('\n') : '');
  const [ipMode, setIPMode] = useState('set');
  const [budget, setBudget] = useState<BudgetPatch>({});
  const flow = useSubmission();
  const locked = flow.busy || flow.uncertain;
  const has = (field: string) => fields.includes(field);
  const api = useManagementAPI();
  const project = useSelectedProjectId();
  const { user } = usePermissions();
  const budgetRevisions = useQuery({
    queryKey: ['keyBudgets', user?.id, project, 'managementRevisions', selection.items.map((i) => i.id)],
    enabled: !groups && has('budget'),
    refetchOnWindowFocus: false,
    staleTime: 0,
    queryFn: async () =>
      Promise.all(
        selection.items.map(async (i) => {
          const id = Number(extractNumberID(String(i.id)));
          return { id, revision: (await api.budget(id)).revision };
        })
      ),
  });
  const successful = new Set(flow.results.filter((r) => r.success).map((r) => r.rowID));
  const rows: ManagementRow[] = selection.items
    .map((item) => {
      const id = groups ? (item as Group).id : Number(extractNumberID((item as Member).id));
      return {
        rowID: String(id),
        targetID: id,
        action: action === 'delete' ? (groups ? 'deleteGroup' : 'archiveKey') : groups ? 'updateGroup' : 'updateKey',
        ...(groups ? { revision: (item as Group).revision } : { updatedAt: (item as Member).updatedAt }),
        ...(action === 'edit'
          ? {
              ...(single && has('name') ? { name: name.trim() } : {}),
              ...(groups && has('note') ? { note: noteMode === 'clear' ? '' : note } : {}),
              ...(has('enabled') ? { enabled: enabled === 'true' } : {}),
              ...(has('budget')
                ? {
                    budget: {
                      ...budget,
                      revision: groups ? (item as Group).budget.revision : budgetRevisions.data?.find((b) => b.id === id)?.revision,
                    },
                  }
                : {}),
              ...(!groups && has('group') ? { groupRef } : {}),
              ...(!groups && has('template') ? { templateID: template } : {}),
              ...(!groups && has('ips')
                ? { setAllowedIPs: true, allowedIPs: ipMode === 'clear' ? [] : ips.split(/[\s,]+/).filter(Boolean) }
                : {}),
            }
          : {}),
      };
    })
    .filter((r) => !successful.has(r.rowID));
  const groupOptions = useKeyGroups();
  const itemLabel = (item: Group | Member) =>
    groups ? item.name : `${groupOptions.data?.find((g) => g.id === (item as Member).groupRef)?.name ?? t('kg.ungrouped')} / ${item.name}`;
  const labels = new Map(
    selection.items.map((i) => {
      const id = String(groups ? (i as Group).id : extractNumberID((i as Member).id));
      const submitted = flow.submitted.find((r) => r.rowID === id);
      let label = itemLabel(i);
      if (submitted?.name || submitted?.groupRef !== undefined) {
        const destination = groups
          ? ''
          : `${groupOptions.data?.find((g) => g.id === (submitted.groupRef ?? (i as Member).groupRef))?.name ?? t('kg.ungrouped')} / `;
        label += ` → ${destination}${submitted.name ?? i.name}`;
      }
      return [id, label];
    })
  );
  const field = (key: string) => (
    <label className='flex items-center gap-2 text-sm font-medium'>
      <Checkbox checked={has(key)} onCheckedChange={(v) => setFields((old) => (v ? [...old, key] : old.filter((f) => f !== key)))} />
      {t('kg.field.' + key)}
    </label>
  );
  const incomplete =
    (has('template') && !template) ||
    (has('note') && noteMode === 'set' && !note.trim()) ||
    (has('ips') && ipMode === 'set' && !ips.trim()) ||
    (!groups && has('budget') && !budgetRevisions.data);
  return (
    <Dialog
      open
      onOpenChange={(v) => {
        if (!v && !flow.busy) onClose();
      }}
    >
      <DialogContent className='flex max-h-[92dvh] flex-col overflow-hidden sm:max-w-3xl' showCloseButton={!flow.busy}>
        <DialogHeader>
          <DialogTitle>{t(groups ? 'kg.manageGroups' : 'kg.manageKeys')}</DialogTitle>
          <DialogDescription>
            {t('kg.selected', { count: selection.items.length })} · {t('kg.patchHelp')}
          </DialogDescription>
        </DialogHeader>
        <div className='min-h-0 flex-1 overflow-y-auto'>
          <fieldset disabled={locked} className='flex min-w-0 flex-col gap-4'>
            <details className='rounded-md border p-3'>
              <summary className='cursor-pointer'>{t('kg.selectedList')}</summary>
              <ul className='max-h-32 overflow-auto pt-2 text-sm'>
                {selection.items.map((i) => (
                  <li key={i.id}>{itemLabel(i)}</li>
                ))}
              </ul>
            </details>
            <Choice
              label={t('kg.action')}
              value={action}
              onChange={setAction}
              options={[
                { value: 'edit', label: t('kg.editSettings') },
                { value: 'delete', label: t(groups ? 'kg.deleteGroups' : 'kg.archiveKeys') },
              ]}
            />
            {action === 'delete' ? (
              <p className='rounded-md border p-3 text-sm'>{t(groups ? 'kg.deleteHelp' : 'kg.archiveHelp')}</p>
            ) : (
              <>
                {single && (
                  <>
                    {field('name')}
                    {has('name') && <Input aria-label={t('kg.field.name')} value={name} onChange={(e) => setName(e.target.value)} />}
                  </>
                )}
                {groups && (
                  <>
                    {field('note')}
                    {has('note') && (
                      <>
                        <Choice
                          label={t('kg.note')}
                          value={noteMode}
                          onChange={setNoteMode}
                          options={[
                            { value: 'set', label: t('kg.setValue') },
                            { value: 'clear', label: t('kg.clearValue') },
                          ]}
                        />
                        {noteMode === 'set' && <Input aria-label={t('kg.note')} value={note} onChange={(e) => setNote(e.target.value)} />}
                      </>
                    )}
                  </>
                )}
                {field('enabled')}
                {has('enabled') && (
                  <Choice
                    label={t('kg.status')}
                    value={enabled}
                    onChange={setEnabled}
                    options={[
                      { value: 'true', label: t('kg.enabled') },
                      { value: 'false', label: t('kg.disabled') },
                    ]}
                  />
                )}
                {!groups && (
                  <>
                    {field('group')}
                    {has('group') && <GroupChoice value={groupRef} onChange={setGroupRef} />}
                    {field('template')}
                    {has('template') && <TemplateChoice value={template} onChange={setTemplate} />}
                    {field('ips')}
                    {has('ips') && (
                      <>
                        <Choice
                          label={t('kg.ips')}
                          value={ipMode}
                          onChange={setIPMode}
                          options={[
                            { value: 'set', label: t('kg.setValue') },
                            { value: 'clear', label: t('kg.clearValue') },
                          ]}
                        />
                        {ipMode === 'set' && <Textarea value={ips} onChange={(e) => setIPs(e.target.value)} aria-label={t('kg.ips')} />}
                      </>
                    )}
                  </>
                )}
                {field('budget')}
                {has('budget') && <BudgetPatchFields value={budget} onChange={setBudget} />}
                <p className='text-muted-foreground text-sm'>{t(groups ? 'kg.groupEffect' : 'kg.keyEffect')}</p>
              </>
            )}
          </fieldset>
          {flow.valid(rows) &&
            flow.preview.map((r) => (
              <p key={r.rowID} className='py-1 text-sm' role={r.success ? 'status' : 'alert'}>
                {labels.get(r.rowID)} · {r.success ? t('kg.valid') : r.error}
              </p>
            ))}
          <ResultsPanel flow={flow} labels={labels} />
        </div>
        <DialogFooter className='flex-col border-t pt-4 sm:flex-col'>
          <Label>
            {action === 'delete'
              ? t(groups ? 'kg.deleteGroups' : 'kg.archiveKeys')
              : t('kg.changeSummary', { fields: fields.map((f) => t('kg.field.' + f)).join('、') || t('kg.unchanged') })}
          </Label>
          {incomplete && (
            <p role='alert' className='text-destructive text-sm'>
              {t('kg.valueRequired')}
            </p>
          )}
          <SubmissionActions flow={flow} rows={incomplete || (action === 'edit' && !fields.length) ? [] : rows} />
          <Button variant='ghost' disabled={flow.busy} onClick={onClose}>
            {t('common.buttons.close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
