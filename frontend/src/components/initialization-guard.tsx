import { useEffect } from 'react';
import { useState } from 'react';
import { useRouter } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { useSystemStatus } from '@/features/auth/data/initialization';

interface InitializationGuardProps {
  children: React.ReactNode;
}

export function InitializationGuard({ children }: InitializationGuardProps) {
  const { t } = useTranslation();
  const router = useRouter();
  const { data: systemStatus, isLoading, error, refetch } = useSystemStatus();
  const [isNavigating, setIsNavigating] = useState(false);

  useEffect(() => {
    // Only redirect if we have data and system is not initialized
    if (systemStatus && !systemStatus.isInitialized) {
      // Check if we're not already on the initialization page
      const currentPath = window.location.pathname;
      if (currentPath !== '/initialization') {
        setIsNavigating(true);
        //@ts-ignore
        router.navigate({ to: '/initialization' }).finally(() => {
          setIsNavigating(false);
        });
      }
    }
  }, [systemStatus, router]);

  // Show loading skeleton while checking system status
  if (isLoading) {
    return (
      <div className='flex h-screen items-center justify-center'>
        <div className='space-y-4'>
          <Skeleton className='h-8 w-48' />
          <Skeleton className='h-4 w-32' />
        </div>
      </div>
    );
  }

  // Show error if failed to check system status
  if (error && !systemStatus) {
    return (
      <div className='flex h-screen items-center justify-center'>
        <div className='text-center'>
          <p className='text-muted-foreground'>{t('common.loadError')}</p>
          <Button onClick={() => void refetch()}>{t('common.buttons.retry')}</Button>
        </div>
      </div>
    );
  }

  // If system is not initialized and we're not on initialization page, don't render children
  // But allow navigation to complete naturally
  if ((systemStatus && !systemStatus.isInitialized && window.location.pathname !== '/initialization') || isNavigating) {
    // Don't return null immediately - let the navigation complete
    // The useEffect will handle the redirect
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
