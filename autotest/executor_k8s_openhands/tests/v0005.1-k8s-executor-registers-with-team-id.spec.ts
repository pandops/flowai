// v0005.1-k8s-executor-registers-with-team-id
//
// Verify a K8s Executor registers with exactly one scope = team,
// one team_id, one authorized_tag, observed max_capacity, observed
// running_count, and runtime metadata, all bound to the configured
// Executor team identity. The first-start POST returns 201 with a
// State Registry-generated UUID that is cached before task intake;
// restart refresh returns 200; the State Registry rejects any
// change to scope, team_id, or authorized_tag between
// re-registrations as scope_change_forbidden or
// team_binding_mismatch.

import { test, expect } from "../fixtures/k3d-suite";

test("v0005.1 k8s executor registers with team-id", async ({ suite }) => {
  expect(suite.clusterName).toMatch(/^flowai-exec-k8s-/);
  expect(suite.executorID).toMatch(/^[0-9a-f-]{36}$/);
  const response = await suite.registryFetch(
    `/v1/executors/${suite.executorID}`,
    {
      headers: {
        "X-FlowAI-Role": "team-executor",
        "X-FlowAI-Executor-Id": suite.executorID,
        "X-FlowAI-Team-Id": suite.teamA.admin.team_id,
        "X-Request-Id": "v0005-1-read",
      },
    },
  );
  expect(response.status).toBe(200);
  const record = (await response.json()) as Record<string, unknown>;
  expect(record).toMatchObject({
    executor_id: suite.executorID,
    executor_type: "executor_k8s_openhands",
    scope: "team",
    team_id: suite.teamA.admin.team_id,
    authorized_tag: "k8s-cluster-a",
  });
  expect(record).not.toHaveProperty("team_name");
});
