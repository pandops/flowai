import * as path from "node:path";

import { startMockedProxy } from "../../web-ui/fixtures/mocked_proxy";
import { expect, test } from "../fixtures/k3d-suite";

test("v0006.24 → v0006.25 → v0006.23 created team, K8s Executor and completed task appear in Mission Control", async ({
  page,
  suite,
}) => {
  test.setTimeout(180_000);
  const proxy = await startMockedProxy(
    [
      {
        team_id: suite.teamA.admin.team_id,
        team_name: suite.teamA.admin.team_name,
      },
    ],
    path.resolve(__dirname, "../../../svc/web-ui/web/index.html"),
    suite.registryBaseURL,
  );
  try {
    await page.goto(proxy.baseUrl);
    await expect(page.getByRole("combobox", { name: "Team" })).toHaveValue(
      suite.teamA.admin.team_id,
    );
    await expect(
      page.getByRole("combobox", { name: "Team" }).locator("option:checked"),
    ).toHaveText(suite.teamA.admin.team_name);

    await page.getByRole("button", { name: "Executors" }).click();
    const executorLink = page.getByRole("button", { name: suite.executorID });
    await expect(executorLink).toBeVisible();
    await executorLink.click();
    await expect(page.getByText("executor_k8s_openhands")).toBeVisible();
    await expect(page.getByText("team", { exact: true })).toBeVisible();
    await expect(
      page.getByText("k8s-cluster-a", { exact: true }),
    ).toBeVisible();

    const llmEnvironmentID = await suite.configureLLM(suite.teamA);
    const task = await suite.ingestTask(suite.teamA, {
      prompt: "Complete the Mission Control lifecycle test.",
    });
    await page.getByRole("button", { name: "History" }).click();
    const taskLink = page.getByRole("button", { name: task.task_id });
    await expect(taskLink).toBeVisible({ timeout: 30_000 });
    await taskLink.click();
    await expect(
      page.getByRole("heading", { name: "Task details" }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: suite.executorID }),
    ).toBeVisible({ timeout: 30_000 });
    await expect
      .poll(
        async () => await page.locator(".timeline li strong").allTextContents(),
        { timeout: 90_000 },
      )
      .toEqual([
        "task.lifecycle.created",
        "task.lifecycle.running",
        "task.lifecycle.finished",
      ]);
    await page.reload();
    await expect(
      page.getByRole("heading", { name: "Task details" }),
    ).toBeVisible();
    await expect(page.locator(".timeline li strong")).toHaveText([
      "task.lifecycle.created",
      "task.lifecycle.running",
      "task.lifecycle.finished",
    ]);
    await page.getByRole("button", { name: suite.executorID }).click();
    await expect(
      page.getByRole("heading", { name: "Executor details" }),
    ).toBeVisible();
    await expect(page.getByText("executor_k8s_openhands")).toBeVisible();
    await suite.clearLLM(suite.teamA, llmEnvironmentID);
  } finally {
    await proxy.close();
  }
});
