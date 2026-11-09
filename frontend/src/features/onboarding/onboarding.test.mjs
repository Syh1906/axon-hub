import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { QueryClient, QueryObserver, onlineManager } from '@tanstack/react-query';
import ts from 'typescript';

// 执行真实查询与组件中的状态判断；不复制弹窗条件，也不加载无关页面依赖。
function loadFunction(path, name, bindings, beforeRender = false) {
  const source = ts.createSourceFile(path, readFileSync(new URL(path, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const fn = source.statements.find((node) => ts.isFunctionDeclaration(node) && node.name?.text === name);
  assert.ok(fn, `找不到 ${name}`);
  const code = beforeRender
    ? `function ${name}(${fn.parameters.map((node) => node.getText(source)).join(',')}) {\n${fn.body.statements
        .slice(0, fn.body.statements.findIndex(ts.isReturnStatement))
        .map((node) => node.getText(source)).join('\n')}\n}`
    : fn.getText(source).replace(/^export (default )?/, '');
  const compiled = ts.transpileModule(code, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText;
  return new Function(...Object.keys(bindings), `${compiled}\nreturn ${name};`)(...Object.values(bindings));
}

function queryOptions(request) {
  return loadFunction('../system/data/system.ts', 'useOnboardingInfo', {
    useQuery: (options) => options,
    graphqlRequest: request,
    ONBOARDING_INFO_QUERY: 'query OnboardingInfo { onboardingInfo { onboarded } }',
  })();
}

function view(path, name, owner = true) {
  let result;
  let state;
  let previousDeps;
  const render = loadFunction(path, name, {
    useOnboardingInfo: () => result,
    useAuthStore: (select) => select({ auth: { user: { isOwner: owner } } }),
    useTranslation: () => ({ t: (key) => key }),
    useState: (initial) => [state ??= initial, (value) => { state = value; }],
    useCallback: (callback) => callback,
    useEffect: (effect, deps) => {
      if (!previousDeps || deps.some((value, index) => !Object.is(value, previousDeps[index]))) effect();
      previousDeps = deps;
    },
  }, true);
  return (next, props = {}) => {
    result = next;
    render(props);
    return state;
  };
}

const mainView = (owner) => view('./onboarding-provider.tsx', 'OnboardingProvider', owner);
const modelView = () => view('../models/index.tsx', 'ModelsManagement');
const completed = { onboarded: true, autoDisableChannel: { onboarded: true }, systemModelSetting: { onboarded: true } };
const success = (data) => ({ data, isSuccess: true, isLoading: false });

function clientFor(t) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
  t.after(() => client.clear());
  return client;
}

test('首次状态、未完成子引导和已完成状态保持原有显示规则', () => {
  const main = mainView();
  assert.equal(main(success(null)), 'main');
  assert.equal(main(success({ onboarded: false })), 'main');
  assert.equal(main(success({ onboarded: true })), 'autoDisableChannel');
  assert.equal(main(success(completed)), 'none');
  assert.equal(mainView(false)(success(null)), 'none');
  assert.equal(main(success(null), { showOnboarding: false }), 'none');
  const models = modelView();
  assert.equal(models(success(null)), false);
  assert.equal(models(success({ onboarded: true })), true);
  assert.equal(modelView()(success(completed)), false);
});

test('无缓存且离线暂停时不弹首次引导，恢复后按真实记录显示', async (t) => {
  const client = clientFor(t);
  const wasOnline = onlineManager.isOnline();
  client.mount();
  t.after(() => { client.unmount(); onlineManager.setOnline(wasOnline); });
  onlineManager.setOnline(false);
  let finish;
  let calls = 0;
  const response = new Promise((resolve) => { finish = resolve; });
  const observer = new QueryObserver(client, queryOptions(() => { calls++; return response; }));
  t.after(() => observer.destroy());
  const main = mainView();
  const models = modelView();
  observer.subscribe(() => {});
  let result = observer.getCurrentResult();
  assert.equal(result.fetchStatus, 'paused');
  assert.equal(result.isLoading, false);
  assert.equal(calls, 0);
  assert.equal(main(result), 'none');
  assert.equal(models(result), false);
  onlineManager.setOnline(true);
  await new Promise((resolve) => setTimeout(resolve, 0));
  result = observer.getCurrentResult();
  assert.equal(result.isLoading, true);
  assert.equal(main(result), 'none');
  finish({ onboardingInfo: completed });
  await observer.refetch();
  result = observer.getCurrentResult();
  assert.equal(result.isSuccess, true);
  assert.equal(main(result), 'none');
  assert.equal(models(result), false);
});

test('第一次读取失败不伪造完成记录，也不触发任何引导；重读为空才显示首次引导', async (t) => {
  const client = clientFor(t);
  const failure = new Error('读取超时');
  let fail = true;
  const observer = new QueryObserver(client, queryOptions(async () => {
    if (fail) throw failure;
    return { onboardingInfo: null };
  }));
  t.after(() => observer.destroy());
  const main = mainView();
  const models = modelView();
  const failed = await observer.refetch();
  assert.equal(failed.isError, true);
  assert.equal(failed.error, failure);
  assert.equal(failed.data, undefined);
  assert.equal(main(failed), 'none');
  assert.equal(models(failed), false);
  fail = false;
  const recovered = await observer.refetch();
  assert.equal(recovered.isSuccess, true);
  assert.equal(main(recovered), 'main');
});

test('后台刷新失败保留完成记录且不触发主引导或子引导', async (t) => {
  const client = clientFor(t);
  const failure = new Error('网络异常');
  const options = queryOptions(async () => { throw failure; });
  client.setQueryData(options.queryKey, completed);
  const observer = new QueryObserver(client, options);
  t.after(() => observer.destroy());
  const main = mainView();
  const models = modelView();
  assert.equal(main(observer.getCurrentResult()), 'none');
  assert.equal(models(observer.getCurrentResult()), false);
  const failed = await observer.refetch();
  assert.equal(failed.error, failure);
  assert.equal(failed.data, completed);
  assert.equal(main(failed), 'none');
  assert.equal(models(failed), false);
});

test('读取失败不启动引导，也不打断已经显示的引导', () => {
  const main = mainView();
  assert.equal(main(success(null)), 'main');
  const failed = { data: undefined, isSuccess: false, isLoading: false };
  assert.equal(main(failed), 'main');
  assert.equal(mainView()(failed), 'none');
  const models = modelView();
  const unfinished = { onboarded: true };
  assert.equal(models(success(unfinished)), true);
  const failedRefresh = { data: unfinished, isSuccess: false, isLoading: false };
  assert.equal(models(failedRefresh), true);
  assert.equal(modelView()(failedRefresh), false);
});

test('查询保留取消信号，失败仍交给既有查询重试机制', async (t) => {
  const client = clientFor(t);
  let calls = 0;
  const options = queryOptions(async (_query, _variables, _headers, init) => {
    assert.ok(init.signal instanceof AbortSignal);
    if (++calls === 1) throw new Error('短暂断线');
    return { onboardingInfo: completed };
  });
  assert.deepEqual(await client.fetchQuery({ ...options, retry: 1, retryDelay: 0 }), completed);
  assert.equal(calls, 2);
});
