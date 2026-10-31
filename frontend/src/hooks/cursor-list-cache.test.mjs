import { InfiniteQueryObserver, QueryClient } from '@tanstack/react-query';
import assert from 'node:assert/strict';
import test from 'node:test';
import { trimCursorList } from './cursor-list-cache.ts';

const page = (id) => ({
  edges: [{ node: { id: String(id) }, cursor: String(id) }],
  pageInfo: { hasNextPage: true, endCursor: String(id) },
});

for (const list of ['requests', 'channels']) {
  test(`${list}: returning to a cached filter refreshes only its head and can load history again`, async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
    const key = [list, 'infinite', { filter: 'previous' }];
    const other = [list, 'infinite', { filter: 'other' }];
    const cached = { pages: [page(1), page(2), page(3)], pageParams: [null, '1', '2'] };
    client.setQueryData(key, cached, { updatedAt: 1 });
    client.setQueryData(other, cached);
    const calls = [];
    const options = {
      queryKey: key,
      initialPageParam: null,
      enabled: false,
      refetchOnMount: 'always',
      queryFn: async ({ pageParam }) => {
        calls.push(pageParam);
        return page(Number(pageParam ?? 0) + 1);
      },
      getNextPageParam: (value) => value.pageInfo.endCursor,
    };
    const observer = new InfiniteQueryObserver(client, options);
    let finishRefresh;
    const refreshed = new Promise((resolve) => {
      finishRefresh = resolve;
    });
    const unsubscribe = observer.subscribe((result) => {
      if (calls.length && !result.isFetching) finishRefresh();
    });
    try {
      assert.deepEqual(calls, []);
      await trimCursorList(client, key, 0);
      assert.equal(client.getQueryState(key).dataUpdatedAt, 1);
      observer.setOptions({ ...options, enabled: true });
      await refreshed;
      assert.deepEqual(calls, [null]);
      assert.equal(client.getQueryData(key).pages.length, 1);
      assert.equal(client.getQueryData(other).pages.length, 3);

      await observer.refetch();
      assert.deepEqual(calls, [null, null]);
      await observer.fetchNextPage();
      assert.deepEqual(calls, [null, null, '1']);
      assert.equal(client.getQueryData(key).pages.length, 2);
    } finally {
      unsubscribe();
      client.clear();
    }
  });
}

test('trimming cancels a pending history page so it cannot restore the discarded tail', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
  const key = ['requests', 'infinite'];
  client.setQueryData(key, { pages: [page(1), page(2)], pageParams: [null, '1'] });
  let aborted = false;
  let complete;
  const observer = new InfiniteQueryObserver(client, {
    queryKey: key,
    enabled: false,
    initialPageParam: null,
    getNextPageParam: (value) => value.pageInfo.endCursor,
    queryFn: ({ signal }) =>
      new Promise((resolve) => {
        complete = resolve;
        signal.addEventListener('abort', () => {
          aborted = true;
        });
      }),
  });
  const unsubscribe = observer.subscribe(() => {});
  try {
    const pending = observer.fetchNextPage();
    await trimCursorList(client, key, 0);
    complete(page(3));
    await pending;
    assert.equal(aborted, true);
    assert.deepEqual(client.getQueryData(key).pageParams, [null]);
    assert.deepEqual(
      client.getQueryData(key).pages.map((value) => value.edges[0].node.id),
      ['1']
    );
  } finally {
    unsubscribe();
    client.clear();
  }
});

test('returning with only one cached page cancels its pending second page before enabling refresh', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
  const key = ['requests', 'infinite'];
  client.setQueryData(key, { pages: [page(1)], pageParams: [null] });
  let aborted = false;
  let complete;
  const calls = [];
  const options = {
    queryKey: key,
    enabled: false,
    staleTime: 0,
    initialPageParam: null,
    getNextPageParam: (value) => value.pageInfo.endCursor,
    queryFn: ({ pageParam, signal }) => {
      calls.push(pageParam);
      if (pageParam === null) return Promise.resolve(page(1));
      return new Promise((resolve) => {
        complete = resolve;
        signal.addEventListener('abort', () => {
          aborted = true;
        });
      });
    },
  };
  const observer = new InfiniteQueryObserver(client, options);
  let finishRefresh;
  const refreshed = new Promise((resolve) => {
    finishRefresh = resolve;
  });
  const unsubscribe = observer.subscribe((result) => {
    if (calls.includes(null) && !result.isFetching) finishRefresh();
  });
  try {
    const pending = observer.fetchNextPage();
    await trimCursorList(client, key, 0);
    observer.setOptions({ ...options, enabled: true });
    complete(page(2));
    await pending;
    await refreshed;
    assert.equal(aborted, true);
    assert.deepEqual(calls, ['1', null]);
    assert.deepEqual(client.getQueryData(key).pageParams, [null]);
    await observer.refetch();
    assert.deepEqual(calls, ['1', null, null]);
  } finally {
    unsubscribe();
    client.clear();
  }
});

test('upward scrolling retains the complete batch containing the reading position', async () => {
  const client = new QueryClient();
  const key = ['channels', 'infinite'];
  const pages = [page(1), page(2), page(3)];
  client.setQueryData(key, { pages, pageParams: [null, '1', '2'] });
  try {
    await trimCursorList(client, key, 2);
    assert.deepEqual(client.getQueryData(key).pageParams, [null, '1']);
    assert.equal(client.getQueryData(key).pages[0], pages[0]);
    await trimCursorList(client, key, 99);
    assert.equal(client.getQueryData(key).pages.length, 2);
  } finally {
    client.clear();
  }
});
