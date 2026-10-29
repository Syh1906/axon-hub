'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { IconKey, IconLoader2, IconPlayerPlay, IconTrash, IconUpload } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Textarea } from '@/components/ui/textarea';
import { useChannels } from '../context/channels-context';
import { upstreamKeyRef, useUpdateKeySettings, useKeySessionReuse } from '../data/channel-accounts';
import { useChannelUpstreamStates, useRefreshChannelUpstreams } from '../data/channel-upstream';
import {
  useChannelDetails,
  useChannelDisabledAPIKeys,
  useDeleteDisabledChannelAPIKeys,
  useDisableChannelAPIKey,
  useEnableChannelAPIKey,
  useTestChannelAPIKey,
  useUpdateChannel,
} from '../data/channels';
import { TestAPIKeyResult } from '../data/schema';
import { ChannelCredentialActions } from './channel-credential-actions';
import { ChannelKeyPriorityCell } from './channel-key-priority-cell';
import { useBalanceLabel } from './channel-upstream-cell';

interface ChannelsAPIKeyManagementDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

function maskAPIKey(key: string) {
  if (key.length <= 8) {
    return '****';
  }
  return `${key.slice(0, 4)}****${key.slice(-4)}`;
}

/**
 * 统一密钥管理弹窗：
 * - 始终可从行操作菜单进入（无论 1 个还是多个 key）
 * - 批量导入（多行文本，一行一个）
 * - 单个/批量：测试、复制、禁用/启用、删除
 * - 密钥状态（正常 / 已禁用 / 测试中 / 测试结果）实时动态更新
 */
export function ChannelsAPIKeyManagementDialog({ open, onOpenChange }: ChannelsAPIKeyManagementDialogProps) {
  const { t } = useTranslation();
  const { currentRow, setOpen } = useChannels();
  const [bulkImportText, setBulkImportText] = useState('');
  const [selectedKeys, setSelectedKeys] = useState<Set<string>>(new Set());
  const [testingKey, setTestingKey] = useState<string | null>(null);
  const [testResults, setTestResults] = useState<Record<string, TestAPIKeyResult>>({});
  const [confirmDeleteKey, setConfirmDeleteKey] = useState<string | null>(null);
  const [confirmDeleteSelected, setConfirmDeleteSelected] = useState(false);
  // 本弹窗独立的 key 列表：基于 currentRow 快照初始化，导入/删除后本地更新，
  // 避免依赖不会随 query 刷新而更新的 currentRow 快照。
  const [localKeys, setLocalKeys] = useState<string[]>([]);
  const abortRef = useRef(false);
  const [importAccount, setImportAccount] = useState('none');
  const [fingerprints, setFingerprints] = useState<Record<string, string>>({});
  const [container, setContainer] = useState<HTMLDivElement | null>(null);
  const upstreamQuery = useChannelUpstreamStates(open && currentRow ? [currentRow.id] : []);
  const upstreamState = upstreamQuery.data?.[0];
  const details = useChannelDetails(currentRow?.id, { enabled: open });
  const keySettings = useUpdateKeySettings(currentRow?.id ?? '');
  const keyReuse = useKeySessionReuse(currentRow?.id ?? '');
  const refresh = useRefreshChannelUpstreams();
  const formatBalance = useBalanceLabel();
  const keyStates = useMemo(() => new Map(upstreamState?.keys.map((key) => [key.keyRef, key]) ?? []), [upstreamState]);
  useEffect(() => {
    if (open && details.data) setLocalKeys(details.data.credentials?.apiKeys ?? []);
  }, [open, details.data]);
  useEffect(() => {
    let active = true;
    void Promise.all(localKeys.map(async (key) => [key, await upstreamKeyRef(key)] as const)).then((entries) => {
      if (active) setFingerprints(Object.fromEntries(entries));
    });
    return () => {
      active = false;
    };
  }, [localKeys]);

  const testSingleKey = useTestChannelAPIKey();
  const disableAPIKey = useDisableChannelAPIKey();
  const enableAPIKey = useEnableChannelAPIKey();
  const updateChannel = useUpdateChannel();
  const deleteDisabledAPIKeys = useDeleteDisabledChannelAPIKeys();

  const validKeys = useMemo(() => localKeys.filter((key) => key.trim().length > 0), [localKeys]);
  // 服务端实时禁用的 key 集合（所有 disable/enable mutation 都会 invalidate 此 query）
  const { data: disabledKeys = [], isLoading: disabledLoading } = useChannelDisabledAPIKeys(currentRow?.id || '', {
    enabled: open && !!currentRow?.id,
  });
  const disabledKeySet = useMemo(() => new Set(disabledKeys.map((item) => item.key)), [disabledKeys]);

  // 本弹窗内测试后得到的"已禁用"结果（测试接口会动态反馈真实状态）
  const effectivelyDisabledSet = useMemo(() => {
    const set = new Set(disabledKeySet);
    Object.entries(testResults).forEach(([key, result]) => {
      if (result.disabled) {
        set.add(key);
      }
    });
    return set;
  }, [disabledKeySet, testResults]);

  // 当前可用的 key 数量（未被禁用的），与编辑弹窗的 mustKeepOneEnabled 保护一致
  const enabledKeysCount = useMemo(
    () => validKeys.filter((key) => !effectivelyDisabledSet.has(key)).length,
    [validKeys, effectivelyDisabledSet]
  );
  const isLastKey = validKeys.length <= 1;

  const isPending =
    testingKey !== null ||
    disableAPIKey.isPending ||
    enableAPIKey.isPending ||
    updateChannel.isPending ||
    deleteDisabledAPIKeys.isPending ||
    keySettings.isPending ||
    keyReuse.isPending;

  useEffect(() => {
    if (open) {
      setBulkImportText('');
      setImportAccount('none');
      setLocalKeys(currentRow?.credentials?.apiKeys ?? []);
      setSelectedKeys(new Set());
      setTestingKey(null);
      setTestResults({});
      setConfirmDeleteKey(null);
      setConfirmDeleteSelected(false);
      abortRef.current = false;
    }
    // 仅在弹窗打开或切换渠道时初始化一次 localKeys；
    // 依赖 currentRow.credentials.apiKeys 会导致导入/删除后的本地更新被覆盖。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, currentRow?.id]);

  if (!currentRow) {
    return null;
  }

  const handleClose = () => {
    abortRef.current = true;
    setOpen(null);
    onOpenChange(false);
    setBulkImportText('');
    setSelectedKeys(new Set());
    setTestingKey(null);
    setTestResults({});
    setConfirmDeleteKey(null);
    setConfirmDeleteSelected(false);
  };

  /** 批量导入：多行文本，一行一个 key，去重、去空白，追加到渠道 apiKeys */
  const handleBulkImport = async () => {
    const newKeys = bulkImportText
      .split('\n')
      .map((k) => k.trim())
      .filter((k) => k.length > 0);
    if (newKeys.length === 0) {
      return;
    }

    const existing = new Set(validKeys);
    // 先对输入自身去重（同一 key 粘贴多行时只保留一个）
    const deduped = [...new Set(newKeys)].filter((k) => !existing.has(k));
    if (deduped.length === 0) {
      setBulkImportText('');
      return;
    }

    try {
      await updateChannel.mutateAsync({
        id: currentRow.id,
        input: {
          credentials: {
            apiKeys: [...validKeys, ...deduped],
          },
        },
      });
      // 本地同步更新，避免依赖不会刷新的 currentRow 快照
      setLocalKeys((prev) => [...prev, ...deduped]);
      setBulkImportText('');
      if (importAccount !== 'none') {
        for (const key of deduped)
          await keySettings.mutateAsync({ keyRef: await upstreamKeyRef(key), remark: '', accountRef: importAccount });
      }
      refresh.mutate({ channelIds: [currentRow.id], force: true, includeDisabled: true });
    } catch {
      // handled by hook
    }
  };

  /** 删除 key（活动 key 从 credentials 移除；已禁用 key 走 deleteDisabledAPIKeys） */
  const deleteKeys = async (keysToDelete: string[]) => {
    if (keysToDelete.length === 0) {
      return;
    }

    const disabled = keysToDelete.filter((key) => effectivelyDisabledSet.has(key));
    const active = keysToDelete.filter((key) => !effectivelyDisabledSet.has(key));

    if (disabled.length > 0) {
      await deleteDisabledAPIKeys.mutateAsync({ channelID: currentRow.id, keys: disabled });
    }
    // 注意：remainingKeys 必须同时过滤 active 与 disabled，
    // 避免把上面 deleteDisabledAPIKeys 已从 credentials 移除的 disabled key 写回去。
    const removed = new Set([...active, ...disabled]);
    const remainingKeys = validKeys.filter((key) => !removed.has(key));
    if (active.length > 0) {
      await updateChannel.mutateAsync({
        id: currentRow.id,
        input: { credentials: { apiKeys: remainingKeys } },
      });
    }
    // 本地同步更新（disabled 删除走 deleteDisabledAPIKeys 不触发 updateChannel，
    // 但同样要从弹窗列表里移除）
    setLocalKeys(remainingKeys);
  };

  const handleDeleteSingle = async (key: string) => {
    try {
      await deleteKeys([key]);
      setConfirmDeleteKey(null);
    } catch {
      // handled by hooks
    }
  };

  const handleDeleteSelected = async () => {
    if (selectedKeys.size === 0 || selectedKeys.size >= validKeys.length) {
      return;
    }
    try {
      await deleteKeys(Array.from(selectedKeys));
      setConfirmDeleteSelected(false);
      setSelectedKeys(new Set());
    } catch {
      // handled by hooks
    }
  };

  const handleTestSingle = async (key: string) => {
    if (testingKey) {
      return;
    }
    setTestingKey(key);
    try {
      const result = await testSingleKey.mutateAsync({
        channelID: currentRow.id,
        key,
        modelID: currentRow.defaultTestModel || undefined,
      });
      if (!abortRef.current) {
        setTestResults((prev) => ({ ...prev, [key]: result }));
      }
    } catch {
      if (!abortRef.current) {
        setTestResults((prev) => ({
          ...prev,
          [key]: {
            keyPrefix: maskAPIKey(key),
            success: false,
            latency: 0,
            error: t('channels.dialogs.testAPIKeys.requestFailed'),
            disabled: false,
          },
        }));
      }
    } finally {
      if (!abortRef.current) {
        setTestingKey(null);
      }
    }
  };

  const handleTestSelected = async () => {
    const keysToTest = validKeys.filter((key) => selectedKeys.has(key));
    if (keysToTest.length === 0 || testingKey) {
      return;
    }

    abortRef.current = false;
    for (const key of keysToTest) {
      if (abortRef.current) {
        break;
      }
      setTestingKey(key);
      try {
        const result = await testSingleKey.mutateAsync({
          channelID: currentRow.id,
          key,
          modelID: currentRow.defaultTestModel || undefined,
        });
        if (!abortRef.current) {
          setTestResults((prev) => ({ ...prev, [key]: result }));
        }
      } catch {
        if (!abortRef.current) {
          setTestResults((prev) => ({
            ...prev,
            [key]: {
              keyPrefix: maskAPIKey(key),
              success: false,
              latency: 0,
              error: t('channels.dialogs.testAPIKeys.requestFailed'),
              disabled: false,
            },
          }));
        }
      }
      if (!abortRef.current) {
        setTestingKey(null);
      }
    }
    setTestingKey(null);
  };

  const handleToggleDisable = async (key: string) => {
    try {
      await disableAPIKey.mutateAsync({ channelID: currentRow.id, key });
    } catch {
      // handled by hooks
    }
  };

  const handleToggleEnable = async (key: string) => {
    try {
      await enableAPIKey.mutateAsync({ channelID: currentRow.id, key });
      setTestResults((previous) => {
        const next = { ...previous };
        delete next[key];
        return next;
      });
    } catch {
      // handled by hooks
    }
  };

  const handleSelectAll = () => {
    if (selectedKeys.size === validKeys.length) {
      setSelectedKeys(new Set());
    } else {
      setSelectedKeys(new Set(validKeys));
    }
  };

  const isAllSelected = validKeys.length > 0 && selectedKeys.size === validKeys.length;
  const isSomeSelected = selectedKeys.size > 0 && selectedKeys.size < validKeys.length;

  const getKeyResult = (key: string): TestAPIKeyResult | undefined => testResults[key];
  const isKeyDisabled = (key: string) => effectivelyDisabledSet.has(key);

  const saveKey = (keyRef: string, remark: string, accountRef: string) =>
    keySettings.mutate(
      { keyRef, remark, accountRef },
      { onSuccess: () => refresh.mutate({ channelIds: [currentRow.id], keyRef, force: true, includeDisabled: true }) }
    );
  return (
    <Dialog
      open={open}
      onOpenChange={(value) => {
        if (!value) handleClose();
      }}
    >
      <DialogContent ref={setContainer} className='flex max-h-[90vh] flex-col overflow-hidden sm:max-w-4xl'>
        <DialogHeader>
          <DialogTitle className='flex items-center gap-2'>
            <IconKey className='size-5' />
            {t('channels.dialogs.keyManagement.title')}
          </DialogTitle>
          <DialogDescription>{currentRow.name}</DialogDescription>
        </DialogHeader>
        <div className='flex min-h-0 flex-1 flex-col gap-4'>
          <div className='flex flex-wrap items-center gap-3 rounded-md border p-3'>
            <Label htmlFor='reuse-channel-key'>{t('channels.accounts.reuseKey')}</Label>
            <Switch
              id='reuse-channel-key'
              checked={upstreamState?.keySessionReuse ?? true}
              disabled={isPending || !upstreamState}
              onCheckedChange={(enabled) => keyReuse.mutate(enabled)}
            />
            <span className='text-muted-foreground text-xs'>{t('channels.accounts.reuseHint')}</span>
          </div>
          <div className='bg-muted/40 flex flex-col gap-2 rounded-md border p-3'>
            <div className='flex items-center justify-between text-sm'>
              <span className='font-medium'>{t('channels.dialogs.keyManagement.importTitle')}</span>
              <span className='text-muted-foreground text-xs'>{t('channels.dialogs.keyManagement.importHint')}</span>
            </div>
            <div className='flex flex-wrap items-start gap-2'>
              <Textarea
                value={bulkImportText}
                onChange={(event) => setBulkImportText(event.target.value)}
                placeholder={t('channels.dialogs.keyManagement.importPlaceholder')}
                className='min-h-16 min-w-40 flex-1 font-mono text-xs'
              />
              <div className='flex flex-col gap-1'>
                <Label className='text-xs'>{t('channels.accounts.owner')}</Label>
                <Select value={importAccount} onValueChange={setImportAccount}>
                  <SelectTrigger className='w-40'>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent container={container}>
                    <SelectGroup>
                      <SelectItem value='none'>{t('channels.accounts.unassigned')}</SelectItem>
                      {upstreamState?.accounts.map((account) => (
                        <SelectItem key={account.ref} value={account.ref}>
                          {account.name}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </div>
              <Button disabled={isPending || !bulkImportText.trim()} onClick={() => void handleBulkImport()}>
                <IconUpload />
                {t('channels.dialogs.keyManagement.importButton')}
              </Button>
            </div>
          </div>
          <div className='flex min-h-0 flex-1 flex-col overflow-hidden rounded-lg border'>
            <div className='bg-muted/40 flex items-center justify-between gap-2 border-b px-3 py-2'>
              <div className='flex items-center gap-2'>
                <Checkbox
                  checked={isAllSelected || (isSomeSelected && 'indeterminate')}
                  onCheckedChange={handleSelectAll}
                  aria-label={t('common.columns.selectAll')}
                />
                <span className='text-muted-foreground text-sm'>
                  {t('channels.accounts.keySummary', { total: validKeys.length, enabled: enabledKeysCount })}
                </span>
              </div>
              <div className='flex gap-2'>
                <Button variant='outline' size='sm' disabled={isPending || !selectedKeys.size} onClick={() => void handleTestSelected()}>
                  <IconPlayerPlay />
                  {t('channels.dialogs.keyManagement.testSelected', { count: selectedKeys.size })}
                </Button>
                <Popover open={confirmDeleteSelected} onOpenChange={setConfirmDeleteSelected}>
                  <PopoverTrigger asChild>
                    <Button variant='outline' size='sm' disabled={isPending || !selectedKeys.size || selectedKeys.size >= validKeys.length}>
                      <IconTrash />
                      {t('channels.dialogs.keyManagement.deleteSelected', { count: selectedKeys.size })}
                    </Button>
                  </PopoverTrigger>
                  <PopoverContent container={container}>
                    <p className='mb-3 text-sm'>
                      {t('channels.dialogs.keyManagement.confirmDeleteSelected', { count: selectedKeys.size })}
                    </p>
                    <Button size='sm' variant='destructive' disabled={isPending} onClick={() => void handleDeleteSelected()}>
                      {t('common.buttons.confirm')}
                    </Button>
                  </PopoverContent>
                </Popover>
              </div>
            </div>
            <ScrollArea className='min-h-0 flex-1'>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className='w-10' />
                    <TableHead>{t('channels.accounts.remarkKey')}</TableHead>
                    <TableHead className='w-36'>{t('channels.accounts.owner')}</TableHead>
                    <TableHead className='w-20'>{t('channels.accounts.priority')}</TableHead>
                    <TableHead className='w-24'>{t('channels.accounts.keyQuota')}</TableHead>
                    <TableHead className='w-20'>{t('channels.accounts.enabled')}</TableHead>
                    <TableHead className='w-24'>{t('channels.dialogs.keyManagement.actionsColumn')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {disabledLoading && !validKeys.length ? (
                    <TableRow>
                      <TableCell colSpan={7}>
                        <IconLoader2 className='mx-auto size-5 animate-spin' />
                      </TableCell>
                    </TableRow>
                  ) : (
                    validKeys.map((key) => {
                      const ref = fingerprints[key];
                      const item = keyStates.get(ref);
                      const disabled = isKeyDisabled(key);
                      const result = getKeyResult(key);
                      const quota = item?.snapshot.balances.find((balance) => balance.kind === 'KEY');
                      return (
                        <TableRow key={key}>
                          <TableCell>
                            <Checkbox
                              checked={selectedKeys.has(key)}
                              onCheckedChange={(checked) =>
                                setSelectedKeys((previous) => {
                                  const next = new Set(previous);
                                  if (checked) next.add(key);
                                  else next.delete(key);
                                  return next;
                                })
                              }
                            />
                          </TableCell>
                          <TableCell className='max-w-80 min-w-48'>
                            <Input
                              key={`${ref}:${item?.remark}`}
                              className='h-7 text-xs'
                              defaultValue={item?.remark ?? ''}
                              placeholder={t('channels.accounts.remark')}
                              aria-label={t('channels.accounts.remark')}
                              disabled={!item || isPending}
                              onBlur={(event) => {
                                if (item && event.target.value.trim() !== item.remark)
                                  saveKey(ref, event.target.value.trim(), item.accountRef);
                              }}
                              onKeyDown={(event) => {
                                if (event.key === 'Enter') event.currentTarget.blur();
                                if (event.key === 'Escape') {
                                  event.currentTarget.value = item?.remark ?? '';
                                  event.currentTarget.blur();
                                }
                              }}
                            />
                            {ref ? (
                              <ChannelCredentialActions channelId={currentRow.id} keyRef={ref} label={item?.label ?? maskAPIKey(key)} />
                            ) : (
                              <code>{maskAPIKey(key)}</code>
                            )}
                            {testingKey === key && <IconLoader2 className='size-3 animate-spin' />}
                            {result && (
                              <p className='text-muted-foreground text-xs'>
                                {t(result.success ? 'channels.dialogs.keyManagement.success' : 'channels.dialogs.keyManagement.failed')}
                                {result.latency > 0 && ` · ${result.latency.toFixed(2)}s`}
                              </p>
                            )}
                            {result?.error && <p className='text-destructive max-w-64 text-xs break-all'>{result.error}</p>}
                          </TableCell>
                          <TableCell>
                            <Select
                              value={item?.accountRef || 'none'}
                              disabled={!item || isPending}
                              onValueChange={(value) => saveKey(ref, item?.remark ?? '', value === 'none' ? '' : value)}
                            >
                              <SelectTrigger className='h-8 w-32 text-xs'>
                                <SelectValue />
                              </SelectTrigger>
                              <SelectContent container={container}>
                                <SelectGroup>
                                  <SelectItem value='none'>{t('channels.accounts.unassigned')}</SelectItem>
                                  {upstreamState?.accounts.map((account) => (
                                    <SelectItem key={account.ref} value={account.ref}>
                                      {account.name}
                                    </SelectItem>
                                  ))}
                                </SelectGroup>
                              </SelectContent>
                            </Select>
                          </TableCell>
                          <TableCell>
                            {item && (
                              <ChannelKeyPriorityCell channelId={currentRow.id} item={item} container={container} disabled={isPending} />
                            )}
                          </TableCell>
                          <TableCell className='text-xs tabular-nums'>
                            {quota ? formatBalance(quota) : t('channels.upstream.notProvided')}
                          </TableCell>
                          <TableCell>
                            <Switch
                              checked={!disabled}
                              aria-label={t('channels.accounts.enabled')}
                              disabled={isPending || (!disabled && enabledKeysCount <= 1)}
                              onCheckedChange={(enabled) => {
                                if (enabled) void handleToggleEnable(key);
                                else void handleToggleDisable(key);
                              }}
                            />
                          </TableCell>
                          <TableCell>
                            <div className='flex gap-1'>
                              <Button
                                variant='ghost'
                                size='icon'
                                className='size-7'
                                aria-label={t('channels.dialogs.keyManagement.test')}
                                disabled={isPending}
                                onClick={() => void handleTestSingle(key)}
                              >
                                <IconPlayerPlay />
                              </Button>
                              <Popover open={confirmDeleteKey === key} onOpenChange={(open) => setConfirmDeleteKey(open ? key : null)}>
                                <PopoverTrigger asChild>
                                  <Button
                                    variant='ghost'
                                    size='icon'
                                    className='text-destructive size-7'
                                    aria-label={t('channels.dialogs.keyManagement.deleteSelected', { count: 1 })}
                                    disabled={isPending || isLastKey || (!disabled && enabledKeysCount <= 1)}
                                  >
                                    <IconTrash />
                                  </Button>
                                </PopoverTrigger>
                                <PopoverContent container={container}>
                                  <p className='mb-3 text-sm'>{t('channels.dialogs.keyManagement.confirmDelete')}</p>
                                  <Button variant='destructive' size='sm' disabled={isPending} onClick={() => void handleDeleteSingle(key)}>
                                    {t('common.buttons.confirm')}
                                  </Button>
                                </PopoverContent>
                              </Popover>
                            </div>
                          </TableCell>
                        </TableRow>
                      );
                    })
                  )}
                </TableBody>
              </Table>
            </ScrollArea>
          </div>
          <p className='text-muted-foreground text-xs'>
            {t('channels.accounts.callingUnaffected')}{' '}
            <Button variant='link' size='sm' className='h-auto p-0' onClick={() => setOpen('upstream')}>
              {t('channels.accounts.tab')}
            </Button>
          </p>
        </div>
        <DialogFooter className='items-center sm:justify-between'>
          <p className='text-muted-foreground text-xs'>{t('channels.accounts.managementHint')}</p>
          <Button variant='outline' onClick={handleClose}>
            {t('common.buttons.close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
