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

test("v0006.12 manages team and task-type launch parameters without task scope", async ({
  page,
}) => {
  await page.goto(`${proxy.baseUrl}?view=parameters`);
  await expect(
    page.getByRole("heading", { name: "Launch parameters" }),
  ).toBeVisible();
  await expect(page.getByText("Team parameters")).toBeVisible();
  await page.getByRole("button", { name: "Add variable" }).click();
  await page.getByLabel("Variable scope").selectOption("task_type");
  await page
    .locator("#env-dialog select[name='task_type_id']")
    .selectOption("repository-task");
  await page.getByLabel("Key name", { exact: true }).fill("NEW_ENV_KEY");
  await page.getByLabel("Value", { exact: true }).fill("new-value");
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.getByText("NEW_ENV_KEY=new-value")).toBeVisible();
  await expect(page.getByLabel("Variable scope").locator("option")).toHaveText([
    "Team",
    "Task category",
  ]);
});

test("v0006.13 exposes immutable parameter history without secret plaintext", async ({
  page,
}) => {
  await page.goto(`${proxy.baseUrl}?view=parameters`);
  await page.getByRole("button", { name: "Change history" }).first().click();
  await expect(page.locator("#history-dialog")).toContainText("Revision 2");
  await expect(page.locator("#history-dialog")).not.toContainText(
    "fixture-secret",
  );
});

test("v0006.14 disables cancellation after request and keeps it beside status", async ({
  page,
}) => {
  await page.goto(`${proxy.baseUrl}?view=history&search=task-alpha-running`);
  await page.getByRole("button", { name: "task-alpha-running" }).click();
  const cancel = page.locator("[data-cancel]");
  await expect(cancel).toBeVisible();
  await cancel.click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Cancel task" })
    .click();
  await expect(cancel).toBeDisabled();
  await expect(cancel).toHaveText("Awaiting cancellation…");
});

test("v0006.15 appends live work and agent-published reasoning without log controls", async ({
  page,
}) => {
  await page.goto(`${proxy.baseUrl}?view=history&search=task-alpha-running`);
  await page.getByRole("button", { name: "task-alpha-running" }).click();
  await expect(page.getByText("Inspecting the workspace")).toBeVisible();
  registry.push("team-alpha", {
    frame_id: "live-log",
    team_id: "team-alpha",
    frame_type: "task_log",
    task_id: "task-alpha-running",
    occurred_at: new Date().toISOString(),
    data: { log_offset: 3, stream: "work", content: "New container output" },
  });
  await expect(
    page
      .locator("[data-log]")
      .getByText("New container output", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: /clear|pause|connect|follow/i }),
  ).toHaveCount(0);
});
