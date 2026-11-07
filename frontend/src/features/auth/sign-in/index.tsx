import { useSearch } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import AuthLayout from '../auth-layout';
import TwoColumnAuth from '../components/two-column-auth';
import AnimatedLineBackground from './components/animated-line-background';
import { UserAuthForm } from './components/user-auth-form';

export default function SignIn() {
  const { t } = useTranslation();
  const { redirect } = useSearch({ from: '/(auth)/sign-in' });

  return (
    <AuthLayout>
      <div data-testid='sign-in-animation-layer'>
        <AnimatedLineBackground key='optimized-layout' />
      </div>
      <TwoColumnAuth
        title={t('auth.signIn.title')}
        description={t('auth.signIn.subtitle')}
        rightFooter={
          <div className='space-y-2 text-xs leading-relaxed text-slate-500 sm:text-sm'>
            <p>{t('auth.signIn.footer.agreement')}</p>
            <a href='/self-usage' className='hover:text-foreground inline-block underline underline-offset-2'>
              {t('auth.signIn.footer.selfUsage')}
            </a>
          </div>
        }
      >
        <UserAuthForm redirect={redirect} />
      </TwoColumnAuth>
    </AuthLayout>
  );
}
