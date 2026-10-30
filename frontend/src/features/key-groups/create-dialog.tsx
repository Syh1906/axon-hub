import { useState } from 'react';
import { Copy, Plus, Trash2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { useSelectedProjectId } from '@/stores/projectStore';
import { extractNumberID } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Textarea } from '@/components/ui/textarea';
import { useApiKeyProfileTemplates } from '@/features/apikeys/data/apikeys';
import { useKeyGroups } from '@/features/apikeys/data/groups';
import { BudgetTime } from '@/features/budgets/budget-table';
import { useGeneralSettings } from '@/features/system/data/system';
import { importedGroup, materialize, newDraft, newKey, parseTable, type Defaults, type Draft } from './drafts';
import { BudgetPatchFields, Choice, GroupChoice, KeyInputFields, TemplateChoice } from './editors';
import { ResultsPanel, SubmissionActions, useSubmission } from './submission';

function Settings({
  value,
  onChange,
  groups,
  restrictions = false,
}: {
  value: Defaults;
  onChange: (value: Defaults) => void;
  groups: boolean;
  restrictions?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <div className='flex flex-col gap-4'>
      {groups && (
        <>
          <h3 className='font-medium'>{t('kg.sharedBudget')}</h3>
          <BudgetPatchFields value={value.budget} onChange={(budget) => onChange({ ...value, budget })} />
          <label className='flex items-center gap-2 text-sm'>
            <Checkbox checked={!!value.keys.length} onCheckedChange={(v) => onChange({ ...value, keys: v ? [newKey('Codex')] : [] })} />
            {t('kg.initialKeys')}
          </label>
        </>
      )}
      {value.keys.map((key, index) => (
        <section key={index} className='rounded-md border p-3'>
          <KeyInputFields
            value={key}
            hideName={!groups}
            expandedRestrictions={restrictions}
            onChange={(input) => onChange({ ...value, keys: value.keys.map((k, i) => (i === index ? input : k)) })}
          />
          {groups && (
            <Button variant='ghost' size='sm' onClick={() => onChange({ ...value, keys: value.keys.filter((_, i) => i !== index) })}>
              {t('kg.removeKey')}
            </Button>
          )}
        </section>
      ))}
      {groups && value.keys.length > 0 && (
        <Button variant='outline' size='sm' onClick={() => onChange({ ...value, keys: [...value.keys, newKey()] })}>
          <Plus />
          {t('kg.addInitialKey')}
        </Button>
      )}
    </div>
  );
}
export function CreateDialog({
  kind,
  initialGroups = [],
  onClose,
}: {
  kind: 'group' | 'groups' | 'key' | 'keys';
  initialGroups?: number[];
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const groupQuery = useKeyGroups();
  const { i18n } = useTranslation();
  const { data: generalSettings } = useGeneralSettings();
  const [groups, setGroups] = useState(kind === 'group' || kind === 'groups');
  const templates = useApiKeyProfileTemplates(useSelectedProjectId());
  const [batch, setBatch] = useState(kind === 'groups' || kind === 'keys');
  const [drafts, setDrafts] = useState<Draft[]>(() => (kind === 'group' || kind === 'key' ? [newDraft(initialGroups[0])] : []));
  const [defaults, setDefaults] = useState<Defaults>({ keys: [newKey(groups ? 'Codex' : '')] });
  const [targets, setTargets] = useState(initialGroups);
  const [names, setNames] = useState('Codex\nClaude');
  const [mode, setMode] = useState('same');
  const [importing, setImporting] = useState(false);
  const [pasted, setPasted] = useState('');
  const [table, setTable] = useState<string[][]>([]);
  const [columns, setColumns] = useState({ name: 0, note: 1, group: 0, template: -1 });
  const [skipHeader, setSkipHeader] = useState(false);
  const [editing, setEditing] = useState<{ id: string; value: Defaults; restrictions?: boolean } | null>(null);
  const flow = useSubmission();
  const locked = flow.busy || flow.uncertain;
  const completed = new Set(flow.results.filter((r) => r.success).map((r) => r.rowID));
  const active = drafts.filter((d) => !completed.has(d.id));
  const rows = materialize(active, defaults, groups);
  const labels = new Map(
    drafts.map((d) => [
      d.id,
      groups ? d.name : `${groupQuery.data?.find((g) => g.id === d.groupRef)?.name ?? t('kg.ungrouped')} / ${d.name}`,
    ])
  );
  const keyCount = rows.reduce((n, r) => n + (r.keys?.length ?? 0), 0);
  const patch = (id: string, change: Partial<Draft>) => setDrafts((list) => list.map((d) => (d.id === id ? { ...d, ...change } : d)));
  const append = (items: Draft[]) => {
    if (active.length + items.length > 100) {
      toast.error(t('kg.maxRows'));
      return;
    }
    setDrafts((old) => [...old, ...items]);
  };
  const parse = () => {
    try {
      const data = parseTable(pasted);
      setTable(data);
      setColumns({ name: data[0]?.length === 1 || groups ? 0 : 1, note: data[0]?.length > 1 ? 1 : -1, group: 0, template: -1 });
    } catch {
      toast.error(t('kg.unclosedQuote'));
    }
  };
  const addImported = () => {
    const imported: Draft[] = [];
    // 整份清单使用相同列映射；缺列的行不能退回单列默认归属。
    const columnCount = Math.max(0, ...table.map((cells) => cells.length));
    for (const cells of table.slice(skipHeader ? 1 : 0)) {
      let groupRef = 0;
      if (!groups) {
        const target = importedGroup(cells, columnCount, columns.group, targets, groupQuery.data ?? [], t('kg.ungrouped'));
        if ('error' in target) {
          toast.error(t(target.error, { name: target.name }));
          return;
        }
        groupRef = target.groupRef;
      }
      const templateName = !groups && columns.template >= 0 ? cells[columns.template]?.trim() : '';
      const template = templates.data?.find((t) => t.name === templateName);
      if (templateName && !template) {
        toast.error(t('kg.unknownTemplate', { name: templateName }));
        return;
      }
      imported.push({
        ...newDraft(groupRef),
        name: cells[columnCount === 1 ? 0 : columns.name]?.trim() ?? '',
        note: groups ? (cells[columns.note] ?? '') : '',
        ...(template
          ? {
              overrides: {
                ...structuredClone(defaults),
                keys: [{ ...structuredClone(defaults.keys[0] ?? newKey()), templateID: Number(extractNumberID(template.id)) }],
              },
            }
          : {}),
      });
    }
    if (active.length + imported.length > 100) {
      toast.error(t('kg.maxRows'));
      return;
    }
    append(imported);
    setImporting(false);
    setPasted('');
    setTable([]);
  };
  const generate = () => {
    const list = names
      .split(/[\n,]+/)
      .map((n) => n.trim())
      .filter(Boolean);
    // Append only missing pairs; existing per-row edits and selections survive.
    const existing = new Set(active.map((d) => `${d.groupRef}\u0000${d.name}`));
    const additions: Draft[] = [];
    for (const id of targets)
      for (const name of list) {
        const key = `${id}\u0000${name}`;
        if (!existing.has(key)) {
          additions.push({ ...newDraft(id), name });
          existing.add(key);
        }
      }
    append(additions);
  };
  return (
    <>
      <Dialog
        open
        onOpenChange={(open) => {
          if (!open && !flow.busy) onClose();
        }}
      >
        <DialogContent
          className={
            batch ? 'flex max-h-[92dvh] flex-col overflow-hidden sm:max-w-6xl' : 'flex max-h-[92dvh] flex-col overflow-hidden sm:max-w-2xl'
          }
          showCloseButton={!flow.busy}
        >
          <DialogHeader>
            <DialogTitle>{t(groups ? (batch ? 'kg.batchGroups' : 'kg.createGroup') : batch ? 'kg.batchKeys' : 'kg.addKey')}</DialogTitle>
            <DialogDescription>{t(groups ? 'kg.createGroupHelp' : 'kg.createKeyHelp')}</DialogDescription>
          </DialogHeader>
          <div className='min-h-0 flex-1 overflow-y-auto'>
            <fieldset disabled={locked} className='flex min-w-0 flex-col gap-5'>
              {!batch && active[0] && (
                <>
                  {!groups && (
                    <div className='flex items-center gap-3'>
                      <Label>{t('kg.group')}</Label>
                      <span className='font-medium'>
                        {groupQuery.data?.find((g) => g.id === active[0].groupRef)?.name ?? t('kg.ungrouped')}
                      </span>
                    </div>
                  )}
                  <Label htmlFor='create-name'>{t(groups ? 'kg.groupName' : 'kg.keyName')}</Label>
                  <Input
                    id='create-name'
                    autoFocus
                    value={active[0].name}
                    onChange={(e) => patch(active[0].id, { name: e.target.value })}
                  />
                  {groups && (
                    <>
                      <Label htmlFor='create-note'>{t('kg.note')}</Label>
                      <Input id='create-note' value={active[0].note} onChange={(e) => patch(active[0].id, { note: e.target.value })} />
                    </>
                  )}
                  <Settings value={defaults} onChange={setDefaults} groups={groups} />
                  <Button variant='link' className='self-start' onClick={() => setBatch(true)}>
                    {t('kg.switchBatch')}
                  </Button>
                </>
              )}
              {batch && (
                <>
                  {!groups && (
                    <>
                      <Tabs value={mode} onValueChange={setMode}>
                        <TabsList>
                          <TabsTrigger value='same'>{t('kg.sameSet')}</TabsTrigger>
                          <TabsTrigger value='rows'>{t('kg.rowMode')}</TabsTrigger>
                        </TabsList>
                      </Tabs>
                      {mode === 'same' && (
                        <div className='flex flex-col gap-3 rounded-md border p-3'>
                          <Label>{t('kg.targetGroups')}</Label>
                          <div className='flex max-h-32 flex-wrap gap-3 overflow-auto'>
                            {[{ id: 0, name: t('kg.ungrouped') }, ...(groupQuery.data ?? [])].map((g) => (
                              <label key={g.id} className='flex items-center gap-2 text-sm'>
                                <Checkbox
                                  checked={targets.includes(g.id)}
                                  onCheckedChange={(v) => setTargets((s) => (v ? [...s, g.id] : s.filter((id) => id !== g.id)))}
                                />
                                {g.name}
                              </label>
                            ))}
                          </div>
                          <Label htmlFor='key-set'>{t('kg.keySet')}</Label>
                          <Textarea id='key-set' rows={2} value={names} onChange={(e) => setNames(e.target.value)} />
                          <div className='flex items-center justify-between gap-2'>
                            <p className='text-muted-foreground text-sm'>
                              {t('kg.cartesian', { groups: targets.length, keys: names.split(/[\n,]+/).filter((n) => n.trim()).length })}
                            </p>
                            <Button variant='outline' onClick={generate}>
                              {t('kg.generateList')}
                            </Button>
                          </div>
                        </div>
                      )}
                    </>
                  )}
                  <details className='bg-muted/30 rounded-md border p-3'>
                    <summary className='cursor-pointer font-medium'>{t('kg.defaults')}</summary>
                    <div className='pt-4'>
                      <Settings value={defaults} onChange={setDefaults} groups={groups} />
                    </div>
                  </details>
                  <div className='flex flex-wrap items-center gap-2'>
                    <h3 className='mr-auto font-semibold'>{t('kg.creationList')}</h3>
                    <Button variant='outline' size='sm' onClick={() => setImporting(true)}>
                      {t('kg.pasteCSV')}
                    </Button>
                    <Button variant='outline' size='sm' onClick={() => append([newDraft(initialGroups[0])])}>
                      <Plus />
                      {t('kg.addRow')}
                    </Button>
                  </div>
                  <div className='max-h-[38vh] overflow-auto rounded-md border'>
                    <Table className='min-w-[850px]'>
                      <TableHeader className='bg-background sticky top-0 z-10'>
                        <TableRow>
                          <TableHead className='w-10'>
                            <Checkbox
                              aria-label={t('kg.selectPage')}
                              checked={active.length > 0 && active.every((d) => d.selected)}
                              onCheckedChange={(v) =>
                                setDrafts((list) => list.map((d) => (completed.has(d.id) ? d : { ...d, selected: !!v })))
                              }
                            />
                          </TableHead>
                          <TableHead>{t(groups ? 'kg.groupName' : 'kg.group')}</TableHead>
                          <TableHead>{t(groups ? 'kg.note' : 'kg.keyName')}</TableHead>
                          <TableHead>{t(groups ? 'kg.initialKeysShort' : 'kg.template')}</TableHead>
                          <TableHead>{t(groups ? 'kg.sharedBudget' : 'kg.individualBudgetShort')}</TableHead>
                          <TableHead>{t('budgets.expiry')}</TableHead>
                          <TableHead>{t('kg.check')}</TableHead>
                          <TableHead>{t('kg.actions')}</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {active.map((d) => {
                          const status =
                            flow.statusFor(rows.find((r) => r.rowID === d.id)) ?? flow.results.find((r) => r.rowID === d.id && !r.success);
                          const effective = d.overrides ?? defaults;
                          const rowBudget = groups ? effective.budget : effective.keys[0]?.budget;
                          const limits = rowBudget?.rules?.filter((r) => Number(r.limit) > 0) ?? [];
                          const amount = (limit: string) =>
                            t('currencies.format', {
                              val: Number(limit),
                              currency: generalSettings?.currencyCode ?? 'USD',
                              locale: i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US',
                              minimumFractionDigits: 6,
                            });
                          return (
                            <TableRow key={d.id}>
                              <TableCell>
                                <Checkbox
                                  aria-label={d.name || t('kg.selectRow')}
                                  checked={d.selected}
                                  onCheckedChange={(v) => patch(d.id, { selected: !!v })}
                                />
                              </TableCell>
                              <TableCell>
                                {groups ? (
                                  <Input
                                    aria-label={t('kg.groupName')}
                                    value={d.name}
                                    onChange={(e) => patch(d.id, { name: e.target.value })}
                                  />
                                ) : (
                                  <GroupChoice value={d.groupRef} onChange={(groupRef) => patch(d.id, { groupRef })} />
                                )}
                              </TableCell>
                              <TableCell>
                                <Input
                                  aria-label={t(groups ? 'kg.note' : 'kg.keyName')}
                                  value={groups ? d.note : d.name}
                                  onChange={(e) => patch(d.id, groups ? { note: e.target.value } : { name: e.target.value })}
                                />
                              </TableCell>
                              <TableCell>
                                <div className='flex flex-col items-start'>
                                  {!groups && (
                                    <TemplateChoice
                                      value={effective.keys[0]?.templateID}
                                      onChange={(templateID) =>
                                        patch(d.id, {
                                          overrides: {
                                            ...structuredClone(effective),
                                            keys: [{ ...structuredClone(effective.keys[0] ?? newKey()), templateID }],
                                          },
                                        })
                                      }
                                    />
                                  )}
                                  <div className='flex items-center'>
                                    <Button
                                      variant='ghost'
                                      size='sm'
                                      className='h-7 px-1 text-xs'
                                      onClick={() => setEditing({ id: d.id, value: structuredClone(d.overrides ?? defaults) })}
                                    >
                                      <span className='block max-w-36 truncate' title={effective.keys.map((k) => k.name).join(' / ')}>
                                        {groups
                                          ? `${effective.keys.length} · ${effective.keys.map((k) => k.name).join(' / ')}`
                                          : t(d.overrides ? 'kg.custom' : 'kg.inherited')}
                                      </span>
                                    </Button>
                                    {d.overrides && (
                                      <Button
                                        variant='link'
                                        size='sm'
                                        className='h-7 px-1 text-xs'
                                        onClick={() => patch(d.id, { overrides: undefined })}
                                      >
                                        {t('kg.restoreDefaults')}
                                      </Button>
                                    )}
                                  </div>
                                </div>
                              </TableCell>
                              <TableCell>
                                <Button
                                  variant='ghost'
                                  size='sm'
                                  className='h-auto max-w-40 flex-col items-start text-xs'
                                  onClick={() => setEditing({ id: d.id, value: structuredClone(effective), restrictions: true })}
                                >
                                  {limits.length ? (
                                    <>
                                      <span>
                                        {t('budgets.periods.' + limits[0].period)} {amount(limits[0].limit)}
                                      </span>
                                      {limits.length > 1 && <span>{t('kg.moreLimits', { count: limits.length - 1 })}</span>}
                                    </>
                                  ) : (
                                    t('budgets.notConfigured')
                                  )}
                                  <span className='text-muted-foreground'>{t(d.overrides ? 'kg.custom' : 'kg.inherited')}</span>
                                </Button>
                              </TableCell>
                              <TableCell>
                                <Button
                                  variant='ghost'
                                  size='sm'
                                  onClick={() => setEditing({ id: d.id, value: structuredClone(effective), restrictions: true })}
                                >
                                  {rowBudget?.setExpiry && rowBudget.expiresAt ? (
                                    <BudgetTime value={rowBudget.expiresAt} timezone={Intl.DateTimeFormat().resolvedOptions().timeZone} />
                                  ) : (
                                    t('budgets.neverExpires')
                                  )}
                                </Button>
                              </TableCell>
                              <TableCell className='max-w-52 text-xs whitespace-normal'>
                                {status ? (
                                  status.success ? (
                                    t('kg.valid')
                                  ) : (
                                    <span role='alert' className='text-destructive'>
                                      {status.error}
                                    </span>
                                  )
                                ) : (
                                  t('kg.unchecked')
                                )}
                              </TableCell>
                              <TableCell>
                                <div className='flex'>
                                  <Button
                                    size='icon'
                                    variant='ghost'
                                    aria-label={t('kg.duplicate')}
                                    onClick={() => append([{ ...structuredClone(d), id: crypto.randomUUID() }])}
                                  >
                                    <Copy className='size-4' />
                                  </Button>
                                  <Button
                                    size='icon'
                                    variant='ghost'
                                    aria-label={t('kg.removeRow')}
                                    onClick={() => setDrafts((list) => list.filter((r) => r.id !== d.id))}
                                  >
                                    <Trash2 className='size-4' />
                                  </Button>
                                </div>
                              </TableCell>
                            </TableRow>
                          );
                        })}
                        {!active.length && (
                          <TableRow>
                            <TableCell colSpan={8} className='text-muted-foreground py-8 text-center'>
                              {t('kg.emptyDraft')}
                            </TableCell>
                          </TableRow>
                        )}
                      </TableBody>
                    </Table>
                  </div>
                </>
              )}
            </fieldset>
            {!batch &&
              flow.preview
                .filter((r) => !r.success)
                .map((r) => (
                  <p key={r.rowID} role='alert' className='text-destructive text-sm'>
                    {r.error}
                  </p>
                ))}
            <ResultsPanel
              flow={flow}
              labels={labels}
              onContinue={
                !batch && flow.results.some((r) => r.success)
                  ? () => {
                      const group = flow.results.find((r) => r.success)?.groupID ?? initialGroups[0];
                      setDrafts([newDraft(group)]);
                      setDefaults({ keys: [{ ...structuredClone(defaults.keys[0] ?? newKey()), name: '' }] });
                      setGroups(false);
                      setBatch(false);
                      flow.clear();
                    }
                  : undefined
              }
            />
          </div>
          <DialogFooter className='flex-col border-t pt-4 sm:flex-col'>
            <p className='mr-auto text-sm'>
              {t('kg.createCount', { groups: groups ? rows.length : new Set(rows.map((r) => r.groupRef)).size, keys: keyCount })}
            </p>
            <SubmissionActions flow={flow} rows={rows} />
            <Button variant='ghost' disabled={flow.busy} onClick={onClose}>
              {t('common.buttons.close')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      {editing && (
        <Dialog
          open
          onOpenChange={(open) => {
            if (!open) setEditing(null);
          }}
        >
          <DialogContent className='flex max-h-[90dvh] flex-col overflow-hidden sm:max-w-2xl'>
            <DialogHeader>
              <DialogTitle>{t('kg.rowSettings')}</DialogTitle>
              <DialogDescription>{t('kg.overrideHelp')}</DialogDescription>
            </DialogHeader>
            <div className='min-h-0 overflow-y-auto'>
              <Settings
                value={editing.value}
                groups={groups}
                restrictions={editing.restrictions}
                onChange={(value) => setEditing({ ...editing, value })}
              />
            </div>
            <DialogFooter>
              <Button variant='outline' onClick={() => setEditing(null)}>
                {t('common.buttons.cancel')}
              </Button>
              <Button
                onClick={() => {
                  patch(editing.id, { overrides: editing.value });
                  setEditing(null);
                }}
              >
                {t('common.buttons.save')}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
      {importing && (
        <Dialog open onOpenChange={setImporting}>
          <DialogContent className='sm:max-w-2xl'>
            <DialogHeader>
              <DialogTitle>{t('kg.pasteCSV')}</DialogTitle>
              <DialogDescription>{t('kg.importHelp')}</DialogDescription>
            </DialogHeader>
            <Textarea
              rows={6}
              value={pasted}
              onChange={(e) => {
                setPasted(e.target.value);
                setTable([]);
              }}
              aria-label={t('kg.pasteCSV')}
            />
            <Input
              type='file'
              accept='.csv,.tsv,.txt'
              aria-label={t('kg.importFile')}
              onChange={async (e) => {
                const file = e.target.files?.[0];
                if (file) {
                  setPasted(await file.text());
                  setTable([]);
                }
              }}
            />
            <Button variant='outline' onClick={parse}>
              {t('kg.parse')}
            </Button>
            {!!table.length && (
              <>
                <label className='flex items-center gap-2'>
                  <Checkbox checked={skipHeader} onCheckedChange={(v) => setSkipHeader(!!v)} />
                  {t('kg.headerRow')}
                </label>
                <div className='flex flex-wrap gap-3'>
                  {(groups ? ['name', 'note'] : ['group', 'name', 'template']).map((field) => (
                    <div key={field}>
                      <Label>{t(field === 'name' ? (groups ? 'kg.groupName' : 'kg.keyName') : 'kg.' + field)}</Label>
                      <Choice
                        label={t(field === 'name' ? (groups ? 'kg.groupName' : 'kg.keyName') : 'kg.' + field)}
                        value={String(columns[field as keyof typeof columns])}
                        onChange={(v) => setColumns({ ...columns, [field]: Number(v) })}
                        options={[
                          ...(field === 'note' || field === 'template' ? [{ value: '-1', label: t('kg.skipColumn') }] : []),
                          ...Array.from({ length: Math.max(...table.map((r) => r.length)) }, (_, i) => ({
                            value: String(i),
                            label: t('kg.column', { number: i + 1 }) + ' · ' + (table[0]?.[i] ?? ''),
                          })),
                        ]}
                      />
                    </div>
                  ))}
                </div>
                <p className='text-muted-foreground text-sm'>{t('kg.importCount', { count: table.length - (skipHeader ? 1 : 0) })}</p>
              </>
            )}
            <DialogFooter>
              <Button variant='outline' onClick={() => setImporting(false)}>
                {t('common.buttons.cancel')}
              </Button>
              <Button disabled={!table.length} onClick={addImported}>
                {t('kg.appendList')}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </>
  );
}
