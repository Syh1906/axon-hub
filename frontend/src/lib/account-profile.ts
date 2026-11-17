import type { QueryClient } from '@tanstack/react-query';
import { useAuthStore, type AuthUser } from '@/stores/authStore';

type ProfileUpdate = Partial<Pick<AuthUser, 'firstName' | 'lastName' | 'preferLanguage' | 'avatar'>>;

// The write may renew the session, creating another Me query without cached
// data. Cancel that read before publishing the authoritative saved profile.
export async function syncCurrentAccountProfile(queryClient: QueryClient, userId: string | undefined, profile: ProfileUpdate) {
  const before = useAuthStore.getState().auth;
  if (!before.user || before.user.id !== userId) return false;
  await queryClient.cancelQueries({ queryKey: ['me', before.accessToken] });

  const current = useAuthStore.getState().auth;
  if (!current.user || current.user.id !== userId) return false;
  const updated = { ...current.user, ...profile };
  current.setUser(updated);
  queryClient.setQueryData<AuthUser>(['me', current.accessToken], updated);
  // The active page's guard selects its language source. A late profile write
  // must not change the language of a public page visited while it was saving.
  void queryClient.invalidateQueries({ queryKey: ['me', current.accessToken] });
  return true;
}
