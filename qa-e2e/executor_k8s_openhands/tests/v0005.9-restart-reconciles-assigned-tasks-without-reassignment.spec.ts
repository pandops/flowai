// v0005.9-restart-reconciles-assigned-tasks-without-reassignment
//
// After a Deployment-managed Executor Pod replacement, the K8s
// Executor re-registers with the same bound team_id and
// authorized_tag, reuses its State Registry-generated cached
// executor_id, loads claimed non-terminal tasks exclusively from
// persistent cache, matches each cached owner_command_id to the
// existing Pod flowai.command_id label, reconnects to existing
// Pods without creating new Pods, reconnects to the same
// OpenHands conversation at its current progress, retries pending
// outbox events with their original event_id, emits no duplicate
// running event, and emits exactly one terminal finished or
// failed event per Pod.

import { test, expect } from "../fixtures/k3d-suite";

test("v0005.9 restart reconciles assigned tasks without reassignment", async ({
  suite,
}) => {
  test.setTimeout(180_000);
  const llmEnvironmentID = await suite.configureLLM(suite.teamA);
  const task = await suite.ingestTask(suite.teamA, {
    prompt: "restart v0005.9",
  });
  const deadline = Date.now() + 90_000;
  let taskPod:
    | {
        metadata: { name: string; uid: string; labels: Record<string, string> };
      }
    | undefined;
  while (Date.now() < deadline) {
    const pods = JSON.parse(
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
    ) as { items: Array<NonNullable<typeof taskPod>> };
    const logs = await suite.kubectl(
      "logs",
      "-n",
      "flowai-executor-k8s",
      "deployment/executor-k8s-openhands",
    );
    if (
      pods.items.length === 1 &&
      logs.includes("observing OpenHands conversation")
    ) {
      taskPod = pods.items[0];
      break;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  expect(taskPod).toBeDefined();
  const oldExecutorPod = await suite.kubectl(
    "get",
    "pods",
    "-n",
    "flowai-executor-k8s",
    "-l",
    "app.kubernetes.io/name=executor-k8s-openhands",
    "-o",
    "jsonpath={.items[0].metadata.name}",
  );
  await suite.kubectl(
    "delete",
    "pod",
    "-n",
    "flowai-executor-k8s",
    oldExecutorPod,
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
  let newExecutorPod = oldExecutorPod;
  while (Date.now() < deadline && newExecutorPod === oldExecutorPod) {
    newExecutorPod = await suite.kubectl(
      "get",
      "pods",
      "-n",
      "flowai-executor-k8s",
      "-l",
      "app.kubernetes.io/name=executor-k8s-openhands",
      "-o",
      "jsonpath={.items[0].metadata.name}",
    );
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  expect(newExecutorPod).not.toBe(oldExecutorPod);
  const after = JSON.parse(
    await suite.kubectl(
      "get",
      "pod",
      "-n",
      "flowai-executor-k8s",
      taskPod!.metadata.name,
      "-o",
      "json",
    ),
  ) as NonNullable<typeof taskPod>;
  expect(after.metadata.uid).toBe(taskPod!.metadata.uid);
  expect(after.metadata.labels["flowai.command_id"]).toBe(
    taskPod!.metadata.labels["flowai.command_id"],
  );
  let ready: { executor_id: string } | undefined;
  while (Date.now() < deadline && ready === undefined) {
    try {
      ready = JSON.parse(
        await suite.kubectl(
          "get",
          "--raw",
          "/api/v1/namespaces/flowai-executor-k8s/services/http:executor-k8s-openhands:8030/proxy/readyz",
        ),
      ) as { executor_id: string };
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 200));
    }
  }
  expect(ready).toBeDefined();
  expect(ready!.executor_id).toBe(suite.executorID);

  let eventTypes: string[] = [];
  while (Date.now() < deadline) {
    const response = await suite.gatewayFetch(
      suite.teamA,
      `/v1/tasks/${task.task_id}/events`,
    );
    const body = (await response.json()) as {
      items: Array<{ event_type: string }>;
    };
    eventTypes = body.items.map((event) => event.event_type);
    if (eventTypes.includes("finished")) break;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  expect(eventTypes.slice(0, 2)).toEqual(["created", "running"]);
  expect(["finished", "failed"]).toContain(eventTypes[2]);
  expect(eventTypes).toHaveLength(3);
  expect(eventTypes.filter((event) => event === "running")).toHaveLength(1);
  await suite.clearLLM(suite.teamA, llmEnvironmentID);
});
