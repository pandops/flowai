// v0005.4-claimed-task-runs-as-pod-with-running-then-terminal-events
//
// Same-team FIFO claim returns 200 claimed with
// tasks.resolved_image, tasks.image_source, tasks.claimed_at,
// tasks.owner_command_id, environment_id, and the State
// Registry-issued scope token; the K8s Executor uses
// resolved_image verbatim and refuses the task if the image cannot
// be pulled or started; emits a running event after 200 claimed,
// creates the Kubernetes Pod carrying the canonical labels and
// the runtime label flowai.runtime=k8s, treats the OpenHands
// terminal conversation status rather than Pod exit code as
// authoritative, and emits exactly one finished or failed event.

import { test, expect } from "../fixtures/k3d-suite";

test("v0005.4 claimed task runs as pod with running then terminal events", async ({
  suite,
}) => {
  const ingest = await suite.registryFetch("/v1/tasks", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-FlowAI-Role": "listener",
      "X-FlowAI-Team-Id": suite.teamA.admin.team_id,
      "X-FlowAI-Listener-Identity": suite.teamA.listenerIdentity,
      "X-FlowAI-Source-System-Id": suite.teamA.sourceSystem.source_system_id,
      "X-FlowAI-Request-Id": `v0005-4-${Date.now()}`,
    },
    body: JSON.stringify({
      team_id: suite.teamA.admin.team_id,
      source_system_id: suite.teamA.sourceSystem.source_system_id,
      source_id: `v0005-4-${Date.now()}`,
      task_type_id: suite.teamA.taskType.task_type_id,
      payload: { prompt: "finish deterministic v0005 mock task" },
    }),
  });
  expect(ingest.status).toBe(201);
  const task = (await ingest.json()) as { task_id: string };
  const gatewayHeaders = {
    "X-FlowAI-Role": "gateway",
    "X-FlowAI-Team-Id": suite.teamA.admin.team_id,
    "X-FlowAI-Operator-Id": `op-${suite.teamA.admin.team_id}`,
    "X-FlowAI-Request-Id": `v0005-4-read-${Date.now()}`,
  };
  const deadline = Date.now() + 90_000;
  let events: Array<Record<string, unknown>> = [];
  while (Date.now() < deadline) {
    const response = await suite.registryFetch(
      `/v1/tasks/${task.task_id}/events`,
      { headers: gatewayHeaders },
    );
    if (response.status === 200) {
      const body = (await response.json()) as {
        items?: Array<Record<string, unknown>>;
      };
      events = body.items ?? [];
      if (events.some((event) => event.event_type === "finished")) break;
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  expect(events.map((event) => event.event_type)).toEqual([
    "created",
    "running",
    "finished",
  ]);
  const pod = JSON.parse(
    await suite.kubectl(
      "get",
      "pod",
      "-n",
      "flowai-executor-k8s",
      "-l",
      `flowai.task_id=${task.task_id}`,
      "-o",
      "json",
    ),
  ) as {
    items: Array<{
      metadata: { labels: Record<string, string> };
      spec: { containers: Array<{ image: string }> };
    }>;
  };
  expect(pod.items).toHaveLength(1);
  expect(pod.items[0].metadata.labels).toMatchObject({
    "flowai.executor_id": suite.executorID,
    "flowai.team_id": suite.teamA.admin.team_id,
    "flowai.task_id": task.task_id,
    "flowai.executor_scope": "team",
    "flowai.runtime": "k8s",
  });
  expect(pod.items[0].spec.containers[0].image).toBe(
    "localhost/flowai/mock-openhands:v0005-e2e",
  );
});
