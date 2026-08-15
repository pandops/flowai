import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { WebSocket, WebSocketServer } from "ws";
import { startWebUIContainer } from "./web_ui_container";

export type TeamOption = Readonly<{
  team_id: string;
  team_name: string;
}>;

export type MockedProxy = Readonly<{
  baseUrl: string;
  requests: readonly string[];
  webUIImageID?: string;
  close: () => Promise<void>;
}>;

export async function startMockedProxy(
  teams: readonly TeamOption[],
  webEntry?: string,
  stateRegistryOrigin?: string,
): Promise<MockedProxy> {
  validateTeams(teams);
  const webUI = webEntry ? await startWebUIContainer() : undefined;
  const requests: string[] = [];
  const sockets = new Set<WebSocket>();
  const server = createServer(async (request, response) => {
    const path = request.url ?? "/";
    requests.push(path);
    if (
      request.method === "GET" &&
      new URL(path, "http://mocked-proxy").pathname === "/" &&
      webUI
    ) {
      const upstream = await fetch(`${webUI.baseUrl}/`);
      response.setHeader(
        "content-type",
        upstream.headers.get("content-type") ?? "text/html; charset=utf-8",
      );
      response
        .writeHead(upstream.status)
        .end(Buffer.from(await upstream.arrayBuffer()));
      return;
    }

    response.setHeader("content-type", "application/json; charset=utf-8");

    if (request.method === "GET" && path === "/health") {
      response.writeHead(200).end(JSON.stringify({ status: "ok" }));
      return;
    }
    if (request.method === "GET" && path === "/ui/v1/teams") {
      response.writeHead(200).end(JSON.stringify({ items: teams }));
      return;
    }
    if (stateRegistryOrigin && isAllowedUIRoute(path)) {
      const chunks: Buffer[] = [];
      for await (const chunk of request) {
        chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
      }
      const body = Buffer.concat(chunks);
      const upstream = await fetch(new URL(path, stateRegistryOrigin), {
        method: request.method,
        headers: forwardHeaders(request.headers),
        body: body.length > 0 ? body : undefined,
      });
      response.statusCode = upstream.status;
      upstream.headers.forEach((value, name) =>
        response.setHeader(name, value),
      );
      response.end(Buffer.from(await upstream.arrayBuffer()));
      return;
    }
    const dashboard = path.match(/^\/ui\/v1\/teams\/([^/]+)\/dashboard/);
    if (request.method === "GET" && dashboard) {
      response.writeHead(200).end(
        JSON.stringify({
          team_id: dashboard[1],
          period: "week",
          counts: {
            pending: 0,
            created: 0,
            running: 0,
            finished: 0,
            failed: 0,
          },
          buckets: [],
        }),
      );
      return;
    }
    if (
      request.method === "GET" &&
      /^\/ui\/v1\/teams\/[^/]+\/launch-parameters(?:\?.*)?$/.test(path)
    ) {
      response
        .writeHead(200)
        .end(JSON.stringify({ items: [], next_cursor: null }));
      return;
    }
    if (
      request.method === "GET" &&
      /^\/ui\/v1\/teams\/[^/]+\/task-types(?:\?.*)?$/.test(path)
    ) {
      response
        .writeHead(200)
        .end(JSON.stringify({ items: [], next_cursor: null }));
      return;
    }
    if (
      request.method === "POST" &&
      /^\/ui\/v1\/teams\/[^/]+\/launch-parameters$/.test(path)
    ) {
      response.writeHead(201).end(JSON.stringify({ status: "created" }));
      return;
    }
    response.writeHead(404).end(
      JSON.stringify({
        code: "route_unknown",
        message: `route is unavailable: ${path}`,
      }),
    );
  });

  const websocketServer = new WebSocketServer({ noServer: true });
  server.on("upgrade", (request, socket, head) => {
    const path = request.url ?? "/";
    if (
      !stateRegistryOrigin ||
      !/^\/ui\/v1\/teams\/[^/]+\/stream(?:\?.*)?$/.test(path)
    ) {
      socket.write("HTTP/1.1 404 Not Found\r\nConnection: close\r\n\r\n");
      socket.destroy();
      return;
    }
    requests.push(path);
    const target = new URL(path, stateRegistryOrigin);
    target.protocol = target.protocol === "https:" ? "wss:" : "ws:";
    const upstream = new WebSocket(target);
    const failUpgrade = () => {
      socket.write("HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n");
      socket.destroy();
    };
    upstream.once("error", failUpgrade);
    upstream.once("open", () => {
      upstream.off("error", failUpgrade);
      websocketServer.handleUpgrade(request, socket, head, (browser) => {
        sockets.add(browser);
        sockets.add(upstream);
        browser.on("message", (data, binary) => {
          if (upstream.readyState === WebSocket.OPEN)
            upstream.send(data, { binary });
        });
        upstream.on("message", (data, binary) => {
          if (browser.readyState === WebSocket.OPEN)
            browser.send(data, { binary });
        });
        browser.on("close", () => upstream.close());
        upstream.on("close", () => browser.close());
        browser.on("error", () => upstream.close());
        upstream.on("error", () => browser.close());
        browser.on("close", () => sockets.delete(browser));
        upstream.on("close", () => sockets.delete(upstream));
      });
    });
  });

  await listen(server);
  const address = server.address() as AddressInfo;
  return {
    baseUrl: `http://127.0.0.1:${address.port}`,
    requests,
    webUIImageID: webUI?.imageID,
    close: async () => {
      for (const socket of sockets) socket.terminate();
      websocketServer.close();
      server.close();
      server.closeAllConnections();
      await webUI?.close();
    },
  };
}

function validateTeams(teams: readonly TeamOption[]): void {
  const seen = new Set<string>();
  for (const [index, team] of teams.entries()) {
    if (
      !team ||
      typeof team.team_id !== "string" ||
      team.team_id.trim() === ""
    ) {
      throw new Error(`teams[${index}].team_id must be a non-empty string`);
    }
    if (typeof team.team_name !== "string" || team.team_name.trim() === "") {
      throw new Error(`teams[${index}].team_name must be a non-empty string`);
    }
    if (seen.has(team.team_id)) {
      throw new Error(`teams[${index}].team_id must be unique`);
    }
    seen.add(team.team_id);
  }
}

function isAllowedUIRoute(path: string): boolean {
  return /^\/ui\/v1\/teams\/[^/]+\/(?:dashboard|task-types|tasks(?:\/[^/?]+)?|executors(?:\/[^/?]+)?|launch-parameters(?:\/[^/?]+)?|audit)(?:[/?].*)?$/.test(
    path,
  );
}

function forwardHeaders(
  headers: import("node:http").IncomingHttpHeaders,
): Headers {
  const forwarded = new Headers();
  for (const [name, value] of Object.entries(headers)) {
    if (
      value === undefined ||
      [
        "host",
        "connection",
        "content-length",
        "authorization",
        "x-operator-id",
        "x-team-id",
      ].includes(name)
    ) {
      continue;
    }
    forwarded.set(name, Array.isArray(value) ? value.join(", ") : value);
  }
  return forwarded;
}

function listen(server: Server): Promise<void> {
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      server.off("error", reject);
      resolve();
    });
  });
}

function close(server: Server): Promise<void> {
  return new Promise((resolve, reject) => {
    server.close((error) => (error ? reject(error) : resolve()));
  });
}
