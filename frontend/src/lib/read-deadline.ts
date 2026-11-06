// 管理端读取包含下载与解析；取消只终止当前调用，不影响其他缓存订阅。
export async function withReadDeadline<T>(read: (signal: AbortSignal) => Promise<T>, signal?: AbortSignal, timeoutMs = 35_000): Promise<T> {
  const controller = new AbortController();
  const cancel = () => controller.abort(signal?.reason);
  if (signal?.aborted) cancel();
  else signal?.addEventListener('abort', cancel, { once: true });
  const timer = setTimeout(() => controller.abort(new DOMException('Read timed out', 'TimeoutError')), timeoutMs);
  try {
    return await read(controller.signal);
  } catch (error) {
    if (controller.signal.aborted) throw controller.signal.reason;
    throw error;
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener('abort', cancel);
  }
}
