// v0005.7-assigned-task-environment-opens-with-team-bound-scope-token
//
// Inherited v0002 scope-token contract: the assigned K8s Executor
// opens the environment via GET
// /v1/environments/{environment_id}/open?task_id={task_id} with the
// compact three-part signed scope token in
// X-FlowAI-Scope-Token; protected header carries alg (allow-listed
// HS256/HS384/HS512), kid, and typ; payload carries every claim;
// every failure variation returns the same non-revealing 404
// environment_unknown_or_unavailable shape with zero provider
// decrypt operations.

import { test, expect } from "../fixtures/k3d-suite";

test("v0005.7 assigned task environment opens with team-bound scope token", async ({
  suite,
}) => {
  const createEnvironment = await suite.gatewayFetch(
    suite.teamA,
    "/v1/environments",
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name: `v0005-7-${Date.now()}`,
        scope: { project_id: null, task_id: null, parent_task_id: null },
        values: { FLOWAI_V0005_REGION: "cluster-local" },
      }),
    },
  );
  expect(createEnvironment.status).toBe(201);
  const environment = (await createEnvironment.json()) as {
    environment_id: string;
  };
  const task = await suite.ingestTask(
    suite.teamA,
    { prompt: "hold environment v0005.7" },
    environment.environment_id,
  );
  const deadline = Date.now() + 60_000;
  let pod: {
    items: Array<{
      spec: {
        containers: Array<{ env?: Array<{ name: string; value: string }> }>;
      };
    }>;
  } = { items: [] };
  while (Date.now() < deadline) {
    pod = JSON.parse(
      await suite.kubectl(
        "get",
        "pods",
        "-n",
        "flowai-executor-k8s",
        "-l",
        `flowai.task_id=${task.task_id}`,
        "-o",
        "json",
      ),
    ) as typeof pod;
    if (pod.items.length === 1) break;
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  expect(pod.items).toHaveLength(1);
  expect(pod.items[0].spec.containers[0].env).toContainEqual({
    name: "FLOWAI_V0005_REGION",
    value: "cluster-local",
  });

  const invalid = await suite.registryFetch(
    `/v1/environments/${environment.environment_id}/open?task_id=${task.task_id}`,
    {
      headers: {
        "X-FlowAI-Role": "team-executor",
        "X-FlowAI-Team-Id": suite.teamA.admin.team_id,
        "X-FlowAI-Executor-Id": suite.executorID,
        "X-FlowAI-Scope-Token": "invalid.scope.token",
        "X-FlowAI-Request-Id": crypto.randomUUID(),
      },
    },
  );
  expect(invalid.status).toBe(404);
  expect(await invalid.json()).toMatchObject({
    code: "environment_unknown_or_unavailable",
  });

  await suite.kubectl(
    "delete",
    "pod",
    "-n",
    "flowai-executor-k8s",
    "-l",
    `flowai.task_id=${task.task_id}`,
    "--wait=true",
  );
  await new Promise((resolve) => setTimeout(resolve, 2_000));
});
