// v0005.5-foreign-team-event-write-is-rejected-without-append
//
// A cross-team event post returns the same non-revealing 404 shape
// used for an unknown identifier and appends no event; a same-team
// Executor that is not the recorded tasks.executor_id returns 403
// not_assigned; an authenticated Executor whose envelope team_id
// differs from its immutable service binding returns 403
// team_mismatch.

import { test, expect } from "../fixtures/k3d-suite";

test("v0005.5 foreign-team event write is rejected without append", async ({
  suite,
}) => {
  const task = await suite.ingestTask(suite.teamA, { prompt: "hold v0005.5" });
  const deadline = Date.now() + 60_000;
  let canonical: Record<string, unknown> = {};
  while (Date.now() < deadline) {
    const response = await suite.gatewayFetch(
      suite.teamA,
      `/v1/tasks/${task.task_id}`,
    );
    if (response.status === 200) {
      canonical = (await response.json()) as Record<string, unknown>;
      if (canonical.executor_id === suite.executorID) break;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  expect(canonical.executor_id).toBe(suite.executorID);

  const register = await suite.registryFetch("/v1/executors", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-FlowAI-Role": "team-executor",
      "X-FlowAI-Team-Id": suite.teamA.admin.team_id,
      "X-FlowAI-Request-Id": `v0005-5-register-${Date.now()}`,
    },
    body: JSON.stringify({
      scope: "team",
      team_id: suite.teamA.admin.team_id,
      executor_type: "executor_k8s_openhands",
      authorized_tag: "k8s-cluster-a",
      max_capacity: 1,
      running_count: 0,
      runtime_metadata: { runtime: "k8s", tool: "openhands" },
    }),
  });
  expect(register.status).toBe(201);
  const other = (await register.json()) as { executor_id: string };
  const event = (teamID: string, executorID: string) => ({
    event_id: crypto.randomUUID(),
    team_id: teamID,
    task_id: task.task_id,
    executor_id: executorID,
    event_type: "running",
    occurred_at: new Date().toISOString(),
    payload: { source: "v0005.5" },
  });
  const post = async (
    authTeamID: string,
    executorID: string,
    envelopeTeamID: string,
  ): Promise<Response> =>
    await suite.registryFetch(`/v1/tasks/${task.task_id}/events`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-FlowAI-Role": "team-executor",
        "X-FlowAI-Team-Id": authTeamID,
        "X-FlowAI-Executor-Id": executorID,
        "X-FlowAI-Request-Id": crypto.randomUUID(),
      },
      body: JSON.stringify(event(envelopeTeamID, executorID)),
    });

  expect(
    (
      await post(
        suite.teamB.admin.team_id,
        suite.executorID,
        suite.teamB.admin.team_id,
      )
    ).status,
  ).toBe(404);
  expect(
    (
      await post(
        suite.teamA.admin.team_id,
        other.executor_id,
        suite.teamA.admin.team_id,
      )
    ).status,
  ).toBe(403);
  expect(
    (
      await post(
        suite.teamA.admin.team_id,
        suite.executorID,
        suite.teamB.admin.team_id,
      )
    ).status,
  ).toBe(403);

  const history = await suite.gatewayFetch(
    suite.teamA,
    `/v1/tasks/${task.task_id}/events`,
  );
  const body = (await history.json()) as {
    items: Array<{ event_type: string }>;
  };
  expect(body.items.map((item) => item.event_type)).toEqual([
    "created",
    "running",
  ]);
  await suite.kubectl(
    "delete",
    "pod",
    "-n",
    "flowai-executor-k8s",
    "-l",
    `flowai.task_id=${task.task_id}`,
    "--wait=true",
  );
  await suite.kubectl(
    "delete",
    "pod",
    "-n",
    "flowai-executor-k8s",
    "-l",
    "app.kubernetes.io/name=executor-k8s-openhands",
    "--wait=true",
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
