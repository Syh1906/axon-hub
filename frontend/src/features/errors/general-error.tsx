import { useNavigate, useRouter } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';

interface GeneralErrorProps extends React.HTMLAttributes<HTMLDivElement> {
  minimal?: boolean;
}

export default function GeneralError({ className, minimal = false }: GeneralErrorProps) {
  const navigate = useNavigate();
  const { history } = useRouter();
  const { t } = useTranslation();
  return (
    <div className={cn('h-svh w-full', className)}>
      <div className='m-auto flex h-full w-full flex-col items-center justify-center gap-2 px-4'>
        <h1 className='text-2xl font-semibold'>{t('common.pageError.title')}</h1>
        <p className='text-muted-foreground max-w-lg text-center'>
          {t('common.pageError.description')}
        </p>
        {!minimal && (
          <div className='mt-6 flex flex-wrap justify-center gap-4'>
            <Button variant='outline' onClick={() => history.go(-1)}>
              {t('common.pageError.back')}
            </Button>
            <Button variant='outline' onClick={() => navigate({ to: '/' })}>{t('common.pageError.home')}</Button>
            <Button onClick={() => window.location.reload()}>{t('common.pageError.reload')}</Button>
          </div>
        )}
      </div>
    </div>
  );
}
