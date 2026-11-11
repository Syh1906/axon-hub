import { useEffect, useCallback } from 'react';
import { useQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { toast } from 'sonner';
import { useAuthStore } from '@/stores/authStore';
import i18n from '@/lib/i18n';
import { CHECK_FOR_UPDATE_QUERY, type VersionCheck } from '@/features/system/data/system';

const VERSION_CHECK_STORAGE_KEY = 'axonhub_dismissed_version';
const VERSION_CHECK_TIMESTAMP_KEY = 'axonhub_version_check_timestamp';
const VERSION_CHECK_INTERVAL = 10 * 60 * 1000; // 10 minutes in milliseconds

export function versionCheckDue(lastAttempt: string | null, now = Date.now()) {
  return !lastAttempt || now - Number(lastAttempt) >= VERSION_CHECK_INTERVAL;
}

/**
 * Hook to check for new versions and show toast notification.
 * Only shows to owners and only once per version.
 */
export function useVersionCheck() {
  const user = useAuthStore((state) => state.auth.user);
  const isOwner = user?.isOwner ?? false;

  const shouldCheckVersion = useCallback(() => {
    if (!isOwner) return false;

    const lastCheckTime = localStorage.getItem(VERSION_CHECK_TIMESTAMP_KEY);
    return versionCheckDue(lastCheckTime);
  }, [isOwner]);

  const { data: updateCheck } = useQuery({
    queryKey: ['checkForUpdate', false],
    queryFn: async ({ signal }) => {
      // 失败也记本次尝试，避免 GitHub 不可达时切页反复占用连接。
      // Query v5 不再调用查询的 onSuccess，必须在实际执行位置记录。
      localStorage.setItem(VERSION_CHECK_TIMESTAMP_KEY, Date.now().toString());
      const data = await graphqlRequest<{ checkForUpdate: VersionCheck }>(CHECK_FOR_UPDATE_QUERY, { includeBeta: false }, undefined, { signal });
      return data.checkForUpdate;
    },
    enabled: shouldCheckVersion(),
    retry: false,
    staleTime: VERSION_CHECK_INTERVAL,
    refetchOnWindowFocus: false,
    refetchOnMount: false,
    refetchOnReconnect: false,
  });

  const showUpdateToast = useCallback((latestVersion: string, releaseUrl: string) => {
    toast.info(i18n.t('system.about.updateCheck.newVersionAvailable'), {
      description: `${i18n.t('system.about.updateCheck.latestVersion')}: ${latestVersion}`,
      duration: 10000,
      action: {
        label: i18n.t('system.about.updateCheck.viewRelease'),
        onClick: () => {
          window.open(releaseUrl, '_blank', 'noopener,noreferrer');
        },
      },
    });
  }, []);

  useEffect(() => {
    if (!isOwner || !updateCheck) return;

    if (!updateCheck.hasUpdate) return;

    // Check if this version was already dismissed
    const dismissedVersion = localStorage.getItem(VERSION_CHECK_STORAGE_KEY);
    if (dismissedVersion === updateCheck.latestVersion) return;

    // Show toast and mark as shown
    showUpdateToast(updateCheck.latestVersion, updateCheck.releaseUrl);
    localStorage.setItem(VERSION_CHECK_STORAGE_KEY, updateCheck.latestVersion);
  }, [isOwner, updateCheck, showUpdateToast]);
}
