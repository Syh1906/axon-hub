import { Fragment, useEffect, useState } from 'react';
import { Link } from '@tanstack/react-router';
import { BarChart3, ChevronDown, ChevronRight, MoreHorizontal, Plus, RefreshCw, Settings2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useSelectedProjectId } from '@/stores/projectStore';
import { useDebounce } from '@/hooks/use-debounce';
import { usePermissions } from '@/hooks/usePermissions';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger, DropdownMenuItem, DropdownMenuGroup } from '@/components/ui/dropdown-menu';
import { Input } from '@/components/ui/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Header } from '@/components/layout/header';
import { Main } from '@/components/layout/main';
import { ApiKeysDialogs } from '@/features/apikeys/components/apikeys-dialogs';
import { ApiKeysProvider, useApiKeysContext } from '@/features/apikeys/context/apikeys-context';
import { useApiKey } from '@/features/apikeys/data/apikeys';
import { BudgetDialog } from '@/features/budgets/budget-dialog';
import { useGeneralSettings } from '@/features/system/data/system';
import { CreateDialog } from './create-dialog';
import { useManagedGroups, useManagementAPI, type Group, type Member } from './data';
import { Choice, periods } from './editors';
import { ManageDialog, type Selection } from './manage-dialog';
import { Members } from './members';
import { MoveDialog } from './move-dialog';
import { subscribePendingRecovery } from './pending';
import { GroupQuickEdit, GroupToggle } from './quick-edit';
import { PendingRecovery } from './submission';

function ProfileLoader({ id, onLoaded }: { id: string; onLoaded: () => void }) {
  const query = useApiKey(id);
  const { openDialog } = useApiKeysContext();
  useEffect(() => {
    if (query.data) {
      openDialog('profiles', query.data);
      onLoaded();
    }
  }, [query.data]);
  return query.error ? <p role='alert'>{query.error.message}</p> : null;
}
function GroupPage() {
  const { t, i18n } = useTranslation();
  const { apiKeyPermissions } = usePermissions();
  const writable = apiKeyPermissions.canWrite;
  const { data: settings } = useGeneralSettings();
  const api = useManagementAPI();
  const [search, setSearch] = useState('');
  const debounced = useDebounce(search, 300);
  const [status, setStatus] = useState('all');
  const [descending, setDescending] = useState(true);
  const [offset, setOffset] = useState(0);
  const [limit, setLimit] = useState(20);
  const query = useManagedGroups(debounced, status, offset, limit, descending);
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [mode, setMode] = useState('normal');
  const [selectedGroups, setSelectedGroups] = useState<Map<number, Group>>(new Map());
  const [selectedKeys, setSelectedKeys] = useState<Map<string, Member>>(new Map());
  const [create, setCreate] = useState<{ kind: 'group' | 'groups' | 'key' | 'keys'; ids?: number[] } | null>(null);
  const [edit, setEdit] = useState<Selection | null>(null);
  const [budget, setBudget] = useState<Group | null>(null);
  const [profile, setProfile] = useState<string | null>(null);
  const [move, setMove] = useState<Group | null>(null);
  const [recoveryVersion, setRecoveryVersion] = useState(0);
  const deferRecovery = () => setRecoveryVersion((value) => value + 1);
  useEffect(() => subscribePendingRecovery(() => setRecoveryVersion((value) => value + 1)), []);
  const groups = query.data?.items ?? [];
  const allGroupsCollapsed = !expanded.has(0) && groups.every((g) => !expanded.has(g.id));
  const selectedCount = mode === 'group' ? selectedGroups.size : selectedKeys.size;
  function changeMode(next: string) {
    setMode(next);
    setSelectedGroups(new Map());
    setSelectedKeys(new Map());
  }
  function toggle(id: number) {
    setExpanded((old) => {
      const next = new Set(old);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  }
  function selectGroups(items: Group[], checked: boolean) {
    setSelectedGroups((old) => {
      const next = new Map(old);
      items.forEach((g) => (checked ? next.set(g.id, g) : next.delete(g.id)));
      return next;
    });
  }
  function selectKeys(items: Member[], checked: boolean) {
    setSelectedKeys((old) => {
      const next = new Map(old);
      items.forEach((k) => (checked ? next.set(k.id, k) : next.delete(k.id)));
      return next;
    });
  }
  const currency = (value: string) =>
    t('currencies.format', {
      val: Number(value),
      currency: settings?.currencyCode || 'USD',
      locale: i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US',
      minimumFractionDigits: 2,
      maximumFractionDigits: 8,
    });
  const child = (id: number) => (
    <Members
      key={id}
      groupRef={id}
      selecting={mode === 'key'}
      selected={selectedKeys}
      onSelect={selectKeys}
      writable={writable}
      onAdd={() => setCreate({ kind: 'key', ids: [id] })}
      onEdit={(key) => setEdit({ kind: 'key', items: [key] })}
      onProfile={setProfile}
      onMove={
        id > 0
          ? () => {
              const group = groups.find((g) => g.id === id);
              if (group) setMove(group);
            }
          : undefined
      }
    />
  );
  return (
    <>
      <Header className='h-auto min-h-16 shrink-0'>
        <div className='flex min-w-0 flex-1 flex-wrap items-center justify-between gap-3'>
          <div>
            <h2 className='text-xl font-bold tracking-tight'>{t('kg.title')}</h2>
            <p className='text-muted-foreground text-sm'>{t('kg.description')}</p>
          </div>
          {writable && (
            <div className='flex flex-wrap gap-2'>
              <Button size='sm' variant='outline' onClick={() => setCreate({ kind: 'keys' })}>
                {t('kg.batchKeys')}
              </Button>
              <Button size='sm' variant='outline' onClick={() => setCreate({ kind: 'groups' })}>
                {t('kg.batchGroups')}
              </Button>
              <Button size='sm' onClick={() => setCreate({ kind: 'group' })}>
                <Plus />
                {t('kg.createGroup')}
              </Button>
            </div>
          )}
        </div>
      </Header>
      <Main fixed>
        <div className='flex min-h-0 flex-1 flex-col gap-3'>
          {!create && !edit && !move && <PendingRecovery key={recoveryVersion} initiallyClosed={recoveryVersion > 0} />}
          <div className='flex flex-wrap items-center gap-2'>
            <Input
              className='w-full sm:max-w-sm'
              placeholder={t('kg.search')}
              aria-label={t('kg.search')}
              value={search}
              onChange={(e) => {
                setSearch(e.target.value);
                setOffset(0);
              }}
            />
            <div className='w-40'>
              <Choice
                label={t('kg.status')}
                value={status}
                onChange={(v) => {
                  setStatus(v);
                  setOffset(0);
                }}
                options={['all', 'enabled', 'disabled', 'expired', 'exhausted'].map((s) => ({ value: s, label: t('kg.' + s) }))}
              />
            </div>
            <Button
              variant='outline'
              size='sm'
              onClick={() => {
                setDescending((v) => !v);
                setOffset(0);
              }}
            >
              {t('kg.createdAt')} {descending ? '↓' : '↑'}
            </Button>
            {writable && mode === 'normal' && (
              <Button size='sm' variant='outline' onClick={() => changeMode('group')}>
                {t('kg.batchManage')}
              </Button>
            )}
            <div className='ml-auto flex gap-2'>
              <Button
                variant='outline'
                size='sm'
                onClick={() => setExpanded(allGroupsCollapsed ? new Set([0, ...groups.map((g) => g.id)]) : new Set())}
              >
                {t(allGroupsCollapsed ? 'kg.expand' : 'kg.collapse')}
              </Button>
              <Button variant='outline' size='icon' aria-label={t('kg.reload')} onClick={() => api.refresh()}>
                <RefreshCw className='size-4' />
              </Button>
            </div>
          </div>
          {writable && mode !== 'normal' && (
            <div className='bg-muted/20 flex flex-wrap items-center gap-3 rounded-lg border px-3 py-2'>
              <span className='text-sm'>{t('kg.manageObject')}</span>
              <Tabs value={mode} onValueChange={changeMode}>
                <TabsList>
                  <TabsTrigger value='group'>{t('kg.groups')}</TabsTrigger>
                  <TabsTrigger value='key'>{t('kg.keys')}</TabsTrigger>
                </TabsList>
              </Tabs>
              <span className='text-sm'>{t('kg.selected', { count: selectedCount })}</span>
              <Button
                size='sm'
                variant='outline'
                disabled={!selectedCount}
                onClick={() =>
                  setEdit(
                    mode === 'group'
                      ? { kind: 'group', items: [...selectedGroups.values()] }
                      : { kind: 'key', items: [...selectedKeys.values()] }
                  )
                }
              >
                <Settings2 className='size-4' />
                {t('kg.editSettings')}
              </Button>
              {mode === 'group' && (
                <Button
                  size='sm'
                  variant='outline'
                  disabled={!selectedGroups.size}
                  onClick={() => setCreate({ kind: 'keys', ids: [...selectedGroups.keys()] })}
                >
                  {t('kg.addKey')}
                </Button>
              )}
              <Button
                size='sm'
                variant='ghost'
                onClick={() => {
                  setSelectedGroups(new Map());
                  setSelectedKeys(new Map());
                }}
              >
                {t('kg.clearSelection')}
              </Button>
              <Button className='ml-auto' size='sm' variant='outline' onClick={() => changeMode('normal')}>
                {t('kg.exitBatch')}
              </Button>
            </div>
          )}
          {query.error && (
            <div role='alert' className='text-destructive'>
              {query.error.message}
              <Button variant='outline' onClick={() => query.refetch()}>
                {t('kg.reload')}
              </Button>
            </div>
          )}
          <div className='min-h-0 flex-1 overflow-auto rounded-xl border'>
            <Table className='min-w-[1120px]'>
              <TableHeader>
                <TableRow className='bg-muted/50'>
                  <TableHead className='w-10'>
                    {mode === 'group' && (
                      <Checkbox
                        aria-label={t('kg.selectPage')}
                        checked={groups.length > 0 && groups.every((g) => selectedGroups.has(g.id))}
                        onCheckedChange={(v) => selectGroups(groups, !!v)}
                      />
                    )}
                  </TableHead>
                  <TableHead className='w-56'>{t('kg.groupNote')}</TableHead>
                  <TableHead>{t('kg.keys')}</TableHead>
                  <TableHead>{t('budgets.expiry')}</TableHead>
                  {periods.map((p) => (
                    <TableHead key={p} className='text-center'>
                      {t('budgets.periods.' + p)}
                    </TableHead>
                  ))}
                  <TableHead className='text-right'>{t('kg.actions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {query.isPending && (
                  <TableRow>
                    <TableCell colSpan={10} className='py-12 text-center'>
                      {t('kg.loading')}
                    </TableCell>
                  </TableRow>
                )}
                {groups.map((g) => {
                  const state = !g.enabled
                    ? 'disabled'
                    : g.budget.expired
                      ? 'expired'
                      : g.budget.windows.some((w) => Number(w.limit) > 0 && Number(w.available) <= 0)
                        ? 'exhausted'
                        : 'enabled';
                  return (
                    <Fragment key={g.id}>
                      <TableRow data-testid='key-group-row' className={expanded.has(g.id) ? 'bg-muted/20' : undefined}>
                        <TableCell>
                          {mode === 'group' ? (
                            <Checkbox
                              aria-label={g.name}
                              checked={selectedGroups.has(g.id)}
                              onCheckedChange={(v) => selectGroups([g], !!v)}
                            />
                          ) : (
                            <Button
                              size='icon'
                              variant='ghost'
                              aria-label={t(expanded.has(g.id) ? 'kg.collapseGroup' : 'kg.expandGroup', { name: g.name })}
                              onClick={() => toggle(g.id)}
                            >
                              {expanded.has(g.id) ? <ChevronDown className='size-4' /> : <ChevronRight className='size-4' />}
                            </Button>
                          )}
                        </TableCell>
                        <TableCell>
                          <div className='flex max-w-60 flex-col items-start'>
                            <GroupQuickEdit onDeferred={deferRecovery} group={g} kind='name' disabled={!writable}>
                              <span className='truncate font-semibold' title={g.name}>
                                {g.name}
                              </span>
                              {state !== 'enabled' && <Badge variant='outline'>{t('kg.' + state)}</Badge>}
                            </GroupQuickEdit>
                            <GroupQuickEdit onDeferred={deferRecovery} group={g} kind='note' disabled={!writable}>
                              <span className='text-muted-foreground truncate text-xs' title={g.note}>
                                {g.note || t('kg.quick.optionalNote')}
                              </span>
                            </GroupQuickEdit>
                          </div>
                        </TableCell>
                        <TableCell>{g.keyCount}</TableCell>
                        <TableCell>
                          <GroupQuickEdit onDeferred={deferRecovery} group={g} kind='expiry' disabled={!writable}>
                            {g.budget.expiresAt ? (
                              <span className='flex flex-col'>
                                <span>
                                  {new Intl.DateTimeFormat(i18n.language, {
                                    timeZone: g.budget.timezone,
                                    year: 'numeric',
                                    month: '2-digit',
                                    day: '2-digit',
                                  }).format(new Date(g.budget.expiresAt))}
                                </span>
                                <span className={g.budget.expired ? 'text-destructive text-xs' : 'text-muted-foreground text-xs'}>
                                  {g.budget.expired
                                    ? t('kg.expired')
                                    : t('kg.quick.remaining', {
                                        count: Math.max(
                                          0,
                                          Math.ceil((Date.parse(g.budget.expiresAt) - Date.parse(g.budget.serverTime)) / 86400000)
                                        ),
                                      })}
                                </span>
                              </span>
                            ) : (
                              t('budgets.neverExpires')
                            )}
                          </GroupQuickEdit>
                        </TableCell>
                        {periods.map((p) => {
                          const w = g.budget.windows.find((w) => w.period === p);
                          return (
                            <TableCell key={p} className='text-center'>
                              <GroupQuickEdit onDeferred={deferRecovery} group={g} kind={p} disabled={!writable}>
                                {w && Number(w.limit) > 0 ? (
                                  <span>
                                    {currency(w.available)}
                                    <span className='text-muted-foreground block'>/ {currency(w.limit)}</span>
                                  </span>
                                ) : (
                                  t('budgets.notConfigured')
                                )}
                              </GroupQuickEdit>
                            </TableCell>
                          );
                        })}
                        <TableCell>
                          <div className='flex items-center justify-end gap-1'>
                            <GroupToggle group={g} disabled={!writable} />
                            <Button size='sm' variant='outline' asChild>
                              <Link to='/project/key-groups/$groupRef/usage' params={{ groupRef: String(g.id) }} title={t('kg.usageHint')}>
                                <BarChart3 className='size-4' />
                                {t('kg.usage')}
                              </Link>
                            </Button>
                            <Button
                              size='sm'
                              variant='outline'
                              disabled={!writable}
                              aria-label={t('kg.addKey') + ' ' + g.name}
                              onClick={() => setCreate({ kind: 'key', ids: [g.id] })}
                            >
                              <Plus className='size-4' />
                            </Button>
                            <DropdownMenu>
                              <DropdownMenuTrigger asChild>
                                <Button size='icon' variant='ghost' aria-label={t('kg.settings') + ' ' + g.name}>
                                  <MoreHorizontal className='size-4' />
                                </Button>
                              </DropdownMenuTrigger>
                              <DropdownMenuContent align='end'>
                                <DropdownMenuGroup>
                                  <DropdownMenuItem onSelect={() => setBudget(g)}>{t('kg.quick.advanced')}</DropdownMenuItem>
                                  <DropdownMenuItem disabled={!writable} onSelect={() => setEdit({ kind: 'group', items: [g] })}>
                                    {t('kg.editSettings')}
                                  </DropdownMenuItem>
                                </DropdownMenuGroup>
                              </DropdownMenuContent>
                            </DropdownMenu>
                          </div>
                        </TableCell>
                      </TableRow>
                      {expanded.has(g.id) && (
                        <TableRow className='hover:bg-transparent'>
                          <TableCell colSpan={10} className='p-0'>
                            {child(g.id)}
                          </TableCell>
                        </TableRow>
                      )}
                    </Fragment>
                  );
                })}
                <TableRow>
                  <TableCell>
                    <Button
                      size='icon'
                      variant='ghost'
                      aria-label={t('kg.expandGroup', { name: t('kg.ungrouped') })}
                      onClick={() => toggle(0)}
                    >
                      {expanded.has(0) ? <ChevronDown className='size-4' /> : <ChevronRight className='size-4' />}
                    </Button>
                  </TableCell>
                  <TableCell>
                    <button onClick={() => toggle(0)} className='font-semibold'>
                      {t('kg.ungrouped')}
                    </button>
                    <p className='text-muted-foreground text-xs'>{t('kg.ungroupedHelp')}</p>
                  </TableCell>
                  <TableCell>{query.data?.ungroupedCount ?? 0}</TableCell>
                  <TableCell colSpan={6} className='text-muted-foreground text-center'>
                    —
                  </TableCell>
                  <TableCell className='text-right'>
                    <Button size='sm' variant='outline' className='mr-1' asChild>
                      <Link to='/project/key-groups/$groupRef/usage' params={{ groupRef: '0' }}>
                        <BarChart3 className='size-4' />
                        {t('kg.usage')}
                      </Link>
                    </Button>
                    <Button
                      size='sm'
                      variant='outline'
                      disabled={!writable}
                      onClick={() => {
                        changeMode('key');
                        setExpanded(new Set([0]));
                      }}
                    >
                      {t('kg.organize')}
                    </Button>
                  </TableCell>
                </TableRow>
                {expanded.has(0) && (
                  <TableRow>
                    <TableCell colSpan={10} className='p-0'>
                      {child(0)}
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </div>
          <div className='flex flex-wrap items-center justify-between gap-3 text-sm'>
            <span className='text-muted-foreground'>
              {t('kg.total', { count: query.data?.total ?? 0 })} · {t('kg.availableLimit', { currency: settings?.currencyCode || 'USD' })}
            </span>
            <div className='flex items-center gap-2'>
              <div className='w-20'>
                <Choice
                  label={t('kg.pageSize')}
                  value={String(limit)}
                  onChange={(v) => {
                    setLimit(Number(v));
                    setOffset(0);
                  }}
                  options={[10, 20, 50].map((n) => ({ value: String(n), label: String(n) }))}
                />
              </div>
              <Button size='sm' variant='outline' disabled={!offset} onClick={() => setOffset((n) => Math.max(0, n - limit))}>
                {t('kg.previous')}
              </Button>
              <span>{Math.floor(offset / limit) + 1}</span>
              <Button
                size='sm'
                variant='outline'
                disabled={offset + limit >= (query.data?.total ?? 0)}
                onClick={() => setOffset((n) => n + limit)}
              >
                {t('kg.next')}
              </Button>
            </div>
          </div>
        </div>
      </Main>
      {create && <CreateDialog kind={create.kind} initialGroups={create.ids} onClose={() => setCreate(null)} />}
      {edit && (
        <ManageDialog
          selection={edit}
          onClose={() => {
            setEdit(null);
            setSelectedGroups(new Map());
            setSelectedKeys(new Map());
          }}
        />
      )}
      {budget && (
        <BudgetDialog
          target={{ kind: 'group', ownerID: budget.id, name: budget.name }}
          onClose={() => {
            setBudget(null);
            api.refresh();
          }}
        />
      )}
      {profile && <ProfileLoader id={profile} onLoaded={() => setProfile(null)} />}
      <ApiKeysDialogs />
      {move && <MoveDialog group={move} onClose={() => setMove(null)} />}
    </>
  );
}
export default function KeyGroups() {
  const project = useSelectedProjectId();
  const { user } = usePermissions();
  return (
    <ApiKeysProvider key={`${user?.id}:${project}`}>
      <GroupPage />
    </ApiKeysProvider>
  );
}
