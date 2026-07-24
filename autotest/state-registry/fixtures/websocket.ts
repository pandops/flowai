// Concurrent WebSocket helper for the state-registry autotest suite.
// The helper opens one Node WebSocket per requested URL and returns
// the open/close/event primitives so the test can drive concurrent
// subscriptions without browser fixtures.
//
// Contract:
//   - openApiOnlySocket awaits the actual 'open' event before
//     returning. It never resolves on a closed/closing socket.
//   - nextMessage returns ONE promise per waiter. The promise's
//     own resolve/reject are stored on the entry and called on
//     timeout, message arrival, or close — never via a different
//     promise chain.
//   - timeout removes the exact waiter entry it spawned; other
//     waiters continue to wait.
//   - close() settles every pending waiter, removes every listener
//     it owns, awaits the actual 'close' event with a hard timeout
//     and a bounded terminate fallback, and throws an
//     AggregateError if ws.close() / ws.terminate() reported
//     exceptions.
//   - errors() registers one-shot listeners, removes BOTH the
//     success-path and timeout-path listeners when it settles, and
//     never returns two errors for a single call.
//   - openConcurrentSockets opens each URL in parallel via tracked
//     Promise.allSettled, closes every successful partial open when
//     any fails, and throws an AggregateError containing the
//     original rejection plus every close failure.
import { setTimeout as delay } from 'node:timers/promises';
import WebSocket from 'ws';

export interface OpenSocket {
  url: string;
  ws: WebSocket;
  opened(): Promise<void>;
  close(): Promise<void>;
  nextMessage(timeoutMs: number): Promise<unknown>;
  errors(): Promise<unknown>;
}

interface PendingMessage {
  resolve: (v: unknown) => void;
  reject: (err: Error) => void;
  timer?: NodeJS.Timeout;
}

const CloseTimeoutMs = 1_500;
const TerminateTimeoutMs = 3_000;
const SettleDelayMs = 10;
const ErrorsHardBoundMs = 50;
const NextMessageNoopPreMark = true;

async function waitForClosed(ws: WebSocket, timeoutMs: number): Promise<void> {
  if (ws.readyState === WebSocket.CLOSED) return;
  await new Promise<void>((resolve, reject) => {
    let done = false;
    const finish = (err?: Error) => {
      if (done) return;
      done = true;
      if (err) reject(err);
      else resolve();
    };
    const timer = setTimeout(
      () => finish(new Error(`WebSocket did not reach CLOSED within ${timeoutMs}ms`)),
      timeoutMs,
    );
    const onClose = () => {
      clearTimeout(timer);
      finish();
    };
    ws.once('close', onClose);
    if (ws.readyState === WebSocket.CLOSED) {
      clearTimeout(timer);
      finish();
    }
  });
}

export async function openApiOnlySocket(url: string, timeoutMs = 5_000): Promise<OpenSocket> {
  return await openApiOnlySocketWithHeaders(url, undefined, timeoutMs);
}

export async function openApiOnlySocketWithHeaders(
  url: string,
  headers: Record<string, string> | undefined,
  timeoutMs = 5_000,
): Promise<OpenSocket> {
  let ws: WebSocket;
  try {
    if (headers && Object.keys(headers).length > 0) {
      ws = new WebSocket(url, undefined, { headers });
    } else {
      ws = new WebSocket(url);
    }
  } catch (err) {
    throw new Error(`WebSocket construction failed for ${url}: ${(err as Error).message}`);
  }

  let resolveOpen: (() => void) | undefined;
  let rejectOpen: ((err: Error) => void) | undefined;
  const opened = new Promise<void>((resolve, reject) => {
    resolveOpen = resolve;
    rejectOpen = reject;
  });
  let openSettled = false;
  let openTimer: NodeJS.Timeout | undefined;
  const settleOpen = (settle: () => void) => {
    if (openSettled) return;
    openSettled = true;
    if (openTimer) clearTimeout(openTimer);
    settle();
  };
  openTimer = setTimeout(() => {
    settleOpen(() => rejectOpen?.(new Error(`WebSocket open timeout url=${url}`)));
  }, timeoutMs);

  const pending: PendingMessage[] = [];
  const messageQueue: unknown[] = [];
  const errorQueue: Error[] = [];

  const onMessage = (data: WebSocket.RawData) => {
    const text = data.toString('utf-8');
    let parsed: unknown = text;
    try {
      parsed = JSON.parse(text);
    } catch {
      // leave parsed as raw text
    }
    const next = pending.shift();
    if (next) {
      clearTimeout(next.timer);
      next.resolve(parsed);
      return;
    }
    messageQueue.push(parsed);
  };

  const settleAllPending = (err: Error) => {
    while (pending.length > 0) {
      const next = pending.shift();
      if (!next) break;
      clearTimeout(next.timer);
      next.reject(err);
    }
  };

  const onPersistentError = (err: Error) => {
    errorQueue.push(err);
    settleAllPending(err);
    settleOpen(() => rejectOpen?.(err));
  };
  ws.on('error', onPersistentError);
  ws.once('open', () => {
    settleOpen(() => resolveOpen?.());
  });
  ws.on('message', onMessage);

  const removePending = (entry: PendingMessage): void => {
    const idx = pending.indexOf(entry);
    if (idx >= 0) {
      pending.splice(idx, 1);
    }
    clearTimeout(entry.timer);
  };

  let closeStarted = false;
  const socket: OpenSocket = {
    url,
    ws,
    opened: () => opened,
    async close(): Promise<void> {
      if (closeStarted) return;
      closeStarted = true;
      const captured: Error[] = [];
      // Phase 1: graceful close. Capture ws.close() exceptions but
      // continue; the actual close is observed via the 'close' event.
      if (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING) {
        try {
          ws.close();
        } catch (err) {
          captured.push(err instanceof Error ? err : new Error(String(err)));
        }
      }
      try {
        await waitForClosed(ws, CloseTimeoutMs);
      } catch (err) {
        captured.push(err instanceof Error ? err : new Error(String(err)));
      }
      // Phase 2: terminate fallback if the graceful close never
      // reached CLOSED within the deadline.
      if (ws.readyState !== WebSocket.CLOSED) {
        try {
          ws.terminate();
        } catch (err) {
          captured.push(err instanceof Error ? err : new Error(String(err)));
        }
        try {
          await waitForClosed(ws, TerminateTimeoutMs);
        } catch (err) {
          captured.push(err instanceof Error ? err : new Error(String(err)));
        }
      }
      // Phase 3: settle every pending waiter before removing
      // listeners so an in-flight awaiter cannot observe a half-
      // cleared internal queue.
      settleAllPending(new Error(`socket closed url=${url}`));
      ws.removeAllListeners();
      messageQueue.length = 0;
      errorQueue.length = 0;
      await delay(SettleDelayMs);
      if (captured.length > 0) {
        throw new AggregateError(
          captured,
          `openApiOnlySocket close reported ${captured.length} cleanup error(s) url=${url}`,
        );
      }
      if (ws.readyState !== WebSocket.CLOSED) {
        throw new Error(
          `openApiOnlySocket close: socket did not reach CLOSED within the hard budget url=${url}`,
        );
      }
    },
    nextMessage(timeoutMs: number): Promise<unknown> {
      if (messageQueue.length > 0) {
        return Promise.resolve(messageQueue.shift());
      }
      const entry: PendingMessage = {
        resolve: () => undefined,
        reject: () => undefined,
      };
      const promise = new Promise<unknown>((resolve, reject) => {
        entry.resolve = resolve;
        entry.reject = reject;
        entry.timer = setTimeout(() => {
          removePending(entry);
          reject(new Error(`WebSocket message timeout url=${url}`));
        }, timeoutMs);
      });
      if (NextMessageNoopPreMark) {
        promise.catch(() => undefined);
      }
      pending.push(entry);
      return promise;
    },
    errors(): Promise<unknown> {
      const queued = errorQueue.shift();
      if (queued) return Promise.resolve(queued);
      return new Promise<unknown>((resolve) => {
        let settled = false;
        const cleanup = () => {
          if (settled) return;
          settled = true;
          clearTimeout(timer);
          ws.off('error', onErrorOnce);
          ws.off('close', onCloseOnce);
        };
        const onErrorOnce = (err: Error) => {
          cleanup();
          resolve(err);
        };
        const onCloseOnce = () => {
          cleanup();
          resolve('closed');
        };
        const timer = setTimeout(() => {
          cleanup();
          resolve(undefined);
        }, ErrorsHardBoundMs);
        ws.on('error', onErrorOnce);
        ws.on('close', onCloseOnce);
      });
    },
  };

  try {
    await opened;
  } catch (err) {
    let closeErr: Error | undefined;
    try {
      await socket.close();
    } catch (e) {
      closeErr = e instanceof Error ? e : new Error(String(e));
    }
    const primary = err instanceof Error ? err : new Error(String(err));
    if (closeErr) {
      throw new AggregateError(
        [primary, closeErr],
        `openApiOnlySocket failed url=${url}`,
      );
    }
    throw primary;
  }
  return socket;
}

export async function openConcurrentSockets(
  urls: string[],
  timeoutMs = 5_000,
): Promise<OpenSocket[]> {
  const tasks = urls.map((url) => openApiOnlySocket(url, timeoutMs));
  const results = await Promise.allSettled(tasks);
  const succeeded: OpenSocket[] = [];
  const openFailures: Error[] = [];
  for (const r of results) {
    if (r.status === 'fulfilled') {
      succeeded.push(r.value);
    } else {
      openFailures.push(r.reason instanceof Error ? r.reason : new Error(String(r.reason)));
    }
  }
  if (openFailures.length > 0) {
    const closeResults = await Promise.allSettled(succeeded.map((s) => s.close()));
    const closeFailures: Error[] = [];
    for (const r of closeResults) {
      if (r.status === 'rejected') {
        closeFailures.push(r.reason instanceof Error ? r.reason : new Error(String(r.reason)));
      }
    }
    if (closeFailures.length > 0) {
      throw new AggregateError(
        [...openFailures, ...closeFailures],
        'openConcurrentSockets failed',
      );
    }
    throw new AggregateError(openFailures, 'openConcurrentSockets failed');
  }
  return succeeded;
}
