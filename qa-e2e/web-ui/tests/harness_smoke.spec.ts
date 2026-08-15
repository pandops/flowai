import { expect, test } from "@playwright/test";
import path from "node:path";

import { startMockedProxy, type MockedProxy } from "../fixtures/mocked_proxy";

let proxy: MockedProxy;

test.beforeAll(async () => {
  proxy = await startMockedProxy(
    [
      { team_id: "team-alpha", team_name: "Альфа" },
      { team_id: "team-beta", team_name: "Бета" },
    ],
    path.resolve(__dirname, "../../../svc/web-ui/web/index.html"),
  );
});

test.afterAll(async () => {
  await proxy.close();
});

test("mocked proxy starts and preserves the supplied team list", async ({
  request,
}) => {
  expect(proxy.webUIImageID).toMatch(/^[a-z0-9:.-]*[a-f0-9]{12,}$/);
  const index = await request.get(`${proxy.baseUrl}/`);
  expect(index.status()).toBe(200);
  await expect(index.text()).resolves.toContain("FlowAI v2");

  const health = await request.get(`${proxy.baseUrl}/health`);
  expect(health.status()).toBe(200);
  await expect(health.json()).resolves.toEqual({ status: "ok" });

  const teams = await request.get(`${proxy.baseUrl}/ui/v1/teams`);
  expect(teams.status()).toBe(200);
  await expect(teams.json()).resolves.toEqual({
    items: [
      { team_id: "team-alpha", team_name: "Альфа" },
      { team_id: "team-beta", team_name: "Бета" },
    ],
  });
  expect(proxy.requests).toEqual(["/", "/health", "/ui/v1/teams"]);
});
