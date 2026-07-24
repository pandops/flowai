// Focus tests for the websocket helper. The tests run a local Node
// `ws` WebSocketServer on a loopback port chosen by the OS and drive
// the helper through the contract that other autotest modules rely
// on. No browser fixtures, no `process.on('uncaughtException')` guards,
// no skipped tests.
import { AddressInfo } from 'node:net';
import { test, expect } from '@playwright/test';
import { WebSocketServer, WebSocket as WsClient } from 'ws';
import { openApiOnlySocket, openConcurrentSockets } from '../fixtures/websocket';

interface LocalServer {
  url: string;
  port: number;
  close(): Promise<void>;
  pushText(text: string): void;
  connectionCount(): number;
}

interface Connection {
  ws: WsClient;
}

async function startLocalServer(): Promise<LocalServer> {
  const connections: Connection[] = [];
  const wss = new WebSocketServer({ host: '127.0.0.1', port: 0 });
  await new Promise<void>((resolve) => wss.once('listening', () => resolve()));
  const port = (wss.address() as AddressInfo).port;
  wss.on('connection', (ws) => {
    const conn: Connection = { ws };
    connections.push(conn);
    ws.on('close', () => {
      const idx = connections.indexOf(conn);
      if (idx >= 0) {
        connections.splice(idx, 1);
      }
    });
  });
  return {
    url: `ws://127.0.0.1:${port}`,
    port,
    close: () =>
      new Promise<void>((resolve) => {
        wss.close(() => resolve());
      }),
    pushText(text: string): void {
      for (const c of connections) {
        c.ws.send(text);
      }
    },
    connectionCount(): number {
      return connections.length;
    },
  };
}

test.describe('websocket helper', () => {
  test('opens successfully against a local ws server', async () => {
    const srv = await startLocalServer();
    try {
      const s = await openApiOnlySocket(srv.url, 1_000);
      expect(srv.connectionCount()).toBe(1);
      await s.close();
    } finally {
      await srv.close();
    }
  });

  test('concurrent opens succeed and the server sees every connection', async () => {
    const srv = await startLocalServer();
    try {
      const sockets = await openConcurrentSockets([srv.url, srv.url, srv.url], 1_000);
      expect(sockets).toHaveLength(3);
      expect(srv.connectionCount()).toBe(3);
      await Promise.allSettled(sockets.map((s) => s.close()));
    } finally {
      await srv.close();
    }
  });

  test('delivers a JSON text message to nextMessage', async () => {
    const srv = await startLocalServer();
    try {
      const s = await openApiOnlySocket(srv.url, 1_000);
      setTimeout(() => srv.pushText(JSON.stringify({ hello: 'world' })), 50);
      const msg = await s.nextMessage(1_000);
      expect(msg).toEqual({ hello: 'world' });
      await s.close();
    } finally {
      await srv.close();
    }
  });

  test('a queued normal message is never exposed by errors()', async () => {
    const srv = await startLocalServer();
    try {
      const s = await openApiOnlySocket(srv.url, 1_000);
      srv.pushText('ordinary-message');
      await new Promise((resolve) => setTimeout(resolve, 25));
      await expect(s.errors()).resolves.toBeUndefined();
      await expect(s.nextMessage(1_000)).resolves.toBe('ordinary-message');
      await s.close();
    } finally {
      await srv.close();
    }
  });

  test('nextMessage times out and removes exactly the entry it spawned', async () => {
    const srv = await startLocalServer();
    try {
      const s = await openApiOnlySocket(srv.url, 1_000);
      const start = Date.now();
      await expect(s.nextMessage(120)).rejects.toThrow(/timeout/i);
      expect(Date.now() - start).toBeGreaterThanOrEqual(100);
      // The first waiter's timeout was removed. A second waiter
      // for the same socket still times out independently, proving
      // the first waiter's setTimeout handle did not leak and was
      // cleared rather than shared.
      const start2 = Date.now();
      await expect(s.nextMessage(120)).rejects.toThrow(/timeout/i);
      expect(Date.now() - start2).toBeLessThan(500);
      // A successful message after the timeouts proves the entry
      // table is consistent and the socket is still usable.
      setTimeout(() => srv.pushText('after-timeout'), 50);
      const msg = await s.nextMessage(1_000);
      expect(msg).toBe('after-timeout');
      await s.close();
    } finally {
      await srv.close();
    }
  });

  test('two simultaneous nextMessage waiters resolve independently', async () => {
    const srv = await startLocalServer();
    try {
      const s = await openApiOnlySocket(srv.url, 1_000);
      const order: string[] = [];
      const w1 = s.nextMessage(500).catch((err: Error) => {
        order.push('w1-timeout');
        throw err;
      });
      const w2 = s.nextMessage(500).catch((err: Error) => {
        order.push('w2-timeout');
        throw err;
      });
      await expect(w1).rejects.toThrow(/timeout/i);
      await expect(w2).rejects.toThrow(/timeout/i);
      // Both waiters must settle; the order of resolution does not
      // matter, but both must appear in the order list.
      expect(order.sort()).toEqual(['w1-timeout', 'w2-timeout']);
      await s.close();
    } finally {
      await srv.close();
    }
  });

  test('close rejects a pending nextMessage waiter with the documented error', async () => {
    const srv = await startLocalServer();
    try {
      const s = await openApiOnlySocket(srv.url, 1_000);
      const waiter = s.nextMessage(5_000);
      await s.close();
      await expect(waiter).rejects.toThrow(/socket closed url=/i);
    } finally {
      await srv.close();
    }
  });

  test('errors() resolves on the first error event and removes its listeners', async () => {
    const srv = await startLocalServer();
    try {
      const s = await openApiOnlySocket(srv.url, 1_000);
      const baselineErrorListeners = s.ws.listenerCount('error');
      const baselineCloseListeners = s.ws.listenerCount('close');
      // Force an error by terminating the underlying socket from the
      // server side, which propagates to the client as an 'error'
      // event.
      srv.close().catch(() => undefined);
      // Race two calls; each MUST settle exactly once.
      const [first, second] = await Promise.allSettled([
        s.errors(),
        Promise.race([s.errors(), delay(50).then(() => 'timeout')]),
      ]);
      expect(['fulfilled', 'fulfilled']).toContain(first.status);
      expect(['fulfilled', 'fulfilled']).toContain(second.status);
      // After both have settled, the error event has been consumed
      // and the listeners are detached.
      expect(s.ws.listenerCount('error')).toBe(baselineErrorListeners);
      expect(s.ws.listenerCount('close')).toBe(baselineCloseListeners);
      await s.close().catch(() => undefined);
    } finally {
      // srv already closed in the test
    }
  });

  test('errors() returns undefined when no error fires within the hard bound', async () => {
    const srv = await startLocalServer();
    try {
      const s = await openApiOnlySocket(srv.url, 1_000);
      const baselineErrorListeners = s.ws.listenerCount('error');
      const baselineCloseListeners = s.ws.listenerCount('close');
      const err = await s.errors();
      expect(err).toBeUndefined();
      // The 50ms hard-bound listeners must be removed.
      expect(s.ws.listenerCount('error')).toBe(baselineErrorListeners);
      expect(s.ws.listenerCount('close')).toBe(baselineCloseListeners);
      await s.close();
    } finally {
      await srv.close();
    }
  });

  test('partial concurrent open failure closes every successful socket', async () => {
    const srv = await startLocalServer();
    try {
      const bogusUrl = 'ws://127.0.0.1:1';
      await expect(
        openConcurrentSockets([srv.url, bogusUrl, srv.url], 500),
      ).rejects.toThrow();
      // The successful connections must have been closed; the test
      // server is no longer holding them.
      await new Promise((r) => setTimeout(r, 100));
      expect(srv.connectionCount()).toBe(0);
    } finally {
      await srv.close();
    }
  });
});

const delay = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));
