import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import { groupChannelModels } from '../utils/group-channel-models';

interface ChannelModel {
  requestModel: string;
  actualModel?: string;
  priority?: number;
  rank?: number;
  ordered?: boolean;
}

interface Channel {
  id: string | number;
  name: string;
  type: string;
  status: string;
  orderingWeight?: number;
}

interface ChannelModelsListProps {
  channels: Array<{
    channel: Channel;
    models: ChannelModel[];
    priority?: number;
    preferLatestVersion?: boolean;
    unrankedModelIDs?: string[];
  }>;
  emptyMessage?: string;
}

export function ChannelModelsList({ channels, emptyMessage }: ChannelModelsListProps) {
  const { t } = useTranslation();
  const groupedChannels = useMemo(
    () =>
      groupChannelModels(
        channels.map((conn) => ({
          ...conn,
          models: conn.models.map((model, rank) => ({ ...model, rank, priority: conn.priority, ordered: conn.preferLatestVersion })),
        }))
      ),
    [channels]
  );

  const warnings = useMemo(() => {
    const result = new Map<string | number, Set<string>>();
    for (const connection of channels) {
      if (!connection.unrankedModelIDs?.length) continue;
      const ids = result.get(connection.channel.id) ?? new Set<string>();
      for (const id of connection.unrankedModelIDs) ids.add(id);
      result.set(connection.channel.id, ids);
    }
    return result;
  }, [channels]);

  const getStatusColor = (status: string) => {
    switch (status) {
      case 'enabled':
        return 'bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-950 dark:text-emerald-400 dark:border-emerald-800';
      case 'disabled':
        return 'bg-gray-50 text-gray-600 border-gray-200 dark:bg-gray-900 dark:text-gray-400 dark:border-gray-700';
      case 'archived':
        return 'bg-amber-50 text-amber-700 border-amber-200 dark:bg-amber-950 dark:text-amber-400 dark:border-amber-800';
      default:
        return 'bg-gray-50 text-gray-600 border-gray-200 dark:bg-gray-900 dark:text-gray-400 dark:border-gray-700';
    }
  };

  const getTypeColor = (type: string) => {
    const colors = {
      openai: 'bg-blue-50 text-blue-700 border-blue-200 dark:bg-blue-950 dark:text-blue-400',
      anthropic: 'bg-purple-50 text-purple-700 border-purple-200 dark:bg-purple-950 dark:text-purple-400',
      deepseek: 'bg-indigo-50 text-indigo-700 border-indigo-200 dark:bg-indigo-950 dark:text-indigo-400',
      doubao: 'bg-orange-50 text-orange-700 border-orange-200 dark:bg-orange-950 dark:text-orange-400',
      kimi: 'bg-pink-50 text-pink-700 border-pink-200 dark:bg-pink-950 dark:text-pink-400',
    };
    return colors[type as keyof typeof colors] || 'bg-gray-50 text-gray-700 border-gray-200 dark:bg-gray-900 dark:text-gray-400';
  };

  if (channels.length === 0) {
    return (
      <p className='text-muted-foreground py-8 text-center text-sm'>{emptyMessage || t('models.dialogs.association.noConnections')}</p>
    );
  }

  return (
    <div className='space-y-3'>
      {groupedChannels.map((conn) => (
        <div key={conn.channel.id} className='rounded-lg border p-3'>
          <div className='mb-2 flex items-start justify-between gap-2'>
            <div className='flex flex-wrap items-center gap-1.5'>
              <span className='text-sm font-medium'>{conn.channel.name}</span>
              <Badge variant='outline' className={`h-5 px-1.5 text-[10px] font-normal ${getTypeColor(conn.channel.type)}`}>
                {t(`channels.types.${conn.channel.type}`, conn.channel.type)}
              </Badge>
              <Badge variant='outline' className={`h-5 px-1.5 text-[10px] font-normal ${getStatusColor(conn.channel.status)}`}>
                {t(`channels.status.${conn.channel.status}`)}
              </Badge>
            </div>
          </div>
          {conn.channel.orderingWeight !== undefined && (
            <Badge variant="secondary" className="mb-2 font-normal">
              {t("models.dialogs.association.channelWeight")}: {conn.channel.orderingWeight}
            </Badge>
          )}
          {warnings.has(conn.channel.id) && (
            <p role='status' className='text-muted-foreground mb-2 text-xs'>
              {t('models.dialogs.association.versionOrder.excluded', { ids: [...warnings.get(conn.channel.id)!].join(', ') })}
            </p>
          )}
          <div className='space-y-1'>
            {conn.models.map((model) => (
              <div key={model.requestModel} className='bg-muted rounded px-2 py-1 text-xs'>
                {model.priority !== undefined && (
                  <span className='text-muted-foreground mr-2'>
                    {t('models.dialogs.association.versionOrder.priority')} {model.priority}
                  </span>
                )}
                {model.requestModel}
                {model.actualModel && model.actualModel !== model.requestModel && (
                  <span className='text-muted-foreground'> → {model.actualModel}</span>
                )}
                {model.ordered && (
                  <Badge variant='secondary' className='ml-2 text-[10px]'>
                    {t(`models.dialogs.association.versionOrder.${model.rank === 0 ? 'first' : 'fallback'}`)}
                  </Badge>
                )}
              </div>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}
