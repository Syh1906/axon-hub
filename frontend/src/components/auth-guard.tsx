import { useEffect } from 'react';
import { useRouter } from '@tanstack/react-router';
import { isAuthError } from '@/gql/graphql';
import { useTranslation } from 'react-i18next';
import { useAuthStore } from '@/stores/authStore';
import { useSelectedProjectId } from '@/stores/projectStore';
import { loadErrorKey, loadErrorRequestId } from '@/lib/load-error';
import { isProjectSelectionValid } from '@/lib/project-membership';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { useMe } from '@/features/auth/data/auth';

interface AuthGuardProps {
  children: React.ReactNode;
}

export function AuthGuard({ children }: AuthGuardProps) {
  const { t } = useTranslation();
  const router = useRouter();
  const { accessToken } = useAuthStore((state) => state.auth);
  const selectedProjectId = useSelectedProjectId();

  // Automatically fetch user info when token is available
  const { isLoading: isMeLoading, error: meError, data: me, refetch, isFetching } = useMe();
  const projectReady = isProjectSelectionValid(me, selectedProjectId);

  useEffect(() => {
    // If no token, redirect to sign-in
    if (!accessToken) {
      const currentPath = window.location.pathname;
      // Don't redirect if already on auth pages
      if (
        !currentPath.startsWith('/sign-in') &&
        !currentPath.startsWith('/sign-up') &&
        !currentPath.startsWith('/initialization') &&
        !currentPath.startsWith('/forgot-password') &&
        !currentPath.startsWith('/otp')
      ) {
        router.navigate({ to: '/sign-in' });
      }
    }
  }, [accessToken, router]);

  // Handle me query error (e.g., token expired)
  useEffect(() => {
    if (meError && accessToken && isAuthError(meError)) {
      // Token might be expired, redirect to sign-in
      router.navigate({ to: '/sign-in' });
    }
  }, [meError, accessToken, router]);

  // Show loading while checking auth
  if (!accessToken) {
    const currentPath = window.location.pathname;
    // Don't show loading on auth pages
    if (
      currentPath.startsWith('/sign-in') ||
      currentPath.startsWith('/sign-up') ||
      currentPath.startsWith('/initialization') ||
      currentPath.startsWith('/forgot-password') ||
      currentPath.startsWith('/otp')
    ) {
      return <>{children}</>;
    }

    return (
      <div className='flex h-screen items-center justify-center'>
        <div className='space-y-4'>
          <Skeleton className='h-8 w-48' />
          <Skeleton className='h-4 w-32' />
        </div>
      </div>
    );
  }

  if (meError && !me) {
    return (
      <div className='flex h-screen flex-col items-center justify-center gap-4' role='alert'>
        <p>{t('common.loadFailure.account')}</p>
        <p className='text-muted-foreground max-w-md px-4 text-center text-sm'>{t(loadErrorKey(meError))}</p>
        {loadErrorRequestId(meError) && (
          <p className='text-muted-foreground text-xs'>{t('common.loadFailure.requestId', { id: loadErrorRequestId(meError) })}</p>
        )}
        <Button disabled={isFetching} onClick={() => void refetch()}>
          {t(isFetching ? 'common.loadFailure.retrying' : 'common.buttons.retry')}
        </Button>
      </div>
    );
  }

  // Show loading while fetching user info or validating the persisted project
  if (accessToken && (isMeLoading || !projectReady)) {
    return (
      <div className='flex h-screen items-center justify-center'>
        <div className='space-y-4'>
          <Skeleton className='h-8 w-48' />
          <Skeleton className='h-4 w-32' />
        </div>
      </div>
    );
  }

  return <>{children}</>;
}
