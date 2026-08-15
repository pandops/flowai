import {
  createServer,
  type IncomingMessage,
  type ServerResponse,
} from "node:http";
import type { AddressInfo } from "node:net";
import { test, expect } from "@playwright/test";
import { startMockedProxy } from "../fixtures/mocked_proxy";

test("v0006.6 rejects malformed supplied teams and accepts an explicit multi-team list", async ({
  request,
}) => {
  await expect(
    startMockedProxy([{ team_id: "", team_name: "Broken" }]),
  ).rejects.toThrow(/team_id/);

  const proxy = await startMockedProxy([
    { team_id: "alpha", team_name: "Alpha" },
    { team_id: "beta", team_name: "Beta" },
  ]);
  try {
    const response = await request.get(`${proxy.baseUrl}/ui/v1/teams`);
    expect(await response.json()).toEqual({
      items: [
        { team_id: "alpha", team_name: "Alpha" },
        { team_id: "beta", team_name: "Beta" },
      ],
    });
  } finally {
    await proxy.close();
  }
});

test("v0006.7 forwards only allowlisted UI adapter routes", async ({
  request,
}) => {
  const upstream = await startRecorder((_, response) => {
    response.writeHead(200, { "content-type": "application/json" }).end("{}");
  });
  const proxy = await startMockedProxy([], undefined, upstream.baseUrl);
  try {
    await request.get(`${proxy.baseUrl}/ui/v1/teams/alpha/dashboard`);
    for (const path of [
      "/admin/teams",
      "/v1/tasks",
      "/v1/executors",
      "/anything",
    ]) {
      expect((await request.get(`${proxy.baseUrl}${path}`)).status()).toBe(404);
    }
    expect(upstream.requests).toEqual(["/ui/v1/teams/alpha/dashboard"]);
  } finally {
    await proxy.close();
    await upstream.close();
  }
});

test("v0006.8 preserves the selected team id and adds no identity headers", async ({
  request,
}) => {
  const captures: Array<{ path: string; headers: IncomingMessage["headers"] }> =
    [];
  const upstream = await startRecorder((incoming, response) => {
    captures.push({ path: incoming.url ?? "", headers: incoming.headers });
    response.writeHead(200, { "content-type": "application/json" }).end("{}");
  });
  const proxy = await startMockedProxy(
    [
      { team_id: "alpha", team_name: "Alpha" },
      { team_id: "beta", team_name: "Beta" },
    ],
    undefined,
    upstream.baseUrl,
  );
  try {
    await request.get(`${proxy.baseUrl}/ui/v1/teams/beta/dashboard`);
    expect(captures[0]?.path).toBe("/ui/v1/teams/beta/dashboard");
    expect(captures[0]?.headers.authorization).toBeUndefined();
    expect(captures[0]?.headers["x-operator-id"]).toBeUndefined();
    expect(captures[0]?.headers["x-team-id"]).toBeUndefined();
  } finally {
    await proxy.close();
    await upstream.close();
  }
});

test("v0006.9 keeps cross-team not-found responses non-revealing", async ({
  request,
}) => {
  const upstream = await startRecorder((incoming, response) => {
    const status = incoming.url?.includes("/alpha/tasks/beta-task") ? 404 : 200;
    response
      .writeHead(status, { "content-type": "application/json" })
      .end(
        JSON.stringify(
          status === 404
            ? { code: "not_found", message: "resource not found" }
            : { ok: true },
        ),
      );
  });
  const proxy = await startMockedProxy([], undefined, upstream.baseUrl);
  try {
    const response = await request.get(
      `${proxy.baseUrl}/ui/v1/teams/alpha/tasks/beta-task`,
    );
    expect(response.status()).toBe(404);
    expect(await response.json()).toEqual({
      code: "not_found",
      message: "resource not found",
    });
  } finally {
    await proxy.close();
    await upstream.close();
  }
});

test("v0006.11 forwards State Registry status and body unchanged", async ({
  request,
}) => {
  const body = { code: "revision_conflict", current_revision: 17 };
  const upstream = await startRecorder((_, response) => {
    response
      .writeHead(409, {
        "content-type": "application/json",
        "x-registry": "yes",
      })
      .end(JSON.stringify(body));
  });
  const proxy = await startMockedProxy([], undefined, upstream.baseUrl);
  try {
    const response = await request.post(
      `${proxy.baseUrl}/ui/v1/teams/alpha/launch-parameters`,
    );
    expect(response.status()).toBe(409);
    expect(response.headers()["x-registry"]).toBe("yes");
    expect(await response.json()).toEqual(body);
  } finally {
    await proxy.close();
    await upstream.close();
  }
});

async function startRecorder(
  handler: (request: IncomingMessage, response: ServerResponse) => void,
): Promise<{
  baseUrl: string;
  requests: string[];
  close: () => Promise<void>;
}> {
  const requests: string[] = [];
  const server = createServer((request, response) => {
    requests.push(request.url ?? "/");
    handler(request, response);
  });
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address() as AddressInfo;
  return {
    baseUrl: `http://127.0.0.1:${address.port}`,
    requests,
    close: () =>
      new Promise((resolve, reject) =>
        server.close((error) => (error ? reject(error) : resolve())),
      ),
  };
}
