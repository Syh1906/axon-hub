import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { extractNumberID } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';
import type { Member, ManagementRow } from './data';
import { GroupChoice, TemplateChoice } from './editors';
import { useQuickSave, type QuickEditTarget } from './quick-edit';

export function keyQuickTarget(member: Member): QuickEditTarget {
  const id = Number(extractNumberID(member.id));
  return {
    kind: 'key',
    name: member.name,
    groupRef: member.groupRef ?? 0,
    row: { action: 'updateKey', targetID: id, rowID: String(id), updatedAt: member.updatedAt },
  };
}

export function KeySettingsDialog({ member, onClose, onDeferred }: { member: Member; onClose: () => void; onDeferred: () => void }) {
  const { t } = useTranslation();
  const [snapshot] = useState(member);
  const [name, setName] = useState(snapshot.name);
  const [enabled, setEnabled] = useState(snapshot.status === 'enabled');
  const [groupRef, setGroupRef] = useState(snapshot.groupRef ?? 0);
  const [template, setTemplate] = useState<number>();
  const [ips, setIPs] = useState(snapshot.allowedIps.join('\n'));
  const flow = useQuickSave(keyQuickTarget(snapshot), onClose);
  const locked = flow.busy || flow.uncertain || flow.readError;
  const allowedIPs = ips.split(/[\s,]+/).filter(Boolean);
  const patch: Partial<ManagementRow> = {
    ...(name.trim() !== snapshot.name && { name: name.trim() }),
    ...(enabled !== (snapshot.status === 'enabled') && { enabled }),
    ...(groupRef !== (snapshot.groupRef ?? 0) && { groupRef }),
    ...(template !== undefined && { templateID: template }),
    ...(JSON.stringify(allowedIPs) !== JSON.stringify(snapshot.allowedIps) && { setAllowedIPs: true, allowedIPs }),
  };
  const close = () => {
    if (flow.uncertain) onDeferred();
    onClose();
  };
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !flow.busy) close();
      }}
    >
      <DialogContent className='flex max-h-[85dvh] flex-col sm:max-w-xl' showCloseButton={!flow.busy}>
        <DialogHeader>
          <DialogTitle>{t('kg.quick.keySettings')}</DialogTitle>
          <DialogDescription>{t('kg.quick.keySettingsHelp')}</DialogDescription>
        </DialogHeader>
        <form
          className='flex min-h-0 flex-col gap-4'
          onSubmit={(event) => {
            event.preventDefault();
            if (name.trim() && Object.keys(patch).length) void flow.save(patch);
          }}
        >
          <fieldset disabled={locked} className='flex min-h-0 flex-col gap-4 overflow-y-auto'>
            <div className='flex flex-col gap-2'>
              <Label htmlFor='single-key-name'>{t('kg.keyName')}</Label>
              <Input id='single-key-name' value={name} onChange={(event) => setName(event.target.value)} maxLength={128} />
            </div>
            <div className='flex items-center justify-between'>
              <Label htmlFor='single-key-enabled'>{t('kg.status')}</Label>
              <Switch id='single-key-enabled' checked={enabled} onCheckedChange={setEnabled} disabled={snapshot.status === 'archived'} />
            </div>
            <div className='flex flex-col gap-2'>
              <Label>{t('kg.group')}</Label>
              <GroupChoice value={groupRef} onChange={setGroupRef} />
            </div>
            <div className='flex flex-col gap-2'>
              <Label>{t('kg.template')}</Label>
              <TemplateChoice value={template} onChange={setTemplate} />
              <p className='text-muted-foreground text-xs'>{t('kg.quick.templateHelp')}</p>
            </div>
            <div className='flex flex-col gap-2'>
              <Label htmlFor='single-key-ips'>{t('kg.ips')}</Label>
              <Textarea id='single-key-ips' value={ips} onChange={(event) => setIPs(event.target.value)} rows={3} />
              <p className='text-muted-foreground text-xs'>{t('kg.quick.ipsHelp')}</p>
            </div>
          </fieldset>
          {flow.message && (
            <p role='alert' className='text-destructive text-sm'>
              {flow.message}
            </p>
          )}
          {flow.readError && (
            <p role='alert' className='text-destructive text-sm'>
              {t('kg.quick.readFailed')}
            </p>
          )}
          <DialogFooter className='border-t pt-4'>
            <Button type='button' variant='outline' disabled={flow.busy} onClick={close}>
              {t(flow.uncertain ? 'kg.deferRecovery' : 'common.buttons.cancel')}
            </Button>
            {flow.readError ? (
              <Button type='button' disabled={flow.busy} onClick={() => void flow.retryRead()}>
                {t('kg.reload')}
              </Button>
            ) : flow.uncertain ? (
              <Button type='button' disabled={flow.busy} onClick={() => void flow.execute(true)}>
                {t('kg.recover')}
              </Button>
            ) : (
              <Button type='submit' disabled={flow.busy || !name.trim() || !Object.keys(patch).length}>
                {t(flow.busy ? 'kg.working' : 'common.buttons.save')}
              </Button>
            )}
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
