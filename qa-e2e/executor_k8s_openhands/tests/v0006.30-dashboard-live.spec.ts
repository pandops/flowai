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
  const count = async (label: string) =>
    Number(
      await page
        .locator(".stat")
        .filter({ hasText: label })
        .locator("strong")
        .textContent(),
    );
  try {
    await page.goto(proxy.baseUrl);
    await page.getByRole("button", { name: "День" }).click();
    const totalBefore = await count("Всего");
    const pendingBefore = await count("Ожидает");
    const unchanged = Object.fromEntries(
      await Promise.all(
        ["Создана", "Выполняется", "Завершена", "Ошибка"].map(async (label) => [
          label,
          await count(label),
        ]),
      ),
    );
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
    await expect
      .poll(() => count("Всего"), { timeout: 2_000 })
      .toBe(totalBefore + 1);
    expect(await count("Ожидает")).toBe(pendingBefore + 1);
    for (const [label, value] of Object.entries(unchanged))
      expect(await count(label)).toBe(value);
    await new Promise((resolve) => setTimeout(resolve, 750));
    expect(await count("Всего")).toBe(totalBefore + 1);

    await suite.ingestTask(suite.teamB, { foreign: true });
    await new Promise((resolve) => setTimeout(resolve, 750));
    expect(await count("Всего")).toBe(totalBefore + 1);
  } finally {
    await proxy.close();
  }
});
