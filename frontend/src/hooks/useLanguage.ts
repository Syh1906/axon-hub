import { useEffect } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { UPDATE_ME_MUTATION } from '@/gql/users';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { useAuthStore, type AuthUser } from '@/stores/authStore';
import { changeBrowserLanguagePreference, restoreBrowserLanguagePreference, useLanguageStore } from '@/stores/languageStore';
import type { LanguagePreference } from '@/lib/language-preference';
import { syncCurrentAccountProfile } from '@/lib/account-profile';

export function useLanguage({ browserOnly = false }: { browserOnly?: boolean } = {}) {
  const { i18n, t } = useTranslation();
  const auth = useAuthStore((state) => state.auth);
  const queryClient = useQueryClient();
  const preference = useLanguageStore((state) => state.preference);

  useEffect(() => {
    if (browserOnly || !auth.accessToken) void restoreBrowserLanguagePreference();
  }, [browserOnly, auth.accessToken]);

  // Mutation for updating user language preference
  const updateLanguageMutation = useMutation({
    mutationFn: async (language: LanguagePreference) => {
      if (!auth.user) {
        throw new Error('User not authenticated');
      }

      await queryClient.cancelQueries({ queryKey: ['me', auth.accessToken] });
      const response = await graphqlRequest<{ updateMe: Pick<AuthUser, 'preferLanguage'> }>(UPDATE_ME_MUTATION, {
        input: {
          preferLanguage: language,
        },
      });
      return response.updateMe;
    },
    onSuccess: async (updatedUser) => {
      if (!await syncCurrentAccountProfile(queryClient, auth.user?.id, updatedUser)) return;

      const languageName = updatedUser.preferLanguage === 'auto' ? t('language.auto') : updatedUser.preferLanguage === 'en' ? 'English' : '中文';
      toast.success(t('language.changeSuccess', { language: languageName }));
    },
    onError: (error: Error) => {
      toast.error(t('language.changeError', { error: error.message }));
    },
  });

  const changeLanguage = async (language: LanguagePreference) => {
    try {
      if (!browserOnly && auth.user && auth.accessToken) {
        updateLanguageMutation.mutate(language);
      } else {
        await changeBrowserLanguagePreference(language);
      }
    } catch (error) {
      toast.error(t('language.changeError', { error: String(error) }));
    }
  };

  return {
    currentLanguage: i18n.language,
    preference,
    changeLanguage,
    isUpdating: updateLanguageMutation.isPending,
  };
}
