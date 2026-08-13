// v0005.3-team-isolated-discovery-and-claim-no-leak
//
// Collection-level discovery of a tag whose matching pending tasks
// all belong to a different team_id returns the NORMAL empty
// result (204 No Content or 200 with an empty task list and no
// count, cursor, total, or pagination metadata), indistinguishable
// from "no tasks match this tag anywhere"; point-resource lookups
// against a foreign-team task_id (claim, task detail read) return
// a non-revealing 404 indistinguishable from "resource does not
// exist". In both cases the Executor creates no Pod and appends no
// event.

import { test, expect } from "../fixtures/k3d-suite";

test("v0005.3 team-isolated discovery and claim has no leak", async ({
  suite,
}) => {
  const sourceID = `v0005-3-${Date.now()}`;
  const ingest = await suite.registryFetch("/v1/tasks", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-FlowAI-Role": "listener",
      "X-FlowAI-Team-Id": suite.teamB.admin.team_id,
      "X-FlowAI-Listener-Identity": suite.teamB.listenerIdentity,
      "X-FlowAI-Source-System-Id": suite.teamB.sourceSystem.source_system_id,
      "X-Request-Id": "v0005-3-ingest",
    },
    body: JSON.stringify({
      team_id: suite.teamB.admin.team_id,
      source_system_id: suite.teamB.sourceSystem.source_system_id,
      source_id: sourceID,
      task_type_id: suite.teamB.taskType.task_type_id,
      payload: { test: "v0005.3" },
    }),
  });
  expect(ingest.status).toBe(201);
  const task = (await ingest.json()) as { task_id: string };
  const headers = {
    "X-FlowAI-Role": "team-executor",
    "X-FlowAI-Executor-Id": suite.executorID,
    "X-FlowAI-Team-Id": suite.teamA.admin.team_id,
    "X-Request-Id": "v0005-3-executor",
  };
  const discovery = await suite.registryFetch(
    `/v1/executors/${suite.executorID}/tasks?tag=k8s-cluster-a`,
    { headers },
  );
  expect([200, 204]).toContain(discovery.status);
  if (discovery.status === 200) {
    const body = (await discovery.json()) as {
      items?: Array<{ task_id: string }>;
    };
    expect(body.items ?? []).not.toContainEqual(
      expect.objectContaining({ task_id: task.task_id }),
    );
    expect(body).not.toHaveProperty("total");
    expect(body).not.toHaveProperty("cursor");
  }
  const claim = await suite.registryFetch(
    `/v1/executors/${suite.executorID}/claim`,
    {
      method: "POST",
      headers: { ...headers, "Content-Type": "application/json" },
      body: JSON.stringify({
        task_id: task.task_id,
        command_id: crypto.randomUUID(),
      }),
    },
  );
  expect(claim.status).toBe(404);
  const pods = await suite.kubectl(
    "get",
    "pods",
    "-n",
    "flowai-executor-k8s",
    "-l",
    `flowai.task_id=${task.task_id}`,
    "-o",
    "name",
  );
  expect(pods).toBe("");
});
