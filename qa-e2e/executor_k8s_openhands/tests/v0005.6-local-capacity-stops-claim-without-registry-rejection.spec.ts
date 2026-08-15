// v0005.6-local-capacity-stops-claim-without-registry-rejection
//
// max_capacity = running_count does not cause the Registry to
// reject claim; the Executor alone decides when to start a Pod.

import { test, expect } from "../fixtures/k3d-suite";

test("v0005.6 local capacity stops claim without registry rejection", async ({
  suite,
}) => {
  // The suite intentionally shares one cluster. Remove any terminal Pod left
  // by a preceding assertion and let the running Executor observe the
  // deletion, so this scenario starts with zero actual and local slots.
  await suite.kubectl(
    "delete",
    "pod",
    "-n",
    "flowai-executor-k8s",
    "-l",
    "flowai.runtime=k8s",
    "--ignore-not-found=true",
    "--wait=true",
  );
  await new Promise((resolve) => setTimeout(resolve, 2_000));
  const waitForPodCount = async (want: number): Promise<void> => {
    const deadline = Date.now() + 60_000;
    while (Date.now() < deadline) {
      const pods = JSON.parse(
        await suite.kubectl(
          "get",
          "pods",
          "-n",
          "flowai-executor-k8s",
          "-l",
          "flowai.runtime=k8s",
          "-o",
          "json",
        ),
      ) as { items: unknown[] };
      if (pods.items.length === want) return;
      await new Promise((resolve) => setTimeout(resolve, 200));
    }
    throw new Error(`expected ${want} K8s task Pods`);
  };
  const first = await suite.ingestTask(suite.teamA, {
    prompt: "FLOWAI_HOLD_CAPACITY hold capacity one",
  });
  await waitForPodCount(1);
  const second = await suite.ingestTask(suite.teamA, {
    prompt: "hold capacity two",
  });
  await new Promise((resolve) => setTimeout(resolve, 2_000));

  const firstResponse = await suite.gatewayFetch(
    suite.teamA,
    `/v1/tasks/${first.task_id}`,
  );
  expect((await firstResponse.json()) as Record<string, unknown>).toMatchObject(
    { executor_id: suite.executorID },
  );
  const pendingResponse = await suite.gatewayFetch(
    suite.teamA,
    `/v1/tasks/${second.task_id}`,
  );
  const pending = (await pendingResponse.json()) as Record<string, unknown>;
  expect(pending.owner_command_id).toBeNull();

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
      running_count: 1,
      runtime_metadata: { runtime: "k8s", test: "saturated" },
    }),
  });
  expect(register.status).toBe(201);
  const saturated = (await register.json()) as { executor_id: string };
  const claim = await suite.registryFetch(
    `/v1/executors/${saturated.executor_id}/claim`,
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-FlowAI-Role": "team-executor",
        "X-FlowAI-Team-Id": suite.teamA.admin.team_id,
        "X-FlowAI-Executor-Id": saturated.executor_id,
        "X-FlowAI-Request-Id": crypto.randomUUID(),
      },
      body: JSON.stringify({
        task_id: second.task_id,
        command_id: `cmd-${crypto.randomUUID()}`,
      }),
    },
  );
  expect(claim.status).toBe(200);
  await suite.kubectl(
    "delete",
    "pod",
    "-n",
    "flowai-executor-k8s",
    "-l",
    "flowai.runtime=k8s",
    "--wait=true",
  );
  await new Promise((resolve) => setTimeout(resolve, 2_000));
});
