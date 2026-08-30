import { createServer, type IncomingHttpHeaders } from "node:http";
import type { AddressInfo } from "node:net";
import { WebSocket, WebSocketServer } from "ws";
import { startWebUIContainer } from "../../web-ui/fixtures/web_ui_container.js";

export type ProductionIngress = {
  baseURL: string;
  webUIImageID: string;
  setGatewayOrigin(origin: string): void;
  close(): Promise<void>;
};

export async function startProductionIngress(): Promise<ProductionIngress> {
  const webUI = await startWebUIContainer();
  let gatewayOrigin = "";
  const sockets = new Set<WebSocket>();
  const server = createServer(async (request, response) => {
    try {
      const pathname = new URL(request.url ?? "/", "http://ingress.invalid")
        .pathname;
      const origin =
        pathname === "/" || isStaticAsset(pathname)
          ? webUI.baseUrl
          : gatewayOrigin;
      if (!origin) {
        response.writeHead(503).end();
        return;
      }
      const chunks: Buffer[] = [];
      for await (const chunk of request)
        chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
      const upstream = await fetch(new URL(request.url ?? "/", origin), {
        method: request.method,
        headers: proxyHeaders(request.headers),
        body: chunks.length === 0 ? undefined : Buffer.concat(chunks),
        redirect: "manual",
      });
      response.statusCode = upstream.status;
      upstream.headers.forEach((value, name) =>
        response.setHeader(name, value),
      );
      response.end(Buffer.from(await upstream.arrayBuffer()));
    } catch (error) {
      response
        .writeHead(502, { "content-type": "text/plain" })
        .end(String(error));
    }
  });
  const upgrades = new WebSocketServer({ noServer: true });
  server.on("upgrade", (request, socket, head) => {
    if (
      !gatewayOrigin ||
      !new URL(
        request.url ?? "/",
        "http://ingress.invalid",
      ).pathname.startsWith("/ui/")
    ) {
      socket.write("HTTP/1.1 404 Not Found\r\nConnection: close\r\n\r\n");
      socket.destroy();
      return;
    }
    const target = new URL(request.url ?? "/", gatewayOrigin);
    target.protocol = target.protocol === "https:" ? "wss:" : "ws:";
    const protocols = String(request.headers["sec-websocket-protocol"] ?? "")
      .split(",")
      .map((value) => value.trim())
      .filter(Boolean);
    const upstream = new WebSocket(target, protocols);
    const fail = () => {
      socket.write("HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n");
      socket.destroy();
    };
    upstream.once("error", fail);
    upstream.once("open", () => {
      upstream.off("error", fail);
      upgrades.handleUpgrade(request, socket, head, (browser) => {
        sockets.add(browser);
        sockets.add(upstream);
        browser.on(
          "message",
          (data, binary) =>
            upstream.readyState === WebSocket.OPEN &&
            upstream.send(data, { binary }),
        );
        upstream.on(
          "message",
          (data, binary) =>
            browser.readyState === WebSocket.OPEN &&
            browser.send(data, { binary }),
        );
        browser.on("close", () => upstream.close());
        upstream.on("close", () => browser.close());
        browser.on("error", () => upstream.close());
        upstream.on("error", () => browser.close());
        browser.on("close", () => sockets.delete(browser));
        upstream.on("close", () => sockets.delete(upstream));
      });
    });
  });
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const baseURL = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  return {
    baseURL,
    webUIImageID: webUI.imageID,
    setGatewayOrigin(origin) {
      gatewayOrigin = origin;
    },
    close: async () => {
      for (const connection of sockets) connection.terminate();
      await new Promise<void>((resolve) => server.close(() => resolve()));
      await webUI.close();
    },
  };
}

function isStaticAsset(pathname: string): boolean {
  return /\.(?:css|js|png|svg|ico|woff2?)$/.test(pathname);
}

function proxyHeaders(input: IncomingHttpHeaders): Headers {
  const result = new Headers();
  for (const [name, value] of Object.entries(input)) {
    if (
      value === undefined ||
      ["host", "connection", "content-length"].includes(name)
    )
      continue;
    result.set(name, Array.isArray(value) ? value.join(", ") : value);
  }
  return result;
}
