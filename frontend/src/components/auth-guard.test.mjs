import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';
import * as jsxRuntime from 'react/jsx-runtime';
import { create } from 'zustand';
import { QueryClient, QueryObserver } from '@tanstack/react-query';
import i18next from 'i18next';
import * as preference from '../lib/language-preference.ts';
import { isProjectSelectionValid } from '../lib/project-membership.ts';

function load(path, dependencies) {
  const code = ts.transpileModule(readFileSync(new URL(path, import.meta.url), 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2023, jsx: ts.JsxEmit.ReactJSX },
  }).outputText;
  const exports = {};
  new Function('require', 'exports', code)((name) => {
    assert.ok(name in dependencies, `unexpected dependency: ${name}`);
    return dependencies[name];
  }, exports);
  return exports;
}

test('账号资料来自缓存或网络时按页面语言来源挂载', async (t) => {
  for (const [account, browserChoice, browserLanguages, expected, languageSource = 'account', network = false] of [
    ['auto', 'en', ['zh-CN'], 'zh'],
    ['en', 'zh', ['zh-CN'], 'en'],
    ['auto', 'en', ['en-US'], 'en'],
    ['auto', 'en', ['zh-CN'], 'zh', 'account', true],
    ['auto', 'en', ['zh-CN'], 'en', 'browser'],
  ]) {
    await t.test(`${account}/${browserChoice}/${browserLanguages[0]}/${languageSource}/${network ? 'network' : 'cache'}`, async (t) => {
      for (const [name, value] of Object.entries({
        navigator: { languages: browserLanguages },
        window: { addEventListener() {}, location: { pathname: '/project/requests' } },
        localStorage: { getItem: () => browserChoice, setItem() {} },
      })) {
        const previous = Object.getOwnPropertyDescriptor(globalThis, name);
        Object.defineProperty(globalThis, name, { configurable: true, value });
        t.after(() => previous ? Object.defineProperty(globalThis, name, previous) : delete globalThis[name]);
      }
      const i18n = i18next.createInstance();
      await i18n.init({ lng: browserChoice, resources: { en: { translation: {} }, zh: { translation: {} } } });
      const language = load('../stores/languageStore.ts', {
        zustand: { create }, '@/lib/i18n': { default: i18n }, '@/lib/language-preference': preference,
      });
      const client = new QueryClient({ defaultOptions: { queries: { staleTime: 10_000 } } });
      const user = { id: 'reader', preferLanguage: account, projects: [] };
      if (!network) client.setQueryData(['me', 'fixture-session'], user);
      let releaseRead;
      const reading = new Promise((resolve) => { releaseRead = resolve; });
      let requests = 0;
      const observer = new QueryObserver(client, {
        queryKey: ['me', 'fixture-session'], queryFn: async () => { requests++; if (network) await reading; return user; },
      });
      const unsubscribe = observer.subscribe(() => {});
      t.after(() => { unsubscribe(); client.clear(); });
      const effects = [];
      const { AuthGuard } = load('./auth-guard.tsx', {
        'react/jsx-runtime': jsxRuntime,
        react: { useEffect: (fn) => effects.push(fn) },
        '@tanstack/react-router': { useRouter: () => ({ navigate() { throw new Error('unexpected redirect'); } }) },
        '@/gql/graphql': { isAuthError: () => false },
        'react-i18next': { useTranslation: () => ({ t: (key) => key, i18n }) },
        '@/stores/authStore': { useAuthStore: (select) => select({ auth: { accessToken: 'fixture-session' } }) },
        '@/stores/projectStore': { useSelectedProjectId: () => null },
        '@/lib/load-error': { loadErrorKey: () => '', loadErrorRequestId: () => '' },
        '@/lib/project-membership': { isProjectSelectionValid },
        '@/lib/language-preference': preference,
        '@/stores/languageStore': { ...language, useLanguageStore: (select) => select(language.useLanguageStore.getState()) },
        '@/components/ui/button': { Button: 'button' },
        '@/components/ui/skeleton': { Skeleton: 'div' },
        '@/features/auth/data/auth': { useMe: () => observer.getCurrentResult() },
      });
      const child = jsxRuntime.jsx('p', { children: 'management page' });
      if (network) {
        assert.notEqual(AuthGuard({ children: child, languageSource }).props.children, child);
        releaseRead();
        await observer.refetch();
        effects.splice(0);
      }
      const first = AuthGuard({ children: child, languageSource });
      if (languageSource === 'account') assert.notEqual(first.props.children, child, 'do not mount children in the previous page language');
      else assert.equal(first.props.children, child, 'self-usage must not wait for account language');
      for (const effect of effects.splice(0)) effect();
      assert.equal(i18n.language, expected);
      assert.equal(language.useLanguageStore.getState().preference, languageSource === 'account' ? account : browserChoice);
      assert.equal(AuthGuard({ children: child, languageSource }).props.children, child);
      assert.equal(requests, network ? 1 : 0, 'fresh cache recovery must not depend on another Me request');
    });
  }
});
