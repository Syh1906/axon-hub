import React, { useRef, useState, useEffect } from 'react';
import useDialogState from '@/hooks/use-dialog-state';
import { Channel } from '../data/schema';
import { readDefaultTags } from '../utils/default-tags';

type ChannelsDialogType =
  | 'add'
  | 'duplicate'
  | 'edit'
  | 'delete'
  | 'settings'
  | 'channelSettings'
  | 'modelMapping'
  | 'overrides'
  | 'proxy'
  | 'status'
  | 'test'
  | 'testHistory'
  | 'bulkImport'
  | 'archive'
  | 'bulkOrdering'
  | 'tagPriorities'
  | 'bulkArchive'
  | 'bulkDisable'
  | 'bulkEnable'
  | 'bulkTest'
  | 'bulkDelete'
  | 'bulkApplyTemplate'
  | 'bulkClearTemplate'
  | 'errorResolved'
  | 'viewModels'
  | 'price'
  | 'upstream'
  | 'transformOptions'
  | 'rateLimit'
  | 'requestSettings'
  | 'availability'
  | 'keyManagement'
  | 'disabledAPIKeys'
  | 'healthGate'
  | 'endpoints';

interface ChannelsContextType {
  open: ChannelsDialogType | null;
  setOpen: (str: ChannelsDialogType | null) => void;
  currentRow: Channel | null;
  setCurrentRow: React.Dispatch<React.SetStateAction<Channel | null>>;
  selectedChannels: Channel[];
  setSelectedChannels: React.Dispatch<React.SetStateAction<Channel[]>>;
  resetRowSelection: () => void;
  setResetRowSelection: (fn: () => void) => void;
  showTypeTabs: boolean;
  setShowTypeTabs: React.Dispatch<React.SetStateAction<boolean>>;
  selectedTag: string[];
  setSelectedTag: React.Dispatch<React.SetStateAction<string[]>>;
}

const ChannelsContext = React.createContext<ChannelsContextType | null>(null);

interface Props {
  children: React.ReactNode;
}

export default function ChannelsProvider({ children }: Props) {
  const [open, setOpen] = useDialogState<ChannelsDialogType>(null);
  const [currentRow, setCurrentRow] = useState<Channel | null>(null);
  const [selectedChannels, setSelectedChannels] = useState<Channel[]>([]);
  const [selectedTag, setSelectedTag] = useState<string[]>(readDefaultTags);
  const [showTypeTabs, setShowTypeTabs] = useState<boolean>(() => {
    try {
      const stored = localStorage.getItem('channels-show-type-tabs');
      if (stored !== null) {
        const parsed = JSON.parse(stored);
        return typeof parsed === 'boolean' ? parsed : true;
      }
    } catch {
      return true;
    }
    return true;
  });

  useEffect(() => {
    try {
      localStorage.setItem('channels-show-type-tabs', JSON.stringify(showTypeTabs));
    } catch {
      // Ignore storage failures; the in-memory preference still works.
    }
  }, [showTypeTabs]);
  const resetRowSelectionRef = useRef<() => void>(() => {});

  return (
    <ChannelsContext.Provider
      value={{
        open,
        setOpen,
        currentRow,
        setCurrentRow,
        selectedChannels,
        selectedTag,
        setSelectedTag,
        setSelectedChannels,
        resetRowSelection: () => resetRowSelectionRef.current(),
        setResetRowSelection: (fn: () => void) => {
          resetRowSelectionRef.current = fn;
        },
        showTypeTabs,
        setShowTypeTabs,
      }}
    >
      {children}
    </ChannelsContext.Provider>
  );
}

// eslint-disable-next-line react-refresh/only-export-components
export const useChannels = () => {
  const channelsContext = React.useContext(ChannelsContext);

  if (!channelsContext) {
    throw new Error('useChannels has to be used within <ChannelsContext>');
  }

  return channelsContext;
};
