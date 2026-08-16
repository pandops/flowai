import { execFile, spawn, type ChildProcess } from "node:child_process";
import { chmod, writeFile } from "node:fs/promises";
import { networkInterfaces } from "node:os";
import path from "node:path";
import { promisify } from "node:util";

import { startExecutorContainer } from "../fixtures/executor_container";
import { systemAdministrator } from "../fixtures/identities";
import { startRegistryWorker } from "../fixtures/registry_worker";
import { startMockedProxy } from "../../web-ui/fixtures/mocked_proxy";
import { bootstrapTeam } from "../tests/contracts/_setup";

const exec = promisify(execFile);
const repoRoot = path.resolve(__dirname, "..", "..", "..");
const statePath = process.env.FLOWAI_DEMO_STATE ?? "/tmp/flowai-demo.json";
const publicPort = Number(process.env.FLOWAI_DEMO_PORT ?? "4173");
const imageRepository =
  process.env.FLOWAI_DEMO_IMAGE ?? "ghcr.io/openhands/agent-server";
const imageTag = process.env.FLOWAI_DEMO_IMAGE_TAG ?? "latest-python";
const dockerSocket =
  process.env.FLOWAI_DOCKER_SOCKET ?? "/run/user/1000/podman/podman.sock";

async function imageDigest(): Promise<string> {
  const reference = `${imageRepository}:${imageTag}`;
  try {
    const inspected = await exec(
      "docker",
      ["image", "inspect", reference, "--format", "{{.Digest}}"],
      { cwd: repoRoot },
    );
    if (inspected.stdout.trim().startsWith("sha256:"))
      return inspected.stdout.trim();
  } catch {}
  process.stdout.write(`Загружаем OpenHands image ${reference}…\n`);
  await exec("docker", ["pull", reference], {
    cwd: repoRoot,
    maxBuffer: 20 * 1024 * 1024,
  });
  const inspected = await exec(
    "docker",
    ["image", "inspect", reference, "--format", "{{.Digest}}"],
    { cwd: repoRoot },
  );
  const digest = inspected.stdout.trim();
  if (!digest.startsWith("sha256:"))
    throw new Error(`image ${reference} has no immutable digest`);
  return digest;
}

function lanAddresses(): string[] {
  return Object.values(networkInterfaces()).flatMap((entries) =>
    (entries ?? [])
      .filter((entry) => entry.family === "IPv4" && !entry.internal)
      .map((entry) => entry.address),
  );
}

async function main(): Promise<void> {
  const digest = await imageDigest();
  const registry = await startRegistryWorker();
  const team = await bootstrapTeam(systemAdministrator(), registry.baseUrl, {
    teamName: "FlowAI Demo",
    executionTag: "openhands",
    defaultImage: { repository: imageRepository, digest },
  });
  const createDefinition = await fetch(
    `${registry.baseUrl}/ui/v1/teams/${team.admin.team_id}/launch-parameters`,
    {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        name: "Параметры команды",
        scope: "team",
        task_type_id: null,
        env: {},
        image: null,
      }),
    },
  );
  if (!createDefinition.ok)
    throw new Error(
      `cannot create demo launch parameters: ${createDefinition.status} ${await createDefinition.text()}`,
    );

  const executor = await startExecutorContainer({
    registryUrl: registry.containerBaseUrl,
    networkName: registry.networkName,
    scope: "team",
    teamId: team.admin.team_id,
    authorizedTag: "openhands",
    dockerSocket,
    maxContainers: 1,
    pollIntervalMs: 500,
    openHandsImage: `${imageRepository}:${imageTag}`,
    openHandsAgentProfileId: "",
    openHandsLLMModel: "",
    openHandsLLMAPIKey: "",
    openHandsLLMBaseURL: "",
    openHandsLLMUsageID: "flowai-demo",
    finishedCleanupDelay: "30s",
  });
  const proxy = await startMockedProxy(
    [{ team_id: team.admin.team_id, team_name: team.admin.team_name }],
    path.resolve(repoRoot, "svc/web-ui/web/index.html"),
    registry.baseUrl,
  );
  const proxyPort = new URL(proxy.baseUrl).port;
  const forwarder = spawn(
    "socat",
    [
      `TCP-LISTEN:${publicPort},bind=0.0.0.0,reuseaddr,fork`,
      `TCP:127.0.0.1:${proxyPort}`,
    ],
    { stdio: "inherit" },
  );
  const state = {
    registry_url: registry.baseUrl,
    team_id: team.admin.team_id,
    team_name: team.admin.team_name,
    source_system_id: team.sourceSystem.source_system_id,
    listener_identity: team.listenerIdentity,
    task_type_id: team.taskType.task_type_id,
  };
  await writeFile(statePath, JSON.stringify(state, null, 2), { mode: 0o600 });
  await chmod(statePath, 0o600);

  process.stdout.write(`\nFlowAI demo запущен.\n`);
  for (const address of lanAddresses())
    process.stdout.write(`UI: http://${address}:${publicPort}\n`);
  process.stdout.write(
    `В разделе «Параметры запуска» добавьте OPENAI_MODEL и OPENAI_BASE_URL как переменные, а OPENAI_API_KEY как секрет.\n`,
  );
  process.stdout.write(
    `Затем отправьте промпт: npm run demo:prompt -- "ваш промпт"\n\n`,
  );

  let stopping = false;
  const stop = async () => {
    if (stopping) return;
    stopping = true;
    forwarder.kill("SIGTERM");
    await proxy.close().catch(() => undefined);
    await executor.teardown().catch(() => undefined);
    await registry.teardown().catch(() => undefined);
    process.exit(0);
  };
  process.on("SIGINT", stop);
  process.on("SIGTERM", stop);
  await new Promise<void>((resolve, reject) => {
    forwarder.once("error", reject);
    forwarder.once("exit", (code) =>
      code === 0 || stopping
        ? resolve()
        : reject(new Error(`socat exited with ${code}`)),
    );
  });
}

main().catch((error) => {
  process.stderr.write(
    `${error instanceof Error ? error.stack : String(error)}\n`,
  );
  process.exit(1);
});
