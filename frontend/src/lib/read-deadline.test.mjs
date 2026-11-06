import assert from 'node:assert/strict';
import test from 'node:test';
import { withReadDeadline } from './read-deadline.ts';

const pendingRead = (signal) =>
  new Promise((resolve, reject) => {
    if (signal.aborted) reject(signal.reason);
    else signal.addEventListener('abort', () => reject(signal.reason), { once: true });
  });

test('deadline aborts a stalled read', async () => {
  await assert.rejects(withReadDeadline(pendingRead, undefined, 10), { name: 'TimeoutError' });
});
test('navigation cancellation retains its reason', async () => {
  const controller = new AbortController();
  const read = withReadDeadline(pendingRead, controller.signal);
  controller.abort();
  await assert.rejects(read, { name: 'AbortError' });
});
test('completed reads clear timers and cancellation listeners', async () => {
  const controller = new AbortController();
  let child;
  assert.equal(
    await withReadDeadline(
      async (signal) => {
        child = signal;
        return 42;
      },
      controller.signal,
      10
    ),
    42
  );
  controller.abort();
  await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(child.aborted, false);
});
test('an already canceled caller never becomes a timeout', async () => {
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(withReadDeadline(pendingRead, controller.signal), { name: 'AbortError' });
});
