import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { usePermissions } from '@/hooks/usePermissions';
import type { TriggerGcCleanupInput } from './system';

export interface MaintenanceSettings {
  batchSize: number;
  batchIntervalMs: number;
  batchTimeoutSeconds: number;
  maxRunSeconds: number;
  busyConnectionsPercent: number;
  diskPath: string;
  diskWarningPercent: number;
  diskCriticalPercent: number;
  reserveDiskGiB: number;
}

export interface CleanupTask {
  id: string;
  status: 'queued' | 'running' | 'waiting' | 'paused' | 'completed' | 'failed';
  automatic: boolean;
  createdAt: string;
  updatedAt: string;
  finishedAt: string | null;
  pauseRequested: boolean;
  currentStage: number;
  stages: {
    resourceType: string;
    retentionDays: number;
    cutoffTime: string;
    estimatedCount: number | null;
    scannedCount: number;
    processedCount: number;
    completed: boolean;
  }[];
  error: string;
  waitReason: string;
}

export interface StorageOverview {
  dialect: string;
  databaseBytes: number | null;
  tables: { name: string; bytes: number; estimatedRows: number }[];
  diskTotalBytes: number | null;
  diskFreeBytes: number | null;
  diskSource: string;
  reserveBytes: number | null;
  level: string;
  sampledAt: string;
  databaseAvailable: boolean;
}

const TASK_FIELDS = `id status automatic createdAt updatedAt finishedAt pauseRequested currentStage error waitReason
  stages { resourceType retentionDays cutoffTime estimatedCount scannedCount processedCount completed }`;

export function cleanupIsActive(task?: CleanupTask | null) {
  return !!task && ['queued', 'running', 'waiting', 'paused'].includes(task.status);
}

export function useCleanupTask() {
  const { hasSystemScope } = usePermissions();
  return useQuery({
    queryKey: ['cleanupTask'],
    enabled: hasSystemScope('read_settings'),
    queryFn: async ({ signal }) =>
      (
        await graphqlRequest<{ cleanupTask: CleanupTask | null }>(
          `query CleanupTaskStatus { cleanupTask { ${TASK_FIELDS} } }`,
          undefined,
          undefined,
          { signal }
        )
      ).cleanupTask,
    refetchInterval: (query) => (cleanupIsActive(query.state.data) ? 1500 : 15000),
    refetchIntervalInBackground: false,
    staleTime: 1000,
    retry: 1,
  });
}

export function useStorageOverview() {
  const { hasSystemScope } = usePermissions();
  return useQuery({
    queryKey: ['storageOverview'],
    enabled: hasSystemScope('read_settings'),
    queryFn: async ({ signal }) =>
      (
        await graphqlRequest<{ storageOverview: StorageOverview }>(
          `query StorageOverview {
      storageOverview { dialect databaseBytes tables { name bytes estimatedRows } diskTotalBytes diskFreeBytes diskSource reserveBytes level sampledAt databaseAvailable }
    }`,
          undefined,
          undefined,
          { signal }
        )
      ).storageOverview,
    staleTime: 30000,
    refetchInterval: 60000,
    refetchIntervalInBackground: false,
    retry: 1,
  });
}

export function useStartCleanupTask() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async (input: TriggerGcCleanupInput) =>
      (
        await graphqlRequest<{ startCleanupTask: CleanupTask }>(
          `mutation StartCleanupTask($input: TriggerGcCleanupInput!) { startCleanupTask(input: $input) { ${TASK_FIELDS} } }`,
          { input }
        )
      ).startCleanupTask,
    onSuccess: (task) => {
      client.setQueryData(['cleanupTask'], task);
    },
    onError: () => {
      void client.invalidateQueries({ queryKey: ['cleanupTask'] });
    },
    retry: false,
  });
}

export function usePauseCleanupTask() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async (input: { id: string; paused: boolean }) =>
      (
        await graphqlRequest<{ setCleanupTaskPaused: CleanupTask }>(
          `mutation PauseCleanupTask($id: String!, $paused: Boolean!) { setCleanupTaskPaused(id: $id, paused: $paused) { ${TASK_FIELDS} } }`,
          input
        )
      ).setCleanupTaskPaused,
    onSuccess: (task) => {
      client.setQueryData(['cleanupTask'], task);
    },
    onSettled: () => {
      void client.invalidateQueries({ queryKey: ['cleanupTask'] });
    },
    retry: false,
  });
}

export const CLEANUP_RESOURCES = [
  { type: 'request_bodies', field: 'requestBodiesCleanupDays', days: 7 },
  { type: 'response_bodies', field: 'responseBodiesCleanupDays', days: 7 },
  { type: 'response_chunks', field: 'responseChunksCleanupDays', days: 3 },
  { type: 'requests', field: 'requestsCleanupDays', days: 60 },
  { type: 'usage_logs', field: 'usageLogsCleanupDays', days: 60 },
  { type: 'cache_windows', field: 'cacheWindowsCleanupDays', days: 60 },
  { type: 'usage_archive', field: 'usageArchiveCleanupDays', days: 365 },
  { type: 'channel_probes', field: 'channelProbesCleanupDays', days: 3 },
] as const;
