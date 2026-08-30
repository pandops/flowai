import path from "node:path";

import { expect, test } from "@playwright/test";

import { startMockedProxy, type MockedProxy } from "../fixtures/mocked_proxy";

let proxy: MockedProxy;

test.beforeAll(async () => {
  proxy = await startMockedProxy(
    [
      { team_id: "team-alpha", team_name: "Alpha" },
      { team_id: "team-beta", team_name: "Beta" },
    ],
    path.resolve(__dirname, "../../../svc/web-ui/web/index.html"),
  );
});

test.afterAll(async () => {
  await proxy.close();
});

test("v0006.1 selects one supplied team and replaces context on switch", async ({
  page,
}) => {
  await page.goto(proxy.baseUrl);

  const selector = page.getByRole("combobox", { name: "Team", exact: true });
  await expect(selector).toHaveValue("team-alpha");
  await expect(selector.locator("option")).toHaveText(["Alpha", "Beta"]);

  await selector.selectOption("team-beta");
  await expect(selector).toHaveValue("team-beta");
  await expect(page.getByTestId("selected-team-id")).toHaveText("team-beta");

  await expect
    .poll(() =>
      proxy.requests.some((request) => request.includes("/teams/team-beta/")),
    )
    .toBe(true);
});

test("v0006.2 browser uses only the configured mocked proxy origin", async ({
  page,
}) => {
  const destinations = new Set<string>();
  page.on("request", (request) =>
    destinations.add(new URL(request.url()).origin),
  );

  await page.goto(proxy.baseUrl);
  await expect(
    page.getByRole("combobox", { name: "Team", exact: true }),
  ).toBeVisible();
  expect([...destinations]).toEqual([new URL(proxy.baseUrl).origin]);
});

test("v0006.3 mutations use the currently selected team id", async ({
  page,
}) => {
  const mutations: Array<{ method: string; path: string }> = [];
  page.on("request", (request) => {
    if (["POST", "PUT", "PATCH", "DELETE"].includes(request.method())) {
      mutations.push({
        method: request.method(),
        path: new URL(request.url()).pathname,
      });
    }
  });
  await page.goto(proxy.baseUrl);
  await page
    .getByRole("combobox", { name: "Team", exact: true })
    .selectOption("team-beta");
  await page.getByRole("button", { name: "Launch parameters" }).click();
  await page.getByRole("button", { name: "Add variable" }).click();
  await page.getByLabel("Key name").fill("FLOWAI_E2E_VISIBLE");
  await page.getByLabel("Value", { exact: true }).fill("visible-value");
  await page.getByRole("button", { name: "Save" }).click();

  await expect
    .poll(() =>
      mutations.some(
        ({ path }) =>
          path === "/ui/v1/teams/team-beta/launch-parameters" ||
          path.startsWith("/ui/v1/teams/team-beta/launch-parameters/"),
      ),
    )
    .toBe(true);
  expect(
    mutations.some(({ path }) =>
      path.startsWith("/ui/v1/teams/team-alpha/launch-parameters"),
    ),
  ).toBe(false);
});

test("v0006.4 v0007.7 sends selected team with bearer but without trusted auth context", async ({
  page,
}) => {
  const headers: Record<string, string>[] = [];
  page.on("request", (request) => headers.push(request.headers()));

  await page.goto(proxy.baseUrl);
  await page
    .getByRole("combobox", { name: "Team", exact: true })
    .selectOption("team-beta");

  await expect
    .poll(() =>
      headers.some(
        (value) => value.authorization === "Bearer working-team-beta",
      ),
    )
    .toBe(true);
  expect(headers.some((value) => "x-flowai-operator-id" in value)).toBe(false);
  expect(headers.some((value) => "x-flowai-team-id" in value)).toBe(false);
});

test("v0006.5 refresh rebuilds the selected-team view from the adapter", async ({
  page,
}) => {
  await page.goto(proxy.baseUrl);
  await page
    .getByRole("combobox", { name: "Team", exact: true })
    .selectOption("team-beta");
  await expect(page.getByTestId("selected-team-id")).toHaveText("team-beta");

  await page.reload();
  await expect(
    page.getByRole("combobox", { name: "Team", exact: true }),
  ).toHaveValue("team-beta");
  await expect(page.getByTestId("selected-team-id")).toHaveText("team-beta");
});
