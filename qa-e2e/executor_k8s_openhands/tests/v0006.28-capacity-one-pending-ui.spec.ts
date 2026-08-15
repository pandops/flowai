import * as path from "node:path";

import { startMockedProxy } from "../../web-ui/fixtures/mocked_proxy";
import { expect, test } from "../fixtures/k3d-suite";

const namespace = "flowai-executor-k8s";

test("v0006.28 max capacity one keeps the next K8s task pending and eventless in UI", async ({
  page,
  suite,
}) => {
  test.setTimeout(180_000);
  await suite.kubectl(
    "delete",
    "pod",
    "-n",
    namespace,
    "-l",
    "flowai.runtime=k8s",
    "--ignore-not-found=true",
    "--wait=true",
  );
  await new Promise((resolve) => setTimeout(resolve, 2_000));
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
  const taskState = async (taskID: string) => {
    const response = await suite.gatewayFetch(
      suite.teamA,
      `/v1/tasks/${taskID}`,
    );
    return (await response.json()) as {
      current_state: string;
      executor_id: string | null;
    };
  };
  const podCount = async () => {
    const pods = JSON.parse(
      await suite.kubectl(
        "get",
        "pods",
        "-n",
        namespace,
        "-l",
        "flowai.runtime=k8s",
        "-o",
        "json",
      ),
    ) as { items: unknown[] };
    return pods.items.length;
  };
  try {
    const first = await suite.ingestTask(suite.teamA, {
      prompt: "FLOWAI_HOLD_CAPACITY first task",
    });
    await expect
      .poll(async () => (await taskState(first.task_id)).current_state, {
        timeout: 30_000,
      })
      .toBe("running");
    await expect.poll(podCount, { timeout: 30_000 }).toBe(1);

    const second = await suite.ingestTask(suite.teamA, {
      prompt: "second task waits for capacity",
    });
    await new Promise((resolve) => setTimeout(resolve, 2_000));
    expect(await taskState(second.task_id)).toMatchObject({
      current_state: "pending",
      executor_id: null,
    });
    const events = (await (
      await suite.gatewayFetch(
        suite.teamA,
        `/v1/tasks/${second.task_id}/events`,
      )
    ).json()) as { items: unknown[] };
    expect(events.items).toEqual([]);
    expect(await podCount()).toBe(1);

    await page.goto(`${proxy.baseUrl}?view=history&status=pending`);
    await expect(
      page.getByRole("button", { name: second.task_id }),
    ).toBeVisible();
    await page.getByRole("button", { name: second.task_id }).click();
    await expect(
      page.getByText("Ожидаем успешного claim. Событий пока нет."),
    ).toBeVisible();

    await expect
      .poll(async () => (await taskState(second.task_id)).current_state, {
        timeout: 90_000,
      })
      .toBe("finished");
    await page.reload();
    await expect(page.locator(".timeline li strong")).toHaveText([
      "task.lifecycle.created",
      "task.lifecycle.running",
      "task.lifecycle.finished",
    ]);
    expect(await podCount()).toBeLessThanOrEqual(1);
  } finally {
    await proxy.close();
    await suite
      .kubectl(
        "delete",
        "pod",
        "-n",
        namespace,
        "-l",
        "flowai.runtime=k8s",
        "--ignore-not-found=true",
        "--wait=true",
      )
      .catch(() => undefined);
  }
});
