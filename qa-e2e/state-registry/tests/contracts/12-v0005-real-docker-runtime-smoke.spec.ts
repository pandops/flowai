import { execFile } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import * as net from "node:net";
import { resolve } from "node:path";
import { promisify } from "node:util";

import { test, expect } from "@playwright/test";
import { startExecutorContainer } from "../../fixtures/executor_container";
import {
  gatewayFor,
  listenerFor,
  systemAdministrator,
} from "../../fixtures/identities";
import { startRegistryWorker } from "../../fixtures/registry_worker";
import {
  startMockedProxy,
  type MockedProxy,
} from "../../../web-ui/fixtures/mocked_proxy";
import {
  bootstrapTeam,
  ingestPendingTask,
  imageReferenceFromDigest,
} from "./_setup";

const exec = promisify(execFile);
const realImage = "localhost/agent-openhands-image:latest";
const repoRoot = resolve(__dirname, "..", "..", "..", "..");
test.use({ trace: "off", video: "off", screenshot: "off" });

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

test("v0005.4 / v0006.23-.28/.31-.33 Docker OpenHands Mission Control runtime", async ({
  page,
}) => {
  test.setTimeout(300_000);
  const registry = await startRegistryWorker();
  const cacheDir = mkdtempSync("/tmp/flowai-v0005-docker-cache-");
  const mockPort = await reservePort();
  const mock = execFile("node", ["qa-e2e/agent-openhands-image/mock-llm.mjs"], {
    cwd: repoRoot,
    env: { ...process.env, FLOWAI_MOCK_LLM_PORT: String(mockPort) },
  });
  let executor: Awaited<ReturnType<typeof startExecutorContainer>> | undefined;
  let proxy: MockedProxy | undefined;
  let containerID = "";
  let cacheVolumeName = "";
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
      "qa-e2e/agent-openhands-image/Dockerfile",
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
    const suffix = crypto
      .randomUUID()
      .replaceAll("-", "")
      .slice(0, 10)
      .toUpperCase();
    const envKey = `FLOWAI_ENV_${suffix}`;
    const secretKey = `FLOWAI_SECRET_${suffix}`;
    const envValue = `env-${suffix.toLowerCase()}`;
    const secretValue = `secret-${crypto.randomUUID()}`;
    proxy = await startMockedProxy(
      [{ team_id: team.admin.team_id, team_name: team.admin.team_name }],
      resolve(repoRoot, "svc/web-ui/web/index.html"),
      registry.baseUrl,
    );
    await page.goto(`${proxy.baseUrl}?view=parameters`);
    const addVariable = async (key: string, value: string) => {
      await page.getByRole("button", { name: "Add variable" }).click();
      await page.locator("#env-dialog").getByLabel("Key name").fill(key);
      await page
        .locator("#env-dialog")
        .getByLabel("Value", { exact: true })
        .fill(value);
      await page
        .locator("#env-dialog")
        .getByRole("button", { name: "Save" })
        .click();
    };
    const addSecret = async (key: string, value: string) => {
      await page.getByRole("button", { name: "Add secret" }).first().click();
      await page.locator("#secret-dialog").getByLabel("Secret key").fill(key);
      await page
        .locator("#secret-dialog")
        .getByLabel("New secret value")
        .fill(value);
      await page
        .locator("#secret-dialog")
        .getByRole("button", { name: "Save secret" })
        .click();
    };
    await addVariable("OPENAI_MODEL", "openai/flowai-mock");
    await addVariable(
      "OPENAI_BASE_URL",
      `http://host.containers.internal:${mockPort}/v1`,
    );
    await addVariable(envKey, envValue);
    await addSecret("OPENAI_API_KEY", "flowai-placeholder-key");
    await addSecret(secretKey, secretValue);
    await expect(page.getByText(`${envKey}=${envValue}`)).toBeVisible();
    await expect(page.getByText(`${secretKey} ••••••••`)).toBeVisible();
    await expect(page.getByText(secretValue)).toHaveCount(0);
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
        payload: {
          prompt: "FLOWAI_HOLD_CAPACITY Write the FlowAI marker and finish.",
        },
        image: exactImage,
      },
    );
    const executorOptions = {
      registryUrl: registry.containerBaseUrl,
      networkName: registry.networkName,
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
      openHandsLLMBaseURL: `http://host.containers.internal:${mockPort}/v1`,
      openHandsLLMUsageID: "flowai-executor",
      finishedCleanupDelay: "15s",
      cacheDir,
    } as const;
    executor = await startExecutorContainer(executorOptions);
    cacheVolumeName = executor.cacheVolumeName ?? "";

    await expect
      .poll(
        async () => {
          const response = await fetch(
            `${registry.baseUrl}/ui/v1/teams/${team.admin.team_id}/tasks/${task.task_id}`,
          );
          return ((await response.json()) as { current_state: string })
            .current_state;
        },
        { timeout: 30_000 },
      )
      .toBe("running");
    await page.goto(`${proxy.baseUrl}?view=task&id=${task.task_id}`);
    const liveReasoning = page
      .locator(".log-line.reasoning")
      .filter({ hasText: "Проверяю рабочее дерево" });
    await expect(liveReasoning).toHaveCount(0);
    try {
      await expect(liveReasoning).toHaveCount(1, { timeout: 20_000 });
    } catch (error) {
      const child = await docker([
        "ps",
        "--filter",
        `label=flowai.task_id=${task.task_id}`,
        "--format",
        "{{.ID}}",
      ]);
      const childID = child.stdout.trim().split("\n")[0] ?? "";
      const childLogs = childID
        ? (
            await docker(["logs", childID]).catch(({ message }) => ({
              stdout: String(message),
            }))
          ).stdout
        : "child container not running";
      throw new Error(
        `${String(error)}\nexecutor logs:\n${executor.redactedLogs()}\nchild logs:\n${childLogs}`,
      );
    }
    const queuedTask = await ingestPendingTask(
      listenerFor({
        teamId: team.admin.team_id,
        listenerIdentity: team.listenerIdentity,
        sourceSystemId: team.sourceSystem.source_system_id,
      }),
      registry.baseUrl,
      {
        team_id: team.admin.team_id,
        source_system_id: team.sourceSystem.source_system_id,
        source_id: `real-docker-capacity-${Date.now().toString(36)}`,
        task_type_id: team.taskType.task_type_id,
        payload: { prompt: "Finish after capacity becomes available." },
        image: exactImage,
      },
    );
    await new Promise((resolve) => setTimeout(resolve, 2_000));
    const queuedProjection = (await (
      await fetch(
        `${registry.baseUrl}/ui/v1/teams/${team.admin.team_id}/tasks/${queuedTask.task_id}`,
      )
    ).json()) as { current_state: string; executor_id: string | null };
    expect(queuedProjection).toMatchObject({
      current_state: "pending",
      executor_id: null,
    });
    const queuedEvents = (await (
      await fetch(
        `${registry.baseUrl}/ui/v1/teams/${team.admin.team_id}/tasks/${queuedTask.task_id}/events?limit=10`,
      )
    ).json()) as { items: unknown[] };
    expect(queuedEvents.items).toEqual([]);
    await page.goto(`${proxy.baseUrl}?view=history&status=pending`);
    await page.getByRole("button", { name: queuedTask.task_id }).click();
    await expect(
      page.getByText(
        "Waiting for a successful claim. There are no events yet.",
      ),
    ).toBeVisible();
    await page.goto(`${proxy.baseUrl}?view=task&id=${task.task_id}`);

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
          const environment = await docker(["exec", containerID, "env"]);
          expect(environment.stdout.split("\n")).toContain(
            `${envKey}=${envValue}`,
          );
          expect(environment.stdout.split("\n")).toContain(
            `${secretKey}=${secretValue}`,
          );
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
    await expect(
      page
        .locator(".log-line.work")
        .filter({ hasText: "flowai-real-runtime.marker" }),
    ).toHaveCount(1, { timeout: 20_000 });
    await expect(
      page.locator(".log-line.work").filter({ hasText: "Task complete." }),
    ).toHaveCount(1, { timeout: 20_000 });

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
      await expect
        .poll(
          async () => {
            const response = await fetch(
              `${registry.baseUrl}/ui/v1/teams/${team.admin.team_id}/tasks/${queuedTask.task_id}`,
            );
            return ((await response.json()) as { current_state: string })
              .current_state;
          },
          { timeout: 90_000 },
        )
        .toBe("finished");
      await page.goto(`${proxy.baseUrl}?view=task&id=${queuedTask.task_id}`);
      await expect(page.locator(".timeline li strong")).toHaveText([
        "task.lifecycle.created",
        "task.lifecycle.running",
        "task.lifecycle.finished",
      ]);

      await page.goto(`${proxy.baseUrl}?view=executors`);
      await expect(page.getByRole("combobox", { name: "Team" })).toHaveValue(
        team.admin.team_id,
      );
      await expect(
        page.getByRole("button", { name: executor.executorId }),
      ).toBeVisible();
      await page.goto(`${proxy.baseUrl}?view=history`);
      await page.getByRole("button", { name: task.task_id }).click();
      await expect(
        page.getByRole("button", { name: executor.executorId }),
      ).toBeVisible();
      await expect(page.locator(".timeline li strong")).toHaveText([
        "task.lifecycle.created",
        "task.lifecycle.running",
        "task.lifecycle.finished",
      ]);
      await page.reload();
      await expect(
        page.getByRole("heading", { name: "Task details" }),
      ).toBeVisible();
      await expect(page.locator(".timeline li strong")).toHaveText([
        "task.lifecycle.created",
        "task.lifecycle.running",
        "task.lifecycle.finished",
      ]);
      await page.goto(
        `${proxy.baseUrl}?view=executor&id=${executor.executorId}`,
      );
      await expect(
        page.getByRole("heading", { name: "Executor details" }),
      ).toBeVisible();
      await expect(page.getByText("executor_docker_openhands")).toBeVisible();

      await page.goto(`${proxy.baseUrl}?view=parameters`);
      page.once("dialog", (dialog) => dialog.accept());
      await page.getByRole("button", { name: `Delete ${envKey}` }).click();
      page.once("dialog", (dialog) => dialog.accept());
      await page
        .locator(".token.secret")
        .filter({ hasText: secretKey })
        .getByRole("button", { name: "Delete" })
        .click();
      await expect(page.getByText(new RegExp(`^${envKey}=`))).toHaveCount(0);
      await expect(page.getByText(`${secretKey} ••••••••`)).toHaveCount(0);

      const completedContainers = (
        await docker([
          "ps",
          "--all",
          "--quiet",
          "--filter",
          `label=flowai.executor_id=${executor.executorId}`,
        ])
      ).stdout
        .trim()
        .split("\n")
        .filter(Boolean);
      for (const completedContainer of completedContainers)
        await docker(["rm", "--force", completedContainer]).catch(
          () => undefined,
        );
      containerID = "";
      const secondTask = await ingestPendingTask(
        listenerFor({
          teamId: team.admin.team_id,
          listenerIdentity: team.listenerIdentity,
          sourceSystemId: team.sourceSystem.source_system_id,
        }),
        registry.baseUrl,
        {
          team_id: team.admin.team_id,
          source_system_id: team.sourceSystem.source_system_id,
          source_id: `real-docker-removed-${Date.now().toString(36)}`,
          task_type_id: team.taskType.task_type_id,
          payload: { prompt: "Finish without removed variables." },
          image: exactImage,
        },
      );
      await expect
        .poll(
          async () => {
            const listed = await docker([
              "ps",
              "--filter",
              `label=flowai.task_id=${secondTask.task_id}`,
              "--format",
              "{{.ID}}",
            ]);
            containerID = listed.stdout.trim().split("\n")[0] ?? "";
            return containerID;
          },
          { timeout: 30_000 },
        )
        .not.toBe("");
      const secondEnvironment = (
        await docker(["exec", containerID, "env"])
      ).stdout.split("\n");
      expect(
        secondEnvironment.some((entry) => entry.startsWith(`${envKey}=`)),
      ).toBe(false);
      expect(
        secondEnvironment.some((entry) => entry.startsWith(`${secretKey}=`)),
      ).toBe(false);
    } finally {
      await gateway.dispose();
    }
    const firstExecutorID = executor.executorId;
    await executor.teardown();
    await new Promise((resolve) => setTimeout(resolve, 750));
    executor = await startExecutorContainer(executorOptions);
    expect(executor.executorId).toBe(firstExecutorID);
  } finally {
    await proxy?.close().catch(() => undefined);
    await executor?.teardown().catch(() => undefined);
    if (containerID) {
      await docker(["rm", "--force", containerID]).catch(() => undefined);
    }
    mock.kill("SIGTERM");
    await registry.teardown().catch(() => undefined);
    if (cacheVolumeName) {
      await docker(["volume", "rm", "--force", cacheVolumeName]).catch(
        () => undefined,
      );
    }
    rmSync(cacheDir, { recursive: true, force: true });
  }
});
