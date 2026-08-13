// WebSocket close/terminate failure-injection tests. The helper must:
//   - capture ws.close() and ws.terminate() exceptions
//   - await the actual CLOSED state
//   - throw an AggregateError if cleanup fails
//   - eventually settle pending waiters and remove listeners
import { test, expect } from "@playwright/test";
import http from "node:http";
import { AddressInfo } from "node:net";
import { createHash } from "node:crypto";
import { WebSocketServer, WebSocket as WsClient } from "ws";
import { openApiOnlySocket } from "../fixtures/websocket";

function wsAccept(key: string): string {
  return createHash("sha1")
    .update(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11")
    .digest("base64");
}

interface LocalServer {
  url: string;
  port: number;
  close(): Promise<void>;
}

async function startLocalServer(): Promise<LocalServer> {
  const wss = new WebSocketServer({ host: "127.0.0.1", port: 0 });
  await new Promise<void>((resolve) => wss.once("listening", () => resolve()));
  const port = (wss.address() as AddressInfo).port;
  return {
    url: `ws://127.0.0.1:${port}`,
    port,
    close: () =>
      new Promise<void>((resolve) => {
        wss.close(() => resolve());
      }),
  };
}

test.describe("WebSocket close/terminate failure injection", () => {
  test("close() captures ws.close() exception and throws AggregateError; pending waiter is settled", async () => {
    const srv = await startLocalServer();
    try {
      const s = await openApiOnlySocket(srv.url, 2_000);
      const originalClose = s.ws.close.bind(s.ws);
      s.ws.close = () => {
        throw new Error("injected ws.close failure");
      };
      const waiter = s.nextMessage(5_000);
      let captured: unknown;
      try {
        await s.close();
      } catch (err) {
        captured = err;
      }
      expect(
        captured,
        "close() must reject when ws.close() throws",
      ).toBeDefined();
      const err = captured as Error;
      expect(err.name).toBe("AggregateError");
      const agg = err as AggregateError;
      expect(Array.isArray(agg.errors)).toBe(true);
      const messages = agg.errors.map((e) => e.message);
      expect(messages.some((m) => /injected ws.close failure/.test(m))).toBe(
        true,
      );
      try {
        await waiter;
        throw new Error("waiter should have rejected");
      } catch (e) {
        expect((e as Error).message).toMatch(/socket closed/);
      }
      s.ws.close = originalClose;
    } finally {
      await srv.close();
    }
  });

  test("close() captures ws.terminate() exception when graceful close fails", async () => {
    const srv = await startLocalServer();
    try {
      const s = await openApiOnlySocket(srv.url, 2_000);
      Object.defineProperty(s.ws, "readyState", {
        configurable: true,
        get: () => 1,
      });
      s.ws.terminate = () => {
        throw new Error("injected ws.terminate failure");
      };
      const waiter = s.nextMessage(5_000);
      let captured: unknown;
      try {
        await s.close();
      } catch (err) {
        captured = err;
      }
      expect(captured).toBeDefined();
      const err = captured as Error;
      expect(err.name).toBe("AggregateError");
      const agg = err as AggregateError;
      const messages = agg.errors.map((e) => e.message);
      expect(
        messages.some((m) => /injected ws.terminate failure/.test(m)),
      ).toBe(true);
      try {
        await waiter;
      } catch {
        // expected
      }
    } finally {
      await srv.close();
    }
  });
});
