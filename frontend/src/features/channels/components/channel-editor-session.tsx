import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { QueryLoadAlert } from '@/components/query-load-alert';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { useChannelDetails } from '../data/channels';
import type { Channel } from '../data/schema';
import { ChannelsActionDialog } from './channels-action-dialog';

interface ChannelEditorSessionProps {
  channelId: string;
  mode: 'edit' | 'duplicate' | 'viewModels';
  onClose: () => void;
}

export function ChannelEditorSession({ channelId, mode, onClose }: ChannelEditorSessionProps) {
  const { t } = useTranslation();
  const [initialChannel, setInitialChannel] = useState<Channel | null>(null);
  const details = useChannelDetails(channelId, { enabled: !initialChannel });
  if (!initialChannel && details.isFetchedAfterMount && details.isSuccess) {
    // 一次编辑只采用一次完整详情，后续刷新不能覆盖正在填写的表单。
    setInitialChannel(details.data);
  }
  const onOpenChange = (open: boolean) => {
    if (!open) onClose();
  };
  if (initialChannel) {
    return (
      <ChannelsActionDialog
        open
        onOpenChange={onOpenChange}
        currentRow={mode === 'duplicate' ? undefined : initialChannel}
        duplicateFromRow={mode === 'duplicate' ? initialChannel : undefined}
        showModelsPanel={mode === 'viewModels'}
      />
    );
  }
  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(mode === 'duplicate' ? 'channels.dialogs.create.title' : 'channels.dialogs.edit.title')}</DialogTitle>
          <DialogDescription className={details.isError ? 'sr-only' : undefined}>
            {t(details.isError ? 'channels.dialogs.edit.description' : 'common.loading')}
          </DialogDescription>
        </DialogHeader>
        <QueryLoadAlert error={details.error} hasData={false} retrying={details.isFetching} onRetry={() => void details.refetch()} />
      </DialogContent>
    </Dialog>
  );
}
