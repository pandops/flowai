import { expect, test } from "@playwright/test";
import WebSocket from "ws";
import { startAuthenticatedGateway } from "../fixtures/authenticated_gateway.js";

test("v0007.15 v0007.26 authenticate, isolate, expire, and renew WebSockets", async ({
  page,
}) => {
  const gateway = await startAuthenticatedGateway(page, {
    API_GATEWAY_WORKING_TOKEN_TTL: "3s",
  });
  try {
    const missing = await rejectedUpgrade(gateway.baseURL);
    expect(missing).toBe(401);
    expect(gateway.downstreamRequests).toHaveLength(0);

    const issued = await gateway.issue("team-alpha");
    const token = ((await issued.json()) as { access_token: string })
      .access_token;
    const invalid = await rejectedUpgrade(
      gateway.baseURL,
      corruptSignature(token),
    );
    expect(invalid).toBe(401);
    expect(gateway.downstreamRequests).toHaveLength(0);

    const protocol = `flowai.bearer.${Buffer.from(token).toString("base64url")}`;
    const socket = await acceptedUpgrade(gateway.baseURL, protocol, {
      "x-flowai-operator-id": "operator-2",
      "x-flowai-team-id": "team-beta",
      "x-flowai-team-name": "Forged Beta",
      "x-flowai-request-id": "forged-request",
    });
    expect(socket.protocol).toBe(protocol);
    const frames = await receiveFrames(socket, 2);
    expect(frames).toEqual([
      { frame_id: "team-alpha-replay", team_id: "team-alpha", kind: "replay" },
      { frame_id: "team-alpha-live", team_id: "team-alpha", kind: "live" },
    ]);
    const downstream = gateway.downstreamRequests[0];
    expect(downstream.headers["x-flowai-team-id"]).toBe("team-alpha");
    expect(downstream.headers["x-flowai-team-name"]).toBe("Alpha Team");
    expect(downstream.headers["x-flowai-operator-id"]).not.toBe("operator-2");
    expect(downstream.headers["x-flowai-request-id"]).not.toBe(
      "forged-request",
    );
    expect(downstream.headers.authorization).toBeUndefined();
    expect(downstream.headers.cookie).toBeUndefined();
    expect(downstream.headers["sec-websocket-protocol"]).toBeUndefined();

    const closed = await closeEvent(socket);
    expect(closed).toEqual({ code: 4401, reason: "working_token_expired" });
    expect(await rejectedUpgrade(gateway.baseURL, token)).toBe(401);
    expect(gateway.downstreamRequests).toHaveLength(1);

    const renewedResponse = await gateway.issue("team-alpha");
    const renewed = ((await renewedResponse.json()) as { access_token: string })
      .access_token;
    expect(renewed).not.toBe(token);
    const reconnected = await acceptedUpgrade(
      gateway.baseURL,
      undefined,
      {},
      renewed,
    );
    expect(await receiveFrames(reconnected, 2)).toEqual(frames);
    reconnected.close();
    expect(gateway.downstreamRequests).toHaveLength(2);

    const logout = await fetch(`${gateway.baseURL}/auth/v1/session`, {
      method: "DELETE",
      headers: { cookie: `flowai_session=${gateway.sessionID}` },
    });
    expect(logout.status).toBe(204);
    expect(await rejectedUpgrade(gateway.baseURL, renewed)).toBe(403);
    expect(gateway.downstreamRequests).toHaveLength(2);
  } finally {
    await gateway.close();
  }
});

function websocketURL(baseURL: string): string {
  return `${baseURL.replace(/^http/, "ws")}/ui/v1/stream?after=cursor`;
}

function corruptSignature(token: string): string {
  const parts = token.split(".");
  if (parts.length !== 3) throw new Error("working token is not JWT");
  const signature = parts[2];
  const index = Math.floor(signature.length / 2);
  const replacement = signature[index] === "A" ? "B" : "A";
  parts[2] =
    signature.slice(0, index) + replacement + signature.slice(index + 1);
  return parts.join(".");
}

async function rejectedUpgrade(
  baseURL: string,
  token?: string,
): Promise<number> {
  return new Promise((resolve, reject) => {
    const socket = new WebSocket(websocketURL(baseURL), {
      headers: token ? { authorization: `Bearer ${token}` } : {},
    });
    socket.once("unexpected-response", (_request, response) => {
      resolve(response.statusCode ?? 0);
      response.destroy();
    });
    socket.once("open", () => {
      socket.terminate();
      reject(new Error("upgrade unexpectedly succeeded"));
    });
    socket.once("error", () => {});
  });
}

async function acceptedUpgrade(
  baseURL: string,
  protocol?: string,
  headers: Record<string, string> = {},
  bearer?: string,
): Promise<WebSocket> {
  return new Promise((resolve, reject) => {
    const socket = new WebSocket(
      websocketURL(baseURL),
      protocol ? [protocol] : undefined,
      {
        headers: {
          ...headers,
          ...(bearer ? { authorization: `Bearer ${bearer}` } : {}),
        },
      },
    );
    socket.once("open", () => resolve(socket));
    socket.once("error", reject);
    socket.once("unexpected-response", (_request, response) =>
      reject(new Error(`upgrade status ${response.statusCode}`)),
    );
  });
}

async function receiveFrames(
  socket: WebSocket,
  count: number,
): Promise<Array<Record<string, string>>> {
  return new Promise((resolve, reject) => {
    const frames: Array<Record<string, string>> = [];
    const timeout = setTimeout(
      () => reject(new Error(`received ${frames.length}/${count} frames`)),
      2_000,
    );
    socket.on("message", (data) => {
      frames.push(JSON.parse(data.toString()) as Record<string, string>);
      if (frames.length === count) {
        clearTimeout(timeout);
        resolve(frames);
      }
    });
    socket.once("error", reject);
  });
}

async function closeEvent(
  socket: WebSocket,
): Promise<{ code: number; reason: string }> {
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(
      () => reject(new Error("socket did not expire")),
      5_000,
    );
    socket.once("close", (code, reason) => {
      clearTimeout(timeout);
      resolve({ code, reason: reason.toString() });
    });
    socket.once("error", reject);
  });
}
