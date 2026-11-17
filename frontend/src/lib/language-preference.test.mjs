import assert from 'node:assert/strict';
import test from 'node:test';
import i18next from 'i18next';
import LanguageDetector from 'i18next-browser-languagedetector';
import {
  LANGUAGE_PREFERENCE_KEY, languageDetectionOptions, normalizeLanguagePreference,
  readBrowserLanguagePreference, resolveLanguage, saveBrowserLanguagePreference,
} from './language-preference.ts';

function browser(t, languages, values = new Map()) {
  for (const [name, value] of Object.entries({
    navigator: { languages, language: languages[0] },
    localStorage: {
      getItem: (key) => values.get(key) ?? null,
      setItem: (key, value) => values.set(key, value),
      removeItem: (key) => values.delete(key),
    },
  })) {
    const previous = Object.getOwnPropertyDescriptor(globalThis, name);
    Object.defineProperty(globalThis, name, { configurable: true, value });
    t.after(() => previous ? Object.defineProperty(globalThis, name, previous) : delete globalThis[name]);
  }
  const previousWindow = globalThis.window;
  globalThis.window = { localStorage: globalThis.localStorage };
  t.after(() => { globalThis.window = previousWindow; });
  return values;
}

async function detect() {
  const instance = i18next.createInstance();
  await instance.use(LanguageDetector).init({
    resources: { en: { translation: {} }, zh: { translation: {} } },
    supportedLngs: ['en', 'zh', 'zh-CN'], fallbackLng: 'en', detection: languageDetectionOptions,
  });
  return instance;
}

test('自动模式按浏览器优先级匹配，未支持的语言落到英文', async (t) => {
  for (const [languages, expected] of [
    [['zh-CN', 'en-US'], 'zh'], [['en-GB', 'zh-TW'], 'en'],
    [['fr-FR', 'zh-HK', 'en'], 'zh'], [['ja-JP'], 'en'], [[], 'en'],
  ]) {
    await t.test(languages.join(',') || '空列表', async (t) => {
      browser(t, languages, new Map([[LANGUAGE_PREFERENCE_KEY, 'auto']]));
      assert.equal(resolveLanguage('auto'), expected);
      assert.equal((await detect()).language, expected);
    });
  }
});

test('旧自动检测缓存失效，自动检测和程序切换不会写入手动偏好', async (t) => {
  const values = browser(t, ['zh-CN'], new Map([['i18nextLng', 'en']]));
  assert.equal(readBrowserLanguagePreference(), 'auto');
  const instance = await detect();
  assert.equal(instance.language, 'zh');
  await instance.changeLanguage('en');
  assert.equal(values.has(LANGUAGE_PREFERENCE_KEY), false);
  assert.equal((await detect()).language, 'zh');
});

test('手动选择跨重新加载保留，切回自动重新使用浏览器语言', async (t) => {
  browser(t, ['zh-CN']);
  saveBrowserLanguagePreference('en');
  assert.equal(readBrowserLanguagePreference(), 'en');
  assert.equal((await detect()).language, 'en');
  assert.equal(resolveLanguage('en', ['zh-CN']), 'en');
  saveBrowserLanguagePreference('auto');
  assert.equal((await detect()).language, 'zh');
  assert.equal(normalizeLanguagePreference('zh-CN'), 'zh');
  assert.equal(normalizeLanguagePreference('invalid'), 'auto');
});

test('存储不可用时仍能按浏览器选择语言', (t) => {
  browser(t, ['zh-CN']);
  globalThis.localStorage.getItem = () => { throw new Error('blocked'); };
  globalThis.localStorage.setItem = () => { throw new Error('blocked'); };
  assert.equal(readBrowserLanguagePreference(), 'auto');
  assert.doesNotThrow(() => saveBrowserLanguagePreference('en'));
  assert.equal(resolveLanguage('auto'), 'zh');
});
