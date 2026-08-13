import { expect, test } from "../fixtures/k3d-suite";

test("v0005.4 real OpenHands runtime writes the deterministic workspace marker", async ({
  suite,
}) => {
  test.setTimeout(300_000);
  const task = await suite.ingestTask(
    suite.teamA,
    { prompt: "Write the FlowAI marker and finish." },
    undefined,
    suite.realAgentImage,
  );
  const deadline = Date.now() + 180_000;
  let eventTypes: string[] = [];
  let podName = "";
  while (Date.now() < deadline) {
    let pods: { items: Array<{ metadata: { name: string } }> } = {
      items: [],
    };
    try {
      pods = JSON.parse(
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
      ) as typeof pods;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 250));
      continue;
    }
    podName = pods.items[0]?.metadata.name ?? podName;
    const response = await suite.gatewayFetch(
      suite.teamA,
      `/v1/tasks/${task.task_id}/events`,
    );
    const body = (await response.json()) as {
      items: Array<{ event_type: string }>;
    };
    eventTypes = body.items.map(({ event_type }) => event_type);
    if (eventTypes.includes("finished")) break;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  expect(eventTypes).toEqual(["created", "running", "finished"]);
  expect(podName).not.toBe("");
  const marker = await suite.kubectl(
    "exec",
    "-n",
    "flowai-executor-k8s",
    podName,
    "--",
    "cat",
    "/workspace/project/flowai-real-runtime.marker",
  );
  expect(marker).toBe("flowai-real-runtime-ok");
});
