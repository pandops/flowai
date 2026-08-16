// Container-only lifecycle for the real Docker OpenHands Executor. Test
// doubles stay in-process; the FlowAI service itself always runs from the
// image built from the current checkout.
import { createHash, randomBytes } from "node:crypto";
import { execFile, execFileSync } from "node:child_process";
import { promisify } from "node:util";
import { resolve as pathResolve } from "node:path";
import { request, type APIRequestContext } from "@playwright/test";
import { preflightSocket } from "../../containerized-runtime-services/fixtures/socket-preflight";

const exec = promisify(execFile);
const repoRoot = pathResolve(__dirname, "../../..");
const imageTag = "flowai-e2e-executor-docker-openhands";
let imagePromise: Promise<string> | undefined;

export interface ExecutorStartOptions {
  serviceImage?: string;
  networkName?: string;
  bindHost?: string;
  bindPort?: number;
  registryUrl?: string;
  legacyTLSServerCert?: string;
  legacyTLSServerKey?: string;
  legacyTLSClientCA?: string;
  legacyTLSRequireClientCert?: string;
  scope?: "team" | "system";
  teamId?: string;
  authorizedTag?: string;
  localImage?: string;
  dockerSocket?: string;
  pollIntervalMs?: number;
  maxContainers?: number;
  openHandsImage?: string;
  openHandsAgentProfileId?: string;
  openHandsLLMModel?: string;
  openHandsLLMAPIKey?: string;
  openHandsLLMBaseURL?: string;
  openHandsLLMUsageID?: string;
  finishedCleanupDelay?: string;
  cacheDir?: string;
}

export interface ExecutorHandles {
  containerId: string;
  bindHost: string;
  bindPort: number;
  baseUrl: string;
  probe: APIRequestContext;
  executorId: string;
  v0002Options: Readonly<Record<string, string>>;
  cacheVolumeName?: string;
  redactedLogs(): string;
  teardown(): Promise<void>;
}

async function run(args: string[], timeout = 600_000): Promise<string> {
  const { stdout } = await exec("docker", args, {
    cwd: repoRoot,
    timeout,
    maxBuffer: 8 * 1024 * 1024,
  });
  return stdout.trim();
}

async function imageID(): Promise<string> {
  imagePromise ??= (async () => {
    await run([
      "build",
      "--tag",
      imageTag,
      "--file",
      "executor/docker_openhands/Containerfile",
      ".",
    ]);
    const id = await run(["image", "inspect", imageTag, "--format", "{{.Id}}"]);
    return id.startsWith("sha256:") ? id : `sha256:${id}`;
  })();
  return await imagePromise;
}

async function waitReady(
  baseUrl: string,
  containerID: string,
): Promise<APIRequestContext> {
  const api = await request.newContext({ baseURL: baseUrl });
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    try {
      if ((await api.get("/v1/livez")).ok()) return api;
    } catch {}
    const running = await run([
      "inspect",
      containerID,
      "--format",
      "{{.State.Running}}",
    ] as string[]).catch(() => "false");
    if (running !== "true") {
      const logs = await run(["logs", containerID]).catch(() => "");
      await api.dispose();
      throw new Error(`executor container exited before readiness: ${logs}`);
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  await api.dispose();
  throw new Error(`executor container did not expose /v1/livez at ${baseUrl}`);
}

export async function startExecutorContainer(
  opts: ExecutorStartOptions = {},
): Promise<ExecutorHandles> {
  const endpoint = process.env.DOCKER_HOST ?? "";
  const configuredSocket =
    opts.dockerSocket ??
    process.env.FLOWAI_DOCKER_SOCKET ??
    (endpoint.startsWith("unix://") ? endpoint.slice(7) : "");
  const socket = await preflightSocket(configuredSocket);
  const image = opts.serviceImage ?? (await imageID());
  const cacheVolumeName = opts.cacheDir
    ? `flowai-executor-cache-${createHash("sha256").update(opts.cacheDir).digest("hex").slice(0, 16)}`
    : undefined;
  const name = `flowai-executor-${Date.now()}-${randomBytes(4).toString("hex")}`;
  const env: Record<string, string> = {
    EXECUTOR_API_BIND: "0.0.0.0:8020",
    EXECUTOR_STATE_REGISTRY_URL: (
      opts.registryUrl ?? "http://host.containers.internal:1"
    ).replace("127.0.0.1", "host.containers.internal"),
    EXECUTOR_SCOPE: opts.scope ?? "system",
    EXECUTOR_TEAM_ID: opts.teamId ?? "",
    EXECUTOR_AUTHORIZED_TAG: opts.authorizedTag ?? "openhands",
    EXECUTOR_MAX_CONTAINERS: String(opts.maxContainers ?? 2),
    EXECUTOR_POLL_INTERVAL: `${opts.pollIntervalMs ?? 1000}ms`,
    EXECUTOR_CACHE_DIR: "/tmp/flowai-cache",
    DOCKER_SOCKET_PATH: socket.hostPath,
    DOCKER_PUBLISHED_HOST: "host.containers.internal",
    OPENHANDS_IMAGE:
      opts.openHandsImage ??
      opts.localImage ??
      "ghcr.io/openhands/agent-server:latest-python",
    OPENHANDS_AGENT_PROFILE_ID:
      opts.openHandsAgentProfileId ?? "flowai-default",
    OPENHANDS_LLM_MODEL: opts.openHandsLLMModel ?? "",
    OPENHANDS_LLM_API_KEY: opts.openHandsLLMAPIKey ?? "",
    OPENHANDS_LLM_BASE_URL: (opts.openHandsLLMBaseURL ?? "").replace(
      "host.docker.internal",
      "host.containers.internal",
    ),
    OPENHANDS_LLM_USAGE_ID: opts.openHandsLLMUsageID ?? "",
    EXECUTOR_FINISHED_CLEANUP_DELAY: opts.finishedCleanupDelay ?? "0s",
  };
  const args = [
    "run",
    "--detach",
    "--name",
    name,
    "--publish",
    "127.0.0.1::8020",
  ];
  if (opts.networkName) args.push("--network", opts.networkName);
  if ((process.env.DOCKER_HOST ?? "").includes("podman"))
    args.push("--annotation", "run.oci.keep_original_groups=1");
  args.push(
    "--group-add",
    String(socket.gid),
    "--mount",
    `type=bind,source=${socket.hostPath},target=${socket.hostPath}`,
  );
  if (cacheVolumeName)
    args.push(
      "--mount",
      `type=volume,source=${cacheVolumeName},target=/tmp/flowai-cache`,
    );
  for (const [key, value] of Object.entries(env))
    args.push("--env", `${key}=${value}`);
  args.push(image);
  let id = "";
  try {
    id = await run(args);
    let portText = await run(["port", id, "8020/tcp"]);
    let match = portText.match(/127\.0\.0\.1:(\d+)/);
    if (!match) {
      const ports = JSON.parse(
        await run([
          "inspect",
          id,
          "--format",
          "{{json .NetworkSettings.Ports}}",
        ] as string[]),
      ) as Record<string, Array<{ HostPort?: string }> | null>;
      const hostPort = ports["8020/tcp"]?.[0]?.HostPort;
      if (hostPort)
        match = [
          `127.0.0.1:${hostPort}`,
          hostPort,
        ] as unknown as RegExpMatchArray;
    }
    if (!match) throw new Error(`executor published port missing: ${portText}`);
    const bindPort = Number(match[1]);
    const baseUrl = `http://127.0.0.1:${bindPort}`;
    const probe = await waitReady(baseUrl, id);
    const live = (await (await probe.get("/v1/livez")).json()) as {
      executor_id?: string;
    };
    const executorId = live.executor_id ?? uniqueExecutorId();
    let stopped = false;
    return {
      containerId: id,
      bindHost: "127.0.0.1",
      bindPort,
      baseUrl,
      probe,
      executorId,
      v0002Options: Object.freeze({ ...env }),
      cacheVolumeName,
      redactedLogs: () => {
        try {
          return execFileSync("docker", ["logs", id], {
            encoding: "utf8",
            maxBuffer: 8 * 1024 * 1024,
          });
        } catch (error) {
          return String(error);
        }
      },
      teardown: async () => {
        if (stopped) return;
        stopped = true;
        await probe.dispose();
        await run(["rm", "--force", id]).catch(() => undefined);
      },
    };
  } catch (error) {
    if (id) await run(["rm", "--force", id]).catch(() => undefined);
    throw error;
  }
}

export function uniqueExecutorId(): string {
  return `exec-${Date.now().toString(36)}-${randomBytes(3).toString("hex")}`;
}
