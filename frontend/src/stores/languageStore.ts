import { create } from 'zustand';
import i18n from '@/lib/i18n';
import {
  normalizeLanguagePreference,
  readBrowserLanguagePreference,
  resolveLanguage,
  saveBrowserLanguagePreference,
  type LanguagePreference,
} from '@/lib/language-preference';

export const useLanguageStore = create<{ preference: LanguagePreference }>(() => ({
  preference: readBrowserLanguagePreference(),
}));

export function applyLanguagePreference(value?: string) {
  const preference = normalizeLanguagePreference(value);
  if (useLanguageStore.getState().preference !== preference) useLanguageStore.setState({ preference });
  const language = resolveLanguage(preference);
  return language === i18n.language ? Promise.resolve() : i18n.changeLanguage(language);
}

export function changeBrowserLanguagePreference(preference: LanguagePreference) {
  saveBrowserLanguagePreference(preference);
  return applyLanguagePreference(preference);
}

export function restoreBrowserLanguagePreference() {
  return applyLanguagePreference(readBrowserLanguagePreference());
}

window.addEventListener('languagechange', () => {
  if (useLanguageStore.getState().preference === 'auto') void applyLanguagePreference('auto');
});
