import path from "node:path";
import { expect, test } from "@playwright/test";
import { startMockedProxy } from "../fixtures/mocked_proxy";

test("v0007.5-v0007.7 use resolved teams and memory-only one-team credentials", async ({
  page,
}) => {
  const proxy = await startMockedProxy(
    [
      { team_id: "team-alpha", team_name: "Alpha Team", archived_at: null },
      {
        team_id: "team-beta",
        team_name: "Beta Team",
        archived_at: "2026-08-27T00:00:00Z",
      },
    ],
    path.resolve(__dirname, "../../../svc/web-ui/web/index.html"),
  );
  const browserRequests: Array<{
    url: string;
    method: string;
    headers: Record<string, string>;
    body: string | null;
  }> = [];
  page.on("request", (request) =>
    browserRequests.push({
      url: request.url(),
      method: request.method(),
      headers: request.headers(),
      body: request.postData(),
    }),
  );
  try {
    await page.goto(proxy.baseUrl);
    const selector = page.getByRole("combobox", { name: "Team", exact: true });
    await expect(selector.locator("option")).toHaveText([
      "Alpha Team",
      "Beta Team (archived)",
    ]);
    await expect(
      page.getByRole("button", {
        name: /create team|archive team|add member/i,
      }),
    ).toHaveCount(0);

    await selector.selectOption("team-beta");
    await expect(page.getByTestId("selected-team-id")).toHaveText("team-beta");
    await expect
      .poll(() =>
        browserRequests.some(
          (request) =>
            request.method === "POST" &&
            new URL(request.url).pathname === "/auth/v1/token" &&
            request.body === JSON.stringify({ team_id: "team-beta" }),
        ),
      )
      .toBe(true);
    await expect
      .poll(() =>
        browserRequests.some(
          (request) =>
            request.headers.authorization === "Bearer working-team-beta",
        ),
      )
      .toBe(true);
    expect(
      browserRequests.every(
        (request) =>
          !Object.keys(request.headers).some((name) =>
            name.toLowerCase().startsWith("x-flowai-"),
          ),
      ),
    ).toBe(true);
    const expectedProtocol = `flowai.bearer.${Buffer.from("working-team-beta").toString("base64url")}`;
    await expect
      .poll(() =>
        proxy.upgradeHeaders.some(
          (headers) => headers["sec-websocket-protocol"] === expectedProtocol,
        ),
      )
      .toBe(true);
    const protocols = String(
      proxy.upgradeHeaders.at(-1)?.["sec-websocket-protocol"] ?? "",
    );
    expect(protocols).toBe(expectedProtocol);
    expect(proxy.upgradeHeaders.at(-1)?.["x-flowai-team-id"]).toBeUndefined();

    await page.reload();
    await expect
      .poll(
        () =>
          browserRequests.filter(
            (request) =>
              request.method === "POST" &&
              new URL(request.url).pathname === "/auth/v1/token",
          ).length,
      )
      .toBeGreaterThan(2);
  } finally {
    await proxy.close();
  }
});
