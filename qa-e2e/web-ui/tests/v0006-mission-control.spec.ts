import path from "node:path";
import { expect, test } from "@playwright/test";
import { startMockedProxy, type MockedProxy } from "../fixtures/mocked_proxy";
import {
  startMissionControlRegistry,
  type MissionControlRegistry,
} from "../fixtures/mission_control_registry";

let proxy: MockedProxy;
let registry: MissionControlRegistry;
test.beforeAll(async () => {
  registry = await startMissionControlRegistry();
  proxy = await startMockedProxy(
    [{ team_id: "team-alpha", team_name: "Alpha" }],
    path.resolve(__dirname, "../../../svc/web-ui/web/index.html"),
    registry.origin,
  );
});
test.afterAll(async () => {
  await proxy.close();
  await registry.close();
});

test("v0006.16 dashboard uses navigable complete calendar periods and subdued chart", async ({
  page,
}) => {
  await page.goto(proxy.baseUrl);
  await expect(page.getByRole("button", { name: "Add variable" })).toHaveCount(
    0,
  );
  await expect(page.locator(".stat")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Day" })).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Week", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Month", exact: true }),
  ).toBeVisible();
  await expect(page.locator("[aria-label='ISO week'] .month-label")).toHaveText(
    /^Week \d{2}, \d{4}$/,
  );
  await expect(page.locator(".bar")).toHaveCount(7);
  await expect(page.getByRole("button", { name: "Next week" })).toBeDisabled();
  const currentWeekURL = page.url();
  await page.getByRole("button", { name: "Previous week" }).click();
  await expect(page.getByRole("button", { name: "Next week" })).toBeEnabled();
  await expect(page).not.toHaveURL(currentWeekURL);
  await page.goBack();
  await expect(page).toHaveURL(currentWeekURL);
  const color = await page
    .locator(".bar")
    .first()
    .evaluate((element) => getComputedStyle(element).backgroundColor);
  expect(color).not.toBe("rgb(254, 230, 0)");
  const heights = await page
    .locator(".bar")
    .evaluateAll((elements) =>
      elements.map((element) => parseFloat(getComputedStyle(element).height)),
    );
  expect(Math.max(...heights)).toBeGreaterThan(Math.min(...heights));
  const activeDay = page
    .locator("[data-dashboard-date]:not(.zero):not(.future)")
    .first();
  const selectedDate = await activeDay.getAttribute("data-dashboard-date");
  await activeDay.click();
  await expect(page).toHaveURL(
    new RegExp(`view=history.*date=${selectedDate}`),
  );
  await expect(page.getByLabel("Date")).toHaveValue(selectedDate!);
  await expect
    .poll(() =>
      proxy.requests.some(
        (request) =>
          request.includes(`/tasks?`) &&
          request.includes(`date=${selectedDate}`),
      ),
    )
    .toBe(true);
  await expect(
    page.getByRole("button", { name: "task-alpha-running" }),
  ).toBeVisible();
});

test("v0006.17 task history filters, paginates and omits unused columns", async ({
  page,
}) => {
  await page.goto(`${proxy.baseUrl}?view=history`);
  await expect(
    page.locator("main .toolbar").getByLabel("Rows per page"),
  ).toHaveValue("10");
  await page
    .getByPlaceholder("Task ID, category, or executor")
    .fill("task-alpha-running");
  await expect(
    page.getByRole("button", { name: "task-alpha-running" }),
  ).toBeVisible();
  await expect(page.getByRole("columnheader")).toHaveText([
    "Task",
    "task_type_id",
    "Status",
    "Executor",
    "Time",
    "Duration",
  ]);
  await expect(
    page.getByRole("columnheader", {
      name: /team_id|cursor|request_id|scope_token/i,
    }),
  ).toHaveCount(0);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator(".cards .card").first()).toBeVisible();
  await expect(page.locator("table")).toBeHidden();
});

test("v0006.18 task detail shows metadata, env and executor navigation", async ({
  page,
}) => {
  await page.goto(proxy.baseUrl);
  await page.getByRole("button", { name: "History" }).click();
  await page
    .getByPlaceholder("Task ID, category, or executor")
    .fill("task-alpha-running");
  await page.getByRole("button", { name: "task-alpha-running" }).click();
  await expect(page.getByText("listener-alpha / external-3")).toBeVisible();
  await expect(page.getByText("FLOWAI_VISIBLE=fixture-value")).toBeVisible();
  await expect(page.getByText("FLOWAI_SECRET ••••••••")).toBeVisible();
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Task details" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "executor-alpha" }).click();
  await expect(
    page.getByRole("heading", { name: "Executor details" }),
  ).toBeVisible();
});

test("v0006.19 executor inventory and detail are read-only and minimally columned", async ({
  page,
}) => {
  await page.goto(`${proxy.baseUrl}?view=executors`);
  await expect(page.getByRole("columnheader")).toHaveText([
    "Executor",
    "Type",
    "Scope",
    "Tag",
    "Activity",
    "Available",
  ]);
  await page.getByRole("button", { name: "executor-alpha" }).click();
  await expect(page.getByText("1 used, 1 available")).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Executor events" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", {
      name: /зарегистрировать|удалить исполнитель|остановить/i,
    }),
  ).toHaveCount(0);
});

test("v0006.20 launch parameter UI accepts explicit env and secret keys", async ({
  page,
}) => {
  await page.goto(`${proxy.baseUrl}?view=parameters`);
  await page.getByRole("button", { name: "Edit FLOWAI_VISIBLE" }).click();
  await page
    .locator("#edit-env-dialog")
    .getByLabel("New value", { exact: true })
    .fill("updated-value");
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.getByText("FLOWAI_VISIBLE=updated-value")).toBeVisible();
  await page.getByRole("button", { name: "Add secret" }).first().click();
  await page
    .locator("#secret-dialog")
    .getByLabel("Secret key")
    .fill("NEW_SECRET_KEY");
  await page
    .locator("#secret-dialog")
    .getByLabel("New secret value")
    .fill("never-render-me");
  await page.getByRole("button", { name: "Save secret" }).click();
  await expect(page.getByText("NEW_SECRET_KEY ••••••••")).toBeVisible();
  await expect(page.getByText("never-render-me")).toHaveCount(0);
  await page
    .getByRole("button", { name: "Replace", exact: true })
    .first()
    .click();
  await page
    .locator("#replace-secret-dialog")
    .getByLabel("New secret value")
    .fill("replacement-never-render-me");
  await page
    .locator("#replace-secret-dialog")
    .getByRole("button", { name: "Replace" })
    .click();
  await page.getByRole("button", { name: "Versions" }).first().click();
  await expect(page.getByText("Version 2")).toBeVisible();
  await page.getByRole("button", { name: "Close" }).click();
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Delete" }).first().click();
  await expect(
    page.getByText("FLOWAI_SECRET ••••••••", { exact: true }),
  ).toHaveCount(0);
  await expect(page.getByText("replacement-never-render-me")).toHaveCount(0);
});

test("v0006.21 audit filters and renders only useful columns", async ({
  page,
}) => {
  await page.goto(`${proxy.baseUrl}?view=audit`);
  await expect(page.getByRole("columnheader")).toHaveText([
    "Time",
    "Action",
    "Actor",
    "Resource",
    "Outcome",
  ]);
  await page.getByLabel("Action, actor, or resource").fill("operator-alpha");
  await expect(
    page.getByRole("cell", { name: "operator-alpha" }).first(),
  ).toBeVisible();
  await expect(
    page.getByRole("columnheader", { name: /request_id|audit_id|team_id/i }),
  ).toHaveCount(0);
});

test("v0006.22 keyboard and responsive states remain operable", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(proxy.baseUrl);
  await page.keyboard.press("Tab");
  await expect(page.getByRole("button", { name: "Dashboard" })).toBeFocused();
  await page.getByRole("button", { name: "History" }).click();
  await expect(page.locator("body")).not.toHaveCSS("overflow-x", "scroll");
  await expect(page.getByText("Create task")).toHaveCount(0);
});

test("v0006.29 every rendered filter matches the view manifest on desktop and mobile", async ({
  page,
}) => {
  const manifest: Array<[string, string[], () => Promise<void>]> = [
    ["dashboard", ["period"], async () => page.goto(proxy.baseUrl)],
    [
      "history",
      ["search", "status", "period", "date", "limit"],
      async () => page.goto(`${proxy.baseUrl}?view=history`),
    ],
    [
      "executors",
      ["search", "status", "limit"],
      async () => page.goto(`${proxy.baseUrl}?view=executors`),
    ],
    [
      "parameters",
      ["scope", "limit"],
      async () => page.goto(`${proxy.baseUrl}?view=parameters`),
    ],
    [
      "audit",
      ["search", "limit"],
      async () => page.goto(`${proxy.baseUrl}?view=audit`),
    ],
    [
      "executor tasks",
      ["status", "limit"],
      async () => {
        await page.goto(`${proxy.baseUrl}?view=executors`);
        await page.getByRole("button", { name: "executor-alpha" }).click();
      },
    ],
  ];
  const keys = async () =>
    await page
      .locator("[data-filter-key]:visible")
      .evaluateAll((elements) =>
        elements
          .map((element) => element.getAttribute("data-filter-key"))
          .sort(),
      );
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: 844 });
    for (const [, expected, open] of manifest) {
      await open();
      await expect.poll(keys).toEqual([...expected].sort());
    }
  }

  await page.goto(`${proxy.baseUrl}?view=history`);
  await page
    .getByPlaceholder("Task ID, category, or executor")
    .fill("does-not-exist");
  await expect(
    page.getByText("No tasks match the selected filters"),
  ).toBeVisible();
  await page.getByRole("button", { name: "Reset" }).click();
  await expect(
    page.getByRole("button", { name: "task-alpha-running" }),
  ).toBeVisible();

  await page.goto(`${proxy.baseUrl}?view=parameters`);
  await page.getByRole("button", { name: "Change history" }).first().click();
  await expect.poll(keys).toEqual(["limit", "limit", "scope"]);
  await page
    .locator("#history-dialog")
    .getByLabel("Rows per page")
    .selectOption("25");
  await expect
    .poll(() =>
      proxy.requests.some((request) => request.includes("/revisions?limit=25")),
    )
    .toBe(true);
  await page.getByRole("button", { name: "Close" }).click();
  const versions = page.getByRole("button", { name: "Versions" }).first();
  if (await versions.count()) {
    await versions.click();
    await expect.poll(keys).toEqual(["limit", "limit", "scope"]);
    await page
      .locator("#history-dialog")
      .getByLabel("Rows per page")
      .selectOption("50");
    await expect
      .poll(() =>
        proxy.requests.some((request) =>
          request.includes("/versions?limit=50"),
        ),
      )
      .toBe(true);
  }
});
