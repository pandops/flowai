import * as path from "node:path";

import { startMockedProxy } from "../../web-ui/fixtures/mocked_proxy";
import { expect, test } from "../fixtures/k3d-suite";

test("v0006.30 a committed same-team task increments the open dashboard once", async ({
  page,
  suite,
}) => {
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
  const today = new Date();
  const todayKey = [
    today.getUTCFullYear(),
    String(today.getUTCMonth() + 1).padStart(2, "0"),
    String(today.getUTCDate()).padStart(2, "0"),
  ].join("-");
  const total = async () =>
    Number(
      (await page.getByText(/\d+ runs · Mon–Sun/).textContent())?.match(
        /\d+/,
      )?.[0],
    );
  const todayCount = async () =>
    Number(
      (
        await page
          .locator(`[data-dashboard-date="${todayKey}"]`)
          .getAttribute("aria-label")
      )?.match(/\((\d+)\)$/)?.[1],
    );
  try {
    await page.goto(proxy.baseUrl);
    await expect(
      page.getByRole("button", { name: "Week", exact: true }),
    ).toHaveAttribute("aria-pressed", "true");
    const totalBefore = await total();
    const todayBefore = await todayCount();
    await expect(page.getByTestId("live-frame")).toHaveAttribute(
      "data-stream-state",
      "connected",
    );

    const taskTypeResponse = await suite.registryFetch("/admin/task-types", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-FlowAI-Role": "admin",
        "X-FlowAI-Admin-Subject": "v0006-dashboard-admin",
        "X-Request-Id": crypto.randomUUID(),
      },
      body: JSON.stringify({
        team_id: suite.teamA.admin.team_id,
        execution_tag: `unmatched-${crypto.randomUUID()}`,
        default_image: null,
      }),
    });
    expect(taskTypeResponse.status).toBe(201);
    const taskType = (await taskTypeResponse.json()) as {
      task_type_id: string;
    };
    const ingest = await suite.registryFetch("/v1/tasks", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-FlowAI-Role": "listener",
        "X-FlowAI-Team-Id": suite.teamA.admin.team_id,
        "X-FlowAI-Listener-Identity": suite.teamA.listenerIdentity,
        "X-FlowAI-Source-System-Id": suite.teamA.sourceSystem.source_system_id,
        "X-FlowAI-Request-Id": crypto.randomUUID(),
      },
      body: JSON.stringify({
        team_id: suite.teamA.admin.team_id,
        source_system_id: suite.teamA.sourceSystem.source_system_id,
        source_id: crypto.randomUUID(),
        task_type_id: taskType.task_type_id,
        payload: {},
      }),
    });
    expect(ingest.status).toBe(201);
    const ingestedTask = (await ingest.json()) as { task_id: string };

    await expect(page.getByTestId("live-frame")).toContainText(
      ingestedTask.task_id,
      { timeout: 2_000 },
    );
    await expect.poll(total, { timeout: 2_000 }).toBe(totalBefore + 1);
    await expect.poll(todayCount, { timeout: 2_000 }).toBe(todayBefore + 1);
    await new Promise((resolve) => setTimeout(resolve, 750));
    expect(await total()).toBe(totalBefore + 1);
    expect(await todayCount()).toBe(todayBefore + 1);

    await suite.ingestTask(suite.teamB, { foreign: true });
    await new Promise((resolve) => setTimeout(resolve, 750));
    expect(await total()).toBe(totalBefore + 1);
    expect(await todayCount()).toBe(todayBefore + 1);
  } finally {
    await proxy.close();
  }
});
