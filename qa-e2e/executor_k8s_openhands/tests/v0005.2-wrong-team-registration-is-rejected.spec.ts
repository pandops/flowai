// v0005.2-wrong-team-registration-is-rejected
//
// Verify a K8s Executor registration with a team_id that does not
// match the identity-bound team is rejected without persistence.

import { test, expect } from "../fixtures/k3d-suite";

test("v0005.2 wrong team registration is rejected", async ({ suite }) => {
  const response = await suite.registryFetch(
    `/v1/executors/${suite.executorID}`,
    {
      method: "PUT",
      headers: {
        "Content-Type": "application/json",
        "X-FlowAI-Role": "team-executor",
        "X-FlowAI-Executor-Id": suite.executorID,
        "X-FlowAI-Team-Id": suite.teamA.admin.team_id,
        "X-Request-Id": "v0005-2-mismatch",
      },
      body: JSON.stringify({
        scope: "team",
        team_id: suite.teamB.admin.team_id,
        executor_type: "executor_k8s_openhands",
        authorized_tag: "k8s-cluster-a",
        max_capacity: 2,
        running_count: 0,
        runtime_metadata: { runtime: "k8s", tool: "openhands" },
      }),
    },
  );
  expect([400, 403, 409]).toContain(response.status);

  const canonical = await suite.registryFetch(
    `/v1/executors/${suite.executorID}`,
    {
      headers: {
        "X-FlowAI-Role": "team-executor",
        "X-FlowAI-Executor-Id": suite.executorID,
        "X-FlowAI-Team-Id": suite.teamA.admin.team_id,
        "X-Request-Id": "v0005-2-read",
      },
    },
  );
  expect(canonical.status).toBe(200);
  expect(await canonical.json()).toMatchObject({
    team_id: suite.teamA.admin.team_id,
  });
});
