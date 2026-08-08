import { execFile } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import * as net from "node:net";
import { resolve } from "node:path";
import { promisify } from "node:util";

import { test, expect } from "@playwright/test";
import { startExecutorBinary } from "../../fixtures/executor_binary";
import {
  gatewayFor,
  listenerFor,
  systemAdministrator,
} from "../../fixtures/identities";
import { startRegistryWorker } from "../../fixtures/registry_worker";
import {
  bootstrapTeam,
  ingestPendingTask,
  imageReferenceFromDigest,
} from "./_setup";

const exec = promisify(execFile);
const realImage = "localhost/agent-openhands-image:latest";
const repoRoot = resolve(__dirname, "..", "..", "..", "..");

async function docker(args: string[]) {
  return await exec("docker", args, { cwd: repoRoot });
}

async function reservePort(): Promise<number> {
  return await new Promise<number>((resolve, reject) => {
    const server = net.createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      if (!address || typeof address === "string") {
        reject(new Error("failed to reserve mock LLM port"));
        return;
      }
      server.close((error) => (error ? reject(error) : resolve(address.port)));
    });
  });
}

test("v0005.4 Docker Executor uses the shared real OpenHands runtime smoke", async () => {
  test.setTimeout(180_000);
  const registry = await startRegistryWorker();
  const cacheDir = mkdtempSync("/tmp/flowai-v0005-docker-cache-");
  const mockPort = await reservePort();
  const mock = execFile(
    "node",
    ["autotest/agent-openhands-image/mock-llm.mjs"],
    {
      cwd: repoRoot,
      env: { ...process.env, FLOWAI_MOCK_LLM_PORT: String(mockPort) },
    },
  );
  let executor: Awaited<ReturnType<typeof startExecutorBinary>> | undefined;
  let containerID = "";
  try {
    const mockDeadline = Date.now() + 10_000;
    while (Date.now() < mockDeadline) {
      try {
        if ((await fetch(`http://127.0.0.1:${mockPort}/health`)).ok) break;
      } catch {}
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    await docker([
      "build",
      "--tag",
      realImage,
      "--file",
      "autotest/agent-openhands-image/Dockerfile",
      ".",
    ]);
    const inspected = await docker([
      "image",
      "inspect",
      realImage,
      "--format",
      "{{json .RepoDigests}}",
    ]);
    const repoDigest = (JSON.parse(inspected.stdout.trim()) as string[]).find(
      (value) => value.includes("@"),
    );
    if (!repoDigest)
      throw new Error(`${realImage} has no immutable RepoDigest`);
    const separator = repoDigest.lastIndexOf("@");
    const exactImage = imageReferenceFromDigest(
      realImage,
      repoDigest.slice(separator + 1),
    );
    const team = await bootstrapTeam(systemAdministrator(), registry.baseUrl, {
      teamName: "v0005-real-docker-runtime",
      executionTag: "openhands",
      defaultImage: exactImage,
    });
    const task = await ingestPendingTask(
      listenerFor({
        teamId: team.admin.team_id,
        listenerIdentity: team.listenerIdentity,
        sourceSystemId: team.sourceSystem.source_system_id,
      }),
      registry.baseUrl,
      {
        team_id: team.admin.team_id,
        source_system_id: team.sourceSystem.source_system_id,
        source_id: `real-docker-${Date.now().toString(36)}`,
        task_type_id: team.taskType.task_type_id,
        payload: { prompt: "Write the FlowAI marker and finish." },
        image: exactImage,
      },
    );
    const executorOptions = {
      registryUrl: registry.baseUrl,
      scope: "team",
      teamId: team.admin.team_id,
      authorizedTag: "openhands",
      dockerSocket:
        process.env.FLOWAI_DOCKER_SOCKET ?? "/run/user/1000/podman/podman.sock",
      maxContainers: 1,
      pollIntervalMs: 100,
      openHandsImage: realImage,
      openHandsAgentProfileId: "",
      openHandsLLMModel: "openai/flowai-mock",
      openHandsLLMAPIKey: "flowai-placeholder-key",
      openHandsLLMBaseURL: `http://host.docker.internal:${mockPort}/v1`,
      openHandsLLMUsageID: "flowai-executor",
      finishedCleanupDelay: "15s",
      cacheDir,
    } as const;
    executor = await startExecutorBinary(executorOptions);

    const markerDeadline = Date.now() + 90_000;
    let marker = "";
    while (Date.now() < markerDeadline && marker === "") {
      const listed = await docker([
        "ps",
        "--filter",
        `label=flowai.task_id=${task.task_id}`,
        "--format",
        "{{.ID}}",
      ]);
      containerID = listed.stdout.trim().split("\n")[0] ?? "";
      if (containerID) {
        try {
          const result = await docker([
            "exec",
            containerID,
            "cat",
            "/workspace/project/flowai-real-runtime.marker",
          ]);
          marker = result.stdout.trim();
        } catch {}
      }
      if (!marker) await new Promise((resolve) => setTimeout(resolve, 250));
    }
    if (marker !== "flowai-real-runtime-ok") {
      throw new Error(
        `real Docker marker was not written; executor logs:\n${executor.redactedLogs().slice(-8_000)}`,
      );
    }

    const gateway = await gatewayFor({
      teamId: team.admin.team_id,
      operatorId: "v0005-real-docker",
      teamName: team.admin.team_name,
    }).api(registry.baseUrl);
    try {
      const eventDeadline = Date.now() + 30_000;
      let eventTypes: string[] = [];
      while (Date.now() < eventDeadline) {
        const response = await gateway.get(`/v1/tasks/${task.task_id}/events`);
        const body = (await response.json()) as {
          items: Array<{ event_type: string }>;
        };
        eventTypes = body.items.map(({ event_type }) => event_type);
        if (eventTypes.includes("finished")) break;
        await new Promise((resolve) => setTimeout(resolve, 250));
      }
      expect(eventTypes).toEqual(["created", "running", "finished"]);
    } finally {
      await gateway.dispose();
    }
    const firstExecutorID = executor.executorId;
    await executor.teardown();
    executor = await startExecutorBinary(executorOptions);
    expect(executor.executorId).toBe(firstExecutorID);
  } finally {
    await executor?.teardown().catch(() => undefined);
    if (containerID) {
      await docker(["rm", "--force", containerID]).catch(() => undefined);
    }
    mock.kill("SIGTERM");
    await registry.teardown().catch(() => undefined);
    rmSync(cacheDir, { recursive: true, force: true });
  }
});
