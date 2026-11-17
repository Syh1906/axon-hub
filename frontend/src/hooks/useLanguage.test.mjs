import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';
import { create } from 'zustand';
import { QueryClient, QueryObserver, MutationObserver } from '@tanstack/react-query';
import i18next from 'i18next';
import * as preferences from '../lib/language-preference.ts';

function load(path, dependencies) {
  const code = ts.transpileModule(readFileSync(new URL(path, import.meta.url), 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2023 },
  }).outputText;
  const exports = {};
  new Function('require', 'exports', code)((name) => {
    assert.ok(name in dependencies, `unexpected dependency: ${name}`);
    return dependencies[name];
  }, exports);
  return exports;
}

async function setup(t, { signedIn = false, browserOnly = false, fail = false } = {}) {
  const values = new Map(), listeners = new Map(), languages = ['zh-CN'];
  for (const [name, value] of Object.entries({
    navigator: { languages },
    window: { addEventListener: (name, fn) => listeners.set(name, fn) },
    localStorage: { getItem: (key) => values.get(key) ?? null, setItem: (key, value) => values.set(key, value) },
  })) {
    const previous = Object.getOwnPropertyDescriptor(globalThis, name);
    Object.defineProperty(globalThis, name, { configurable: true, value });
    t.after(() => previous ? Object.defineProperty(globalThis, name, previous) : delete globalThis[name]);
  }
  const i18n = i18next.createInstance();
  await i18n.init({ lng: 'zh', resources: { en: { translation: {} }, zh: { translation: {} } } });
  const store = load('../stores/languageStore.ts', { zustand: { create }, '@/lib/i18n': { default: i18n }, '@/lib/language-preference': preferences });
  const user = { id: 'reader', preferLanguage: 'auto', scopes: ['read_requests'] };
  const auth = { user: signedIn ? user : null, accessToken: signedIn ? 'fixture-session' : '', setUser: (value) => { auth.user = value; } };
  const useAuthStore = Object.assign((select) => select({ auth: { ...auth } }), { getState: () => ({ auth }) });
  const queryClient = new QueryClient();
  if (signedIn) queryClient.setQueryData(['me', auth.accessToken], user);
  t.after(() => queryClient.clear());
  const requests = [], errors = [];
  let mutation, inFlight;
  const profile = load('../lib/account-profile.ts', { '@/stores/authStore': { useAuthStore } });
  const { useLanguage } = load('./useLanguage.ts', {
    react: { useEffect: (fn) => fn() },
    '@tanstack/react-query': {
      useQueryClient: () => queryClient,
      useMutation: (options) => {
        mutation = options;
        return { isPending: false, mutate: (language) => {
          inFlight = options.mutationFn(language).then((value) => options.onSuccess(value, language), options.onError);
        } };
      },
    },
    '@/gql/graphql': { graphqlRequest: async (_query, variables) => {
      requests.push(variables.input);
      if (fail) throw new Error('fixture save failed');
      return { updateMe: { preferLanguage: variables.input.preferLanguage } };
    } },
    '@/gql/users': { UPDATE_ME_MUTATION: 'fixture' },
    'react-i18next': { useTranslation: () => ({ i18n, t: (key) => key }) },
    sonner: { toast: { success() {}, error: (value) => errors.push(value) } },
    '@/stores/authStore': { useAuthStore },
    '@/lib/account-profile': profile,
    '@/stores/languageStore': { ...store, useLanguageStore: (select) => select(store.useLanguageStore.getState()) },
  });
  const hook = useLanguage({ browserOnly });
  return { ...store, i18n, auth, requests, errors, values, languages, listeners, mutation, queryClient,
    change: async (preference) => { await hook.changeLanguage(preference); await inFlight; } };
}

test('未登录手选只保存在浏览器，自动模式才响应浏览器语言变化', async (t) => {
  const f = await setup(t);
  await f.change('en');
  assert.equal(f.i18n.language, 'en');
  assert.equal(f.values.get(preferences.LANGUAGE_PREFERENCE_KEY), 'en');
  f.listeners.get('languagechange')();
  assert.equal(f.i18n.language, 'en');
  await f.change('auto');
  assert.equal(f.i18n.language, 'zh');
  f.languages.splice(0, 1, 'en-GB');
  f.listeners.get('languagechange')();
  assert.equal(f.i18n.language, 'en');
  assert.equal(f.useLanguageStore.getState().preference, 'auto');
  assert.deepEqual(f.requests, []);
});

test('只读账号保存后同步账号和 Me 缓存，自动模式原样写入', async (t) => {
  const f = await setup(t, { signedIn: true });
  for (const preference of ['en', 'auto']) {
    await f.change(preference);
    assert.equal(f.auth.user.preferLanguage, preference);
    assert.equal(f.queryClient.getQueryData(['me', f.auth.accessToken]).preferLanguage, preference);
  }
  assert.equal(f.useLanguageStore.getState().preference, 'auto', 'save callbacks leave language selection to the active page');
  assert.equal(f.i18n.language, 'zh');
  assert.deepEqual(f.requests, [{ preferLanguage: 'en' }, { preferLanguage: 'auto' }]);
  assert.equal(f.values.has(preferences.LANGUAGE_PREFERENCE_KEY), false);
});

test('保存失败保留原模式，不回退到旧账号默认英文', async (t) => {
  const f = await setup(t, { signedIn: true, fail: true });
  await f.change('en');
  assert.equal(f.i18n.language, 'zh');
  assert.equal(f.auth.user.preferLanguage, 'auto');
  assert.equal(f.useLanguageStore.getState().preference, 'auto');
  assert.equal(f.errors.length, 1);
});

test('自助用量页仅保存浏览器偏好，不修改已登录管理账号', async (t) => {
  const f = await setup(t, { signedIn: true, browserOnly: true });
  await f.change('en');
  assert.equal(f.i18n.language, 'en');
  assert.equal(f.auth.user.preferLanguage, 'auto');
  assert.deepEqual(f.requests, []);
});

test('保存期间切换账号不污染新账号，正常续期仍接受保存结果', async (t) => {
  const f = await setup(t, { signedIn: true });
  f.auth.accessToken = 'renewed-fixture-session';
  f.queryClient.setQueryData(['me', f.auth.accessToken], f.auth.user);
  await f.mutation.onSuccess({ preferLanguage: 'en' });
  assert.equal(f.i18n.language, 'zh');
  assert.equal(f.queryClient.getQueryData(['me', f.auth.accessToken]).preferLanguage, 'en');
  f.auth.user = { id: 'another', preferLanguage: 'en' };
  await f.mutation.onSuccess({ preferLanguage: 'zh' });
  assert.equal(f.i18n.language, 'zh');
  assert.equal(f.auth.user.preferLanguage, 'en');
});

test('续期产生无缓存的在途旧 Me 时，保存结果不会被它覆盖', async (t) => {
  const f = await setup(t, { signedIn: true });
  f.auth.accessToken = 'renewed-fixture-session';
  const key = ['me', f.auth.accessToken];
  const staleUser = { ...f.auth.user };
  let releaseOldRead, reads = 0, aborted = false;
  const oldRead = new Promise((resolve) => { releaseOldRead = resolve; });
  const observer = new QueryObserver(f.queryClient, {
    queryKey: key,
    queryFn: async ({ signal }) => {
      reads++;
      if (reads === 1) {
        signal.addEventListener('abort', () => { aborted = true; });
        await oldRead;
        return staleUser;
      }
      return f.auth.user;
    },
  });
  const unsubscribe = observer.subscribe(() => {});
  t.after(unsubscribe);
  assert.equal(f.queryClient.getQueryData(key), undefined);
  // User leaves for the public page while saving; a late save must not change it.
  await f.changeBrowserLanguagePreference('zh');
  const saving = new MutationObserver(f.queryClient, {
    mutationFn: async () => ({ preferLanguage: 'en' }),
    onSuccess: f.mutation.onSuccess,
  });
  await saving.mutate();
  assert.equal(aborted, true);
  assert.equal(f.queryClient.getQueryData(key).id, 'reader');
  assert.equal(f.queryClient.getQueryData(key).preferLanguage, 'en');
  releaseOldRead();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(f.auth.user.preferLanguage, 'en');
  assert.equal(f.queryClient.getQueryData(key).preferLanguage, 'en');
  assert.equal(f.i18n.language, 'zh');
  assert.equal(f.useLanguageStore.getState().preference, 'zh');
});
