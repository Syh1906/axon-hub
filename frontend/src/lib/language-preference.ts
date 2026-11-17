export type LanguagePreference = 'auto' | 'en' | 'zh';
export type InterfaceLanguage = Exclude<LanguagePreference, 'auto'>;

// Unlike i18nextLng, this key only records an explicit browser-level choice.
export const LANGUAGE_PREFERENCE_KEY = 'axonhub_language_preference';

export function supportedLanguage(value: string): InterfaceLanguage | undefined {
  const base = value.toLowerCase().split(/[-_]/)[0];
  return base === 'en' || base === 'zh' ? base : undefined;
}

export const languageDetectionOptions = {
  order: ['localStorage', 'navigator'],
  lookupLocalStorage: LANGUAGE_PREFERENCE_KEY,
  caches: [],
  convertDetectedLanguage: (language: string) => supportedLanguage(language) ?? language,
};

export function normalizeLanguagePreference(value?: string): LanguagePreference {
  return value === 'auto' ? 'auto' : value ? supportedLanguage(value) ?? 'auto' : 'auto';
}

export function resolveLanguage(preference: LanguagePreference, languages: readonly string[] = navigator.languages): InterfaceLanguage {
  if (preference !== 'auto') return preference;
  for (const language of languages) {
    const supported = supportedLanguage(language);
    if (supported) return supported;
  }
  return 'en';
}

export function readBrowserLanguagePreference(): LanguagePreference {
  try {
    return normalizeLanguagePreference(localStorage.getItem(LANGUAGE_PREFERENCE_KEY) ?? undefined);
  } catch {
    return 'auto';
  }
}

export function saveBrowserLanguagePreference(preference: LanguagePreference) {
  try {
    localStorage.setItem(LANGUAGE_PREFERENCE_KEY, preference);
  } catch {
    // A blocked browser store must not prevent changing the current page language.
  }
}
