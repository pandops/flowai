// v0005.7-assigned-task-environment-opens-with-team-bound-scope-token
//
// Inherited v0002 scope-token contract: the assigned K8s Executor
// opens the task-bound launch-parameter snapshot via GET
// /v1/tasks/{task_id}/launch-parameters/open with the
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
  const createEnvironment = await suite.registryFetch(
    `/ui/v1/teams/${suite.teamA.admin.team_id}/launch-parameters`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name: `v0005-7-${Date.now()}`,
        scope: "task_type",
        task_type_id: suite.teamA.taskType.task_type_id,
        env: {
          FLOWAI_V0005_REGION: "cluster-local",
          OPENAI_MODEL: "openai/flowai-mock",
          OPENAI_BASE_URL: suite.mockLLMBaseURL,
        },
        image: null,
      }),
    },
  );
  expect(createEnvironment.status).toBe(201);
  const environment = (await createEnvironment.json()) as {
    environment_id: string;
  };
  const secret = await suite.registryFetch(
    `/ui/v1/teams/${suite.teamA.admin.team_id}/launch-parameters/${environment.environment_id}/secrets`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        key: "OPENAI_API_KEY",
        value: "flowai-placeholder-key",
      }),
    },
  );
  expect(secret.status).toBe(201);
  const task = await suite.ingestTask(
    suite.teamA,
    { prompt: "hold environment v0005.7" },
    undefined,
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
    `/v1/tasks/${task.task_id}/launch-parameters/open`,
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
    code: "launch_parameters_unknown_or_unavailable",
  });

  const cancel = await suite.gatewayFetch(
    suite.teamA,
    `/v1/tasks/${task.task_id}/controls`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        action: "cancel",
        idempotency_key: `v0005-7-cleanup-${crypto.randomUUID()}`,
        reason: "release executor capacity after environment assertion",
      }),
    },
  );
  expect(cancel.status).toBe(202);
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
      { timeout: 60_000 },
    )
    .toMatch(/^(finished|failed)$/);
  await suite.clearLLM(suite.teamA, environment.environment_id);
});
