import { useState } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createRootRoute, createRoute, createRouter, Outlet, RouterProvider } from '@tanstack/react-router';
import '@/lib/i18n';
import '@/index.css';
import { LanguageSwitch } from '@/components/language-switch';
import { RequestDurationCell } from '@/features/requests/components/request-log-cells';
import GeneralError from '@/features/errors/general-error';
import { AuthGuard } from '@/components/auth-guard';
import { useAuthStore } from '@/stores/authStore';
import { useProjectStore } from '@/stores/projectStore';
import { applyLanguagePreference } from '@/stores/languageStore';
import { useTranslation } from 'react-i18next';

const client = new QueryClient({ defaultOptions: { queries: { staleTime: 10_000 } } });
const cachedLanguageMode = new URLSearchParams(location.search).has('cached');
const cachedUser = {
  id: 'gid://axonhub/User/1', email: 'fixture@example.test', firstName: 'Test', lastName: 'Reader',
  isOwner: false, preferLanguage: 'auto', scopes: [], roles: [], projects: [],
};
if (cachedLanguageMode) {
  useAuthStore.getState().auth.setUser(cachedUser);
  useAuthStore.getState().auth.setAccessToken('language-fixture-session');
  useProjectStore.getState().clearSelectedProjectId();
  client.setQueryData(['me', 'language-fixture-session'], cachedUser);
  void applyLanguagePreference('auto');
}

function CachedManagement() {
  const { t, i18n } = useTranslation();
  // Capture the initial language to catch a late correction after children mount.
  const [firstLanguage] = useState(i18n.language);
  return <p data-testid='cached-management' data-first-language={firstLanguage}>{t('language.auto')}</p>;
}

function CachedLanguageHarness() {
  const [publicPage, setPublicPage] = useState(false);
  return <main className='mx-auto flex max-w-3xl flex-col gap-6 p-8'>
    <h1>账号缓存返回回归夹具</h1>
    {publicPage ? <>
      <AuthGuard languageSource='browser'><LanguageSwitch browserOnly /></AuthGuard>
      <button onClick={() => {
        // Keep the real cached response fresh while a human inspects the page.
        client.setQueryData(['me', 'language-fixture-session'], cachedUser);
        setPublicPage(false);
      }}>返回管理页（复用缓存）</button>
    </> : <>
      <AuthGuard><CachedManagement /></AuthGuard>
      <button onClick={() => {
        client.setQueryData(['me', 'language-fixture-session'], cachedUser);
        setPublicPage(true);
      }}>进入自助页</button>
    </>}
  </main>;
}

const initial = { stream: true, metricsLatencyMs: null, metricsFirstTokenLatencyMs: null, outputTokensPerSecond: null };
function Harness() {
  const [request, setRequest] = useState<Parameters<typeof RequestDurationCell>[0]['request']>(initial);
  const [translated, setTranslated] = useState(false);
  const replaceTextNodes = () => {
    const walker = document.createTreeWalker(document.querySelector('[data-testid="duration"]')!, NodeFilter.SHOW_TEXT);
    const nodes: Node[] = [];
    while (walker.nextNode()) if (walker.currentNode.textContent?.trim()) nodes.push(walker.currentNode);
    for (const node of nodes) {
      const wrapper = document.createElement('font');
      wrapper.textContent = node.textContent;
      node.parentNode!.replaceChild(wrapper, node);
    }
    setTranslated(true);
  };
  return (
    <main className='mx-auto flex max-w-3xl flex-col gap-6 p-8'>
      <h1 className='text-xl font-semibold'>语言与翻译回归夹具</h1>
      <p>使用真实组件和本地合成数据；模拟翻译器替换文字节点。</p>
      <LanguageSwitch browserOnly />
      <div className='flex gap-4'>
        <button onClick={replaceTextNodes}>模拟翻译</button>
        <button onClick={() => setRequest({ stream: true, metricsLatencyMs: 2500, metricsFirstTokenLatencyMs: 700, outputTokensPerSecond: 42 })}>完成请求</button>
        <button onClick={() => setRequest(initial)}>重置请求</button>
      </div>
      <p>{translated ? '已替换文字节点' : '尚未替换文字节点'}</p>
      <section data-testid='duration'><RequestDurationCell request={request} /></section>
    </main>
  );
}
const rootRoute = createRootRoute({ component: Outlet, errorComponent: GeneralError });
const route = createRoute({ getParentRoute: () => rootRoute, path: '/tests/language/index.html', component: cachedLanguageMode ? CachedLanguageHarness : Harness });
const router = createRouter({ routeTree: rootRoute.addChildren([route]) });
createRoot(document.getElementById('root')!).render(
  <QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>
);
