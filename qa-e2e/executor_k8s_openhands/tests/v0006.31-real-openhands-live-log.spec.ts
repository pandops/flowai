import * as path from "node:path";

import { startMockedProxy } from "../../web-ui/fixtures/mocked_proxy";
import { expect, test } from "../fixtures/k3d-suite";

test("v0006.31 real K8s OpenHands output streams reasoning and work into the open task card", async ({
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
  const llmEnvironmentID = await suite.configureLLM(suite.teamA);
  try {
    const task = await suite.ingestTask(
      suite.teamA,
      {
        prompt:
          "FLOWAI_HOLD_CAPACITY publish agent output, write the marker, and finish.",
      },
      llmEnvironmentID,
      suite.realAgentImage,
    );
    await expect
      .poll(
        async () => {
          const response = await suite.gatewayFetch(
            suite.teamA,
            `/v1/tasks/${task.task_id}`,
          );
          return ((await response.json()) as { current_state: string })
            .current_state;
        },
        { timeout: 30_000 },
      )
      .toBe("running");
    await page.goto(`${proxy.baseUrl}?view=task&id=${task.task_id}`);
    await expect(page.getByRole("heading", { name: "Live log" })).toBeVisible();
    await expect(
      page.getByText("Проверяю рабочее дерево перед запуском инструмента."),
    ).toHaveCount(0);

    const reasoningLine = page
      .locator(".log-line.reasoning")
      .filter({ hasText: "Проверяю рабочее дерево" });
    await expect(reasoningLine).toBeVisible({ timeout: 20_000 });
    await expect(reasoningLine).toHaveCount(1);
    await expect(
      page
        .locator(".log-line.work")
        .filter({ hasText: "flowai-real-runtime.marker" }),
    ).toHaveCount(1, { timeout: 30_000 });
    await expect(
      page.locator(".log-line.work").filter({ hasText: "Task complete." }),
    ).toHaveCount(1);
    await expect
      .poll(
        async () => {
          const response = await suite.gatewayFetch(
            suite.teamA,
            `/v1/tasks/${task.task_id}`,
          );
          return ((await response.json()) as { current_state: string })
            .current_state;
        },
        { timeout: 90_000 },
      )
      .toBe("finished");

    const replay = (await (
      await fetch(
        `${suite.registryBaseURL}/ui/v1/teams/${suite.teamA.admin.team_id}/tasks/${task.task_id}/logs?limit=100`,
      )
    ).json()) as {
      items: Array<{ log_offset: number; stream: string; content: string }>;
    };
    expect(replay.items.map((item) => item.log_offset)).toEqual(
      replay.items.map((item) => item.log_offset).sort((a, b) => a - b),
    );
    expect(
      replay.items.filter(
        (item) =>
          item.stream === "reasoning" &&
          item.content.includes("Проверяю рабочее дерево"),
      ),
    ).toHaveLength(1);
    await page.reload();
    await expect(
      page
        .locator(".log-line.reasoning")
        .filter({ hasText: "Проверяю рабочее дерево" }),
    ).toHaveCount(1);
    await expect(
      page.getByRole("button", {
        name: /очистить|пауза|продолжить|переподключить|копировать/i,
      }),
    ).toHaveCount(0);
  } finally {
    await proxy.close();
    await suite.clearLLM(suite.teamA, llmEnvironmentID);
  }
});
