import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import path from "node:path";
import { test, expect } from "@playwright/test";
import { WebSocketServer, type WebSocket } from "ws";
import { startMockedProxy } from "../fixtures/mocked_proxy";

test("v0006.10 replaces the selected-team live subscription and ignores late frames", async ({
  page,
}) => {
  const server = createServer((_, response) => {
    response
      .writeHead(200, { "content-type": "application/json" })
      .end(JSON.stringify({ counts: {}, buckets: [] }));
  });
  const sockets = new Map<string, WebSocket>();
  const websocketServer = new WebSocketServer({ server });
  websocketServer.on("connection", (socket, request) => {
    sockets.set(request.url ?? "", socket);
    socket.on("close", () => sockets.delete(request.url ?? ""));
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address() as AddressInfo;
  const upstream = `http://127.0.0.1:${address.port}`;
  const webEntry = path.resolve(
    __dirname,
    "../../../svc/web-ui/web/index.html",
  );
  const proxy = await startMockedProxy(
    [
      { team_id: "alpha", team_name: "Alpha" },
      { team_id: "beta", team_name: "Beta" },
    ],
    webEntry,
    upstream,
  );

  try {
    await page.goto(proxy.baseUrl);
    const socketFor = (teamID: string) =>
      [...sockets.entries()].find(([url]) =>
        url.startsWith(`/ui/v1/teams/${teamID}/stream?`),
      )?.[1];
    await expect.poll(() => socketFor("alpha")?.readyState).toBe(1);
    const alpha = socketFor("alpha");

    await page.getByLabel("Team", { exact: true }).selectOption("beta");
    await expect.poll(() => socketFor("beta")?.readyState).toBe(1);
    await expect.poll(() => alpha?.readyState).toBe(3);

    socketFor("beta")?.send(
      JSON.stringify({ team_id: "beta", text: "current-beta" }),
    );
    await expect(page.getByTestId("live-frame")).toContainText("current-beta");
    await expect(page.getByTestId("live-frame")).not.toContainText(
      "late-alpha",
    );
  } finally {
    await proxy.close();
    for (const socket of websocketServer.clients) socket.terminate();
    server.close();
    server.closeAllConnections();
  }
});
