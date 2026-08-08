// v0005.10-system-owned-k8s-executor-dispatches-across-teams
//
// System-scope registration with the team_id property absent,
// discovery across teams, oldest-eligible claim, task-team Pod /
// event envelopes, and unchanged canonical task ownership are
// verified for a K8s system-scope Executor.

import { test, expect } from "../fixtures/k3d-suite";

test("v0005.10 system-owned k8s executor dispatches across teams", async ({
  suite,
}) => {
  const system = await suite.installSystemExecutor();
  const registration = await suite.registryFetch(
    `/v1/executors/${system.executorID}`,
    {
      headers: {
        "X-FlowAI-Role": "system-executor",
        "X-FlowAI-Executor-Id": system.executorID,
        "X-FlowAI-Request-Id": crypto.randomUUID(),
      },
    },
  );
  expect(registration.status).toBe(200);
  const record = (await registration.json()) as Record<string, unknown>;
  expect(record).toMatchObject({
    executor_id: system.executorID,
    scope: "system",
    authorized_tag: "k8s-cluster-a",
  });
  expect(record).not.toHaveProperty("team_id");

  const first = await suite.ingestTask(suite.teamB, {
    prompt: "system team B",
  });
  await new Promise((resolve) => setTimeout(resolve, 20));
  const second = await suite.ingestTask(suite.teamA, {
    prompt: "system team A",
  });
  const tasks = [
    { task: first, team: suite.teamB },
    { task: second, team: suite.teamA },
  ];
  const deadline = Date.now() + 90_000;
  for (const item of tasks) {
    let row: Record<string, unknown> = {};
    while (Date.now() < deadline) {
      const response = await suite.gatewayFetch(
        item.team,
        `/v1/tasks/${item.task.task_id}`,
      );
      row = (await response.json()) as Record<string, unknown>;
      if (row.executor_id === system.executorID) break;
      await new Promise((resolve) => setTimeout(resolve, 200));
    }
    expect(row).toMatchObject({
      executor_id: system.executorID,
      team_id: item.team.admin.team_id,
    });
    const pod = JSON.parse(
      await suite.kubectl(
        "get",
        "pods",
        "-n",
        system.namespace,
        "-l",
        `flowai.task_id=${item.task.task_id}`,
        "-o",
        "json",
      ),
    ) as { items: Array<{ metadata: { labels: Record<string, string> } }> };
    expect(pod.items).toHaveLength(1);
    expect(pod.items[0].metadata.labels).toMatchObject({
      "flowai.executor_id": system.executorID,
      "flowai.team_id": item.team.admin.team_id,
      "flowai.executor_scope": "system",
      "flowai.runtime": "k8s",
    });
  }
  await suite.kubectl(
    "scale",
    "deployment/executor-k8s-openhands",
    "-n",
    system.namespace,
    "--replicas=0",
  );
  await suite.kubectl(
    "scale",
    "deployment/executor-k8s-openhands",
    "-n",
    "flowai-executor-k8s",
    "--replicas=1",
  );
  await suite.kubectl(
    "rollout",
    "status",
    "deployment/executor-k8s-openhands",
    "-n",
    "flowai-executor-k8s",
    "--timeout=120s",
  );
});
