import i18n from 'i18next';
import LanguageDetector from 'i18next-browser-languagedetector';
import { initReactI18next } from 'react-i18next';
import { formatCurrencyValue } from '@/utils/currency-formatter';
import { languageDetectionOptions, supportedLanguage } from './language-preference';

function mergeTranslations(...translations: Array<Record<string, unknown>>) {
  return Object.assign({}, ...translations);
}

type LocaleModule = {
  default: Record<string, unknown>;
};

function getModuleDefaultExport(module: unknown): Record<string, unknown> {
  if (module && typeof module === 'object' && 'default' in module) {
    return (module as LocaleModule).default;
  }
  return module as Record<string, unknown>;
}

const enModules = import.meta.glob('../locales/en/*.json', { eager: true }) as Record<string, unknown>;
const zhCNModules = import.meta.glob('../locales/zh-CN/*.json', { eager: true }) as Record<string, unknown>;

const enTranslation = mergeTranslations(...Object.values(enModules).map(getModuleDefaultExport));
const zhTranslation = mergeTranslations(...Object.values(zhCNModules).map(getModuleDefaultExport));

const resources = {
  en: {
    translation: enTranslation,
  },
  zh: {
    translation: zhTranslation,
  },
  'zh-CN': {
    translation: zhTranslation,
  },
};

// Keep the browser's language declaration consistent with the rendered interface.
i18n.on('languageChanged', (language) => {
  document.documentElement.lang = supportedLanguage(language) === 'zh' ? 'zh-CN' : 'en';
});

i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources,
    fallbackLng: 'en',
    debug: false,
    supportedLngs: ['en', 'zh', 'zh-CN'],

    interpolation: {
      escapeValue: false, // React 已经默认转义了

    },

    detection: languageDetectionOptions,
  }).then(() => {
    i18n.services.formatter?.add('currency', (value, lng, options) =>
      formatCurrencyValue(value, options?.locale || lng, {
        style: 'currency', currency: options?.currency || 'USD', currencyDisplay: 'narrowSymbol',
        minimumFractionDigits: options?.minimumFractionDigits, maximumFractionDigits: options?.maximumFractionDigits,
      })
    );
  });

export default i18n;
