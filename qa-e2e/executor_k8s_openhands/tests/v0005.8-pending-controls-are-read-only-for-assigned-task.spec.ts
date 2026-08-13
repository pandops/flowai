// v0005.8-pending-controls-are-read-only-for-assigned-task
//
// The assigned same-team Executor reads and applies pending controls
// for its assigned task; a cross-team read returns a non-revealing
// 404; a same-team unassigned read returns 403 not_assigned.

import { test, expect } from "../fixtures/k3d-suite";

test("v0005.8 pending controls are read only for assigned task", async ({
  suite,
}) => {
  const task = await suite.ingestTask(suite.teamA, {
    prompt: "hold controls v0005.8",
  });
  const deadline = Date.now() + 60_000;
  let assigned = false;
  while (Date.now() < deadline) {
    const response = await suite.gatewayFetch(
      suite.teamA,
      `/v1/tasks/${task.task_id}`,
    );
    const body = (await response.json()) as { executor_id?: string };
    if (body.executor_id === suite.executorID) {
      assigned = true;
      break;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  expect(assigned).toBe(true);
  const control = await suite.gatewayFetch(
    suite.teamA,
    `/v1/tasks/${task.task_id}/controls`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        action: "cancel",
        idempotency_key: `v0005-8-${crypto.randomUUID()}`,
        reason: "k3d control delivery",
      }),
    },
  );
  expect(control.status).toBe(202);
  let logs = "";
  while (Date.now() < deadline) {
    logs = await suite.kubectl(
      "logs",
      "-n",
      "flowai-executor-k8s",
      "deployment/executor-k8s-openhands",
    );
    if (logs.includes("applied control")) break;
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  expect(logs).toContain("applied control");

  const foreignTask = await suite.ingestTask(suite.teamB, {
    prompt: "foreign control",
  });
  const executorHeaders = {
    "X-FlowAI-Role": "team-executor",
    "X-FlowAI-Team-Id": suite.teamA.admin.team_id,
    "X-FlowAI-Executor-Id": suite.executorID,
    "X-FlowAI-Request-Id": crypto.randomUUID(),
  };
  const foreignRead = await suite.registryFetch(
    `/v1/tasks/${foreignTask.task_id}/controls`,
    { headers: executorHeaders },
  );
  expect(foreignRead.status).toBe(404);

  const register = await suite.registryFetch("/v1/executors", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-FlowAI-Role": "team-executor",
      "X-FlowAI-Team-Id": suite.teamA.admin.team_id,
      "X-FlowAI-Request-Id": crypto.randomUUID(),
    },
    body: JSON.stringify({
      scope: "team",
      team_id: suite.teamA.admin.team_id,
      executor_type: "executor_k8s_openhands",
      authorized_tag: "k8s-cluster-a",
      max_capacity: 1,
      running_count: 0,
      runtime_metadata: { runtime: "k8s" },
    }),
  });
  const other = (await register.json()) as { executor_id: string };
  const unassignedRead = await suite.registryFetch(
    `/v1/tasks/${task.task_id}/controls`,
    {
      headers: {
        ...executorHeaders,
        "X-FlowAI-Executor-Id": other.executor_id,
      },
    },
  );
  expect(unassignedRead.status).toBe(403);
});
