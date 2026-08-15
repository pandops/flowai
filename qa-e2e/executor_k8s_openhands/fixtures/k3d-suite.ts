import { test as base, expect } from "@playwright/test";
import { type ChildProcess, spawn } from "node:child_process";
import * as fs from "node:fs/promises";
import * as net from "node:net";
import * as os from "node:os";
import * as path from "node:path";

import {
  startRegistryWorker,
  type RegistryWorker,
} from "../../state-registry/fixtures/registry_worker";

type BootstrapResult = {
  admin: { team_id: string; team_name: string };
  sourceSystem: { source_system_id: string; team_id: string };
  taskType: { task_type_id: string; team_id: string; execution_tag: string };
  listenerIdentity: string;
};

export type K3dExecutorSuite = {
  clusterName: string;
  kubeconfig: string;
  artifactsDir: string;
  registry: RegistryWorker;
  registryBaseURL: string;
  executorID: string;
  realAgentImage: { repository: string; digest: string };
  teamA: BootstrapResult;
  teamB: BootstrapResult;
  kubectl: (...args: string[]) => Promise<string>;
  registryFetch: (pathname: string, init?: RequestInit) => Promise<Response>;
  ingestTask: (
    team: BootstrapResult,
    payload?: Record<string, unknown>,
    environmentID?: string,
    image?: { repository: string; digest: string },
  ) => Promise<{ task_id: string }>;
  gatewayFetch: (
    team: BootstrapResult,
    pathname: string,
    init?: RequestInit,
  ) => Promise<Response>;
  installSystemExecutor: () => Promise<{
    executorID: string;
    namespace: string;
  }>;
};

const K3D_VERSION = "v5.9.0";
const NAMESPACE = "flowai-executor-k8s";
const EXECUTOR_IMAGE = "localhost/flowai/executor_k8s_openhands:v0005-e2e";
const AGENT_IMAGE = "localhost/flowai/mock-openhands:v0005-e2e";
const REAL_AGENT_IMAGE = "localhost/agent-openhands-image:latest";
const STORAGE_HELPER_IMAGE = "docker.io/library/busybox:1.36.1";

function freshClusterName(): string {
  const suffix = Math.random().toString(36).slice(2, 8);
  return `flowai-exec-k8s-${Date.now().toString(36)}-${suffix}`;
}

async function reservePort(): Promise<number> {
  return await new Promise<number>((resolve, reject) => {
    const server = net.createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      if (address === null || typeof address === "string") {
        server.close();
        reject(new Error("failed to reserve a TCP port"));
        return;
      }
      server.close((error) => {
        if (error) reject(error);
        else resolve(address.port);
      });
    });
  });
}

async function run(
  command: string,
  args: string[],
  options: { env?: NodeJS.ProcessEnv; timeoutMs?: number } = {},
): Promise<string> {
  return await new Promise<string>((resolve, reject) => {
    const child = spawn(command, args, {
      cwd: path.resolve(__dirname, "..", "..", ".."),
      env: { ...process.env, ...options.env },
      stdio: ["ignore", "pipe", "pipe"],
    });
    const stdout: Buffer[] = [];
    const stderr: Buffer[] = [];
    const timer = setTimeout(() => {
      child.kill("SIGKILL");
      reject(
        new Error(
          `${command} timed out after ${options.timeoutMs ?? 180_000}ms`,
        ),
      );
    }, options.timeoutMs ?? 180_000);
    child.stdout.on("data", (chunk: Buffer) => stdout.push(chunk));
    child.stderr.on("data", (chunk: Buffer) => stderr.push(chunk));
    child.once("error", (error) => {
      clearTimeout(timer);
      reject(error);
    });
    child.once("close", (code) => {
      clearTimeout(timer);
      const out = Buffer.concat(stdout).toString("utf8").trim();
      const err = Buffer.concat(stderr).toString("utf8").trim();
      if (code === 0) {
        resolve(out);
        return;
      }
      reject(
        new Error(
          `${command} ${args.join(" ")} exited ${code}: ${err}\n${out}`,
        ),
      );
    });
  });
}

async function importImage(clusterName: string, image: string): Promise<void> {
  const node = `k3d-${clusterName}-server-0`;
  await new Promise<void>((resolve, reject) => {
    const save = spawn("docker", ["save", image], {
      cwd: path.resolve(__dirname, "..", "..", ".."),
      env: process.env,
      stdio: ["ignore", "pipe", "pipe"],
    });
    const load = spawn(
      "docker",
      [
        "exec",
        "--interactive",
        node,
        "ctr",
        "--namespace",
        "k8s.io",
        "images",
        "import",
        "-",
      ],
      {
        cwd: path.resolve(__dirname, "..", "..", ".."),
        env: process.env,
        stdio: ["pipe", "ignore", "pipe"],
      },
    );
    save.stdout.pipe(load.stdin);
    const errors: Buffer[] = [];
    save.stderr.on("data", (chunk: Buffer) => errors.push(chunk));
    load.stderr.on("data", (chunk: Buffer) => errors.push(chunk));
    const timer = setTimeout(() => {
      save.kill("SIGKILL");
      load.kill("SIGKILL");
      reject(new Error(`streaming image import timed out for ${image}`));
    }, 600_000);
    load.once("close", (code) => {
      clearTimeout(timer);
      if (code === 0) resolve();
      else
        reject(
          new Error(
            `streaming image import failed for ${image}: ${Buffer.concat(errors).toString("utf8")}`,
          ),
        );
    });
  });
}

async function removeNodeDataDir(nodeDataDir: string): Promise<void> {
  try {
    await fs.rm(nodeDataDir, { recursive: true, force: true });
    return;
  } catch {}
  // Rootless Podman writes bind-mounted containerd files with subordinate
  // IDs. Remove those files in the same user namespace when plain rm cannot.
  await run("podman", ["unshare", "rm", "-rf", nodeDataDir]).catch(
    () => undefined,
  );
}

async function bootstrapTeam(
  baseURL: string,
  teamName: string,
  executionTag: string,
): Promise<BootstrapResult> {
  const suffix = Math.random().toString(36).slice(2, 10);
  const listenerIdentity = `listener-${suffix}`;
  const headers = {
    "Content-Type": "application/json",
    "X-FlowAI-Role": "admin",
    "X-FlowAI-Admin-Subject": `admin-${suffix}`,
    "X-Request-Id": `req-${suffix}`,
  };
  const create = async (
    pathname: string,
    body: unknown,
  ): Promise<Record<string, unknown>> => {
    const response = await fetch(`${baseURL}${pathname}`, {
      method: "POST",
      headers,
      body: JSON.stringify(body),
    });
    if (response.status !== 201) {
      throw new Error(
        `${pathname} returned ${response.status}: ${await response.text()}`,
      );
    }
    return (await response.json()) as Record<string, unknown>;
  };
  const team = await create("/admin/teams", {
    team_name: teamName,
    default_image: {
      repository: AGENT_IMAGE,
      digest: `sha256:${"a".repeat(64)}`,
    },
  });
  const teamID = String(team.team_id);
  const sourceSystem = await create("/admin/source-systems", {
    team_id: teamID,
    listener_identity: listenerIdentity,
    default_image: null,
  });
  const taskType = await create("/admin/task-types", {
    team_id: teamID,
    execution_tag: executionTag,
    default_image: null,
  });
  return {
    admin: { team_id: teamID, team_name: String(team.team_name) },
    sourceSystem: {
      source_system_id: String(sourceSystem.source_system_id),
      team_id: String(sourceSystem.team_id),
    },
    taskType: {
      task_type_id: String(taskType.task_type_id),
      team_id: String(taskType.team_id),
      execution_tag: String(taskType.execution_tag),
    },
    listenerIdentity,
  };
}

async function startRegistryProxy(
  registryPort: number,
): Promise<{ server: net.Server; port: number }> {
  const server = net.createServer((downstream) => {
    const upstream = net.connect({ host: "127.0.0.1", port: registryPort });
    downstream.pipe(upstream);
    upstream.pipe(downstream);
    const close = (): void => {
      downstream.destroy();
      upstream.destroy();
    };
    downstream.once("error", close);
    upstream.once("error", close);
  });
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "0.0.0.0", () => resolve());
  });
  const address = server.address();
  if (!address || typeof address === "string") {
    server.close();
    throw new Error("registry proxy did not expose an IPv4 port");
  }
  return { server, port: address.port };
}

async function collectDiagnostics(
  kubeconfig: string,
  artifactsDir: string,
): Promise<void> {
  const commands = [
    [
      "get",
      "nodes,pods,svc,deployments,pvc,pv,storageclass",
      "-A",
      "-o",
      "wide",
    ],
    ["get", "events", "-A", "--sort-by=.lastTimestamp"],
    ["describe", "pods", "-n", NAMESPACE],
    [
      "logs",
      "-n",
      NAMESPACE,
      "--selector=flowai.runtime=k8s",
      "--all-containers=true",
    ],
    [
      "logs",
      "-n",
      NAMESPACE,
      "deployment/executor-k8s-openhands",
      "--all-containers=true",
    ],
    [
      "logs",
      "-n",
      NAMESPACE,
      "deployment/flowai-local-path-provisioner",
      "--all-containers=true",
    ],
  ];
  const sections: string[] = [`k3d=${K3D_VERSION}`];
  for (const args of commands) {
    try {
      sections.push(
        `kubectl ${args.join(" ")}\n${await run("kubectl", args, { env: { KUBECONFIG: kubeconfig } })}`,
      );
    } catch (error) {
      sections.push(`kubectl ${args.join(" ")}\n${String(error)}`);
    }
  }
  await fs.writeFile(
    path.join(artifactsDir, "cluster.txt"),
    `${sections.join("\n\n")}\n`,
  );
}

export const test = base.extend<{}, { suite: K3dExecutorSuite }>({
  suite: [
    async ({}, use) => {
      const clusterName = freshClusterName();
      const artifactsDir = await fs.mkdtemp(
        path.join(os.tmpdir(), `${clusterName}-artifacts-`),
      );
      // Keep the default off /tmp because some developer machines mount it
      // as a small tmpfs. CI or constrained workstations may explicitly
      // select a larger filesystem without changing the fixture contract.
      const nodeDataRoot = process.env.FLOWAI_K3D_DATA_ROOT
        ? path.resolve(process.env.FLOWAI_K3D_DATA_ROOT)
        : path.resolve(__dirname, "..", "..", "..");
      const nodeDataDir = await fs.mkdtemp(
        path.join(nodeDataRoot, ".k3d-data-"),
      );
      let registry: RegistryWorker | undefined;
      let registryProxy: net.Server | undefined;
      let mockLLM: ChildProcess | undefined;
      const mockLLMOutput: Buffer[] = [];
      let kubeconfig = "";
      let created = false;

      try {
        registry = await startRegistryWorker();
        const proxy = await startRegistryProxy(registry.bindPort);
        registryProxy = proxy.server;
        const mockLLMPort = await reservePort();
        mockLLM = spawn("node", ["qa-e2e/agent-openhands-image/mock-llm.mjs"], {
          cwd: path.resolve(__dirname, "..", "..", ".."),
          env: {
            ...process.env,
            FLOWAI_MOCK_LLM_PORT: String(mockLLMPort),
          },
          stdio: ["ignore", "pipe", "pipe"],
        });
        mockLLM.stdout?.on("data", (chunk: Buffer) =>
          mockLLMOutput.push(chunk),
        );
        mockLLM.stderr?.on("data", (chunk: Buffer) =>
          mockLLMOutput.push(chunk),
        );
        const mockDeadline = Date.now() + 10_000;
        while (Date.now() < mockDeadline) {
          try {
            const response = await fetch(
              `http://127.0.0.1:${mockLLMPort}/health`,
            );
            if (response.status === 200) break;
          } catch {}
          await new Promise((resolve) => setTimeout(resolve, 100));
        }
        const teamA = await bootstrapTeam(
          registry.baseUrl,
          `${clusterName}-a`,
          "k8s-cluster-a",
        );
        const teamB = await bootstrapTeam(
          registry.baseUrl,
          `${clusterName}-b`,
          "k8s-cluster-a",
        );

        await run(
          "docker",
          [
            "build",
            "--tag",
            EXECUTOR_IMAGE,
            "--file",
            "executor/k8s-openhands/Dockerfile",
            ".",
          ],
          { timeoutMs: 600_000 },
        );
        const repoDigests = JSON.parse(
          await run("docker", [
            "image",
            "inspect",
            REAL_AGENT_IMAGE,
            "--format",
            "{{json .RepoDigests}}",
          ]),
        ) as string[];
        const exactRealImage = repoDigests.find((value) => value.includes("@"));
        if (!exactRealImage) {
          throw new Error(`${REAL_AGENT_IMAGE} has no immutable RepoDigest`);
        }
        const separator = exactRealImage.lastIndexOf("@");
        const realAgentImage = {
          repository: REAL_AGENT_IMAGE,
          digest: exactRealImage.slice(separator + 1),
        };
        await run(
          "docker",
          [
            "build",
            "--tag",
            AGENT_IMAGE,
            "--file",
            "qa-e2e/executor_k8s_openhands/mock-openhands/Dockerfile",
            ".",
          ],
          { timeoutMs: 600_000 },
        );

        await run(
          "k3d",
          [
            "cluster",
            "create",
            clusterName,
            "--wait",
            "--timeout",
            "180s",
            "--volume",
            `${nodeDataDir}:/var/lib/rancher/k3s@server:0`,
            "--k3s-arg",
            "--kubelet-arg=feature-gates=KubeletInUserNamespace=true@server:0",
            "--k3s-arg",
            "--kube-proxy-arg=conntrack-max-per-core=0@server:0",
          ],
          { timeoutMs: 300_000 },
        );
        created = true;
        kubeconfig = await run("k3d", ["kubeconfig", "write", clusterName]);
        const node = `k3d-${clusterName}-server-0`;
        const nodeHosts = await run("docker", [
          "exec",
          node,
          "cat",
          "/etc/hosts",
        ]);
        const hostGateway = nodeHosts
          .split("\n")
          .find((line) => line.includes("host.containers.internal"))
          ?.trim()
          .split(/\s+/)[0];
        if (!hostGateway) {
          throw new Error("k3d node does not expose host.containers.internal");
        }

        await importImage(clusterName, EXECUTOR_IMAGE);
        await importImage(clusterName, AGENT_IMAGE);
        await importImage(clusterName, REAL_AGENT_IMAGE);
        await importImage(clusterName, STORAGE_HELPER_IMAGE);

        const registryURL = `http://${hostGateway}:${proxy.port}`;
        await run(
          "helmfile",
          [
            "--file",
            "executor/k8s-openhands/deploy/k8s/helmfile.yaml",
            "apply",
            "--set",
            `stateRegistryURL=${registryURL}`,
            "--set",
            `executor.teamID=${teamA.admin.team_id}`,
            "--set",
            "executor.finishedCleanupDelay=5s",
            "--set",
            `openhands.llmBaseURL=http://${hostGateway}:${mockLLMPort}/v1`,
            "--set",
            "openhands.llmModel=openai/flowai-mock",
          ],
          { env: { KUBECONFIG: kubeconfig }, timeoutMs: 300_000 },
        );

        const kubectl = async (...args: string[]): Promise<string> =>
          await run("kubectl", args, { env: { KUBECONFIG: kubeconfig } });
        await kubectl(
          "rollout",
          "status",
          "-n",
          NAMESPACE,
          "deployment/flowai-local-path-provisioner",
          "--timeout=180s",
        );
        await kubectl(
          "rollout",
          "status",
          "-n",
          NAMESPACE,
          "deployment/executor-k8s-openhands",
          "--timeout=180s",
        );
        await kubectl(
          "wait",
          "-n",
          NAMESPACE,
          "--for=jsonpath={.status.phase}=Bound",
          "pvc/executor-k8s-openhands-cache",
          "--timeout=120s",
        );
        const ready = JSON.parse(
          await kubectl(
            "get",
            "--raw",
            `/api/v1/namespaces/${NAMESPACE}/services/http:executor-k8s-openhands:8030/proxy/readyz`,
          ),
        ) as { executor_id?: string; state_registry_registered?: boolean };
        if (!ready.executor_id || ready.state_registry_registered !== true) {
          throw new Error(
            `executor readiness omitted registered identity: ${JSON.stringify(ready)}`,
          );
        }

        const registryFetch = async (
          pathname: string,
          init: RequestInit = {},
        ): Promise<Response> =>
          await fetch(`${registry!.baseUrl}${pathname}`, init);

        const ingestTask = async (
          team: BootstrapResult,
          payload: Record<string, unknown> = {},
          environmentID?: string,
          image?: { repository: string; digest: string },
        ): Promise<{ task_id: string }> => {
          const response = await registryFetch("/v1/tasks", {
            method: "POST",
            headers: {
              "Content-Type": "application/json",
              "X-FlowAI-Role": "listener",
              "X-FlowAI-Team-Id": team.admin.team_id,
              "X-FlowAI-Listener-Identity": team.listenerIdentity,
              "X-FlowAI-Source-System-Id": team.sourceSystem.source_system_id,
              "X-FlowAI-Request-Id": `ingest-${crypto.randomUUID()}`,
            },
            body: JSON.stringify({
              team_id: team.admin.team_id,
              source_system_id: team.sourceSystem.source_system_id,
              source_id: crypto.randomUUID(),
              task_type_id: team.taskType.task_type_id,
              payload,
              ...(environmentID ? { environment_id: environmentID } : {}),
              ...(image ? { image } : {}),
            }),
          });
          if (response.status !== 201) {
            throw new Error(
              `task ingest returned ${response.status}: ${await response.text()}`,
            );
          }
          return (await response.json()) as { task_id: string };
        };
        const gatewayFetch = async (
          team: BootstrapResult,
          pathname: string,
          init: RequestInit = {},
        ): Promise<Response> =>
          await registryFetch(pathname, {
            ...init,
            headers: {
              "X-FlowAI-Role": "gateway",
              "X-FlowAI-Team-Id": team.admin.team_id,
              "X-FlowAI-Operator-Id": `op-${team.admin.team_id}`,
              "X-FlowAI-Request-Id": `gateway-${crypto.randomUUID()}`,
              ...(init.headers ?? {}),
            },
          });
        const installSystemExecutor = async (): Promise<{
          executorID: string;
          namespace: string;
        }> => {
          const systemNamespace = "flowai-executor-k8s-system";
          await kubectl(
            "scale",
            "deployment/executor-k8s-openhands",
            "-n",
            NAMESPACE,
            "--replicas=0",
          );
          await run(
            "helm",
            [
              "upgrade",
              "--install",
              "executor-k8s-openhands-system",
              "executor/k8s-openhands/deploy/k8s/chart",
              "--namespace",
              systemNamespace,
              "--create-namespace",
              "--values",
              "executor/k8s-openhands/deploy/k8s/values-k3d.yaml",
              "--set",
              `stateRegistryURL=${registryURL}`,
              "--set",
              "storageClass.create=false",
              "--set",
              "executor.scope=system",
              "--set",
              "executor.teamID=",
              "--set",
              "executor.maxPods=2",
            ],
            { env: { KUBECONFIG: kubeconfig }, timeoutMs: 300_000 },
          );
          await kubectl(
            "rollout",
            "status",
            "deployment/executor-k8s-openhands",
            "-n",
            systemNamespace,
            "--timeout=180s",
          );
          const ready = JSON.parse(
            await kubectl(
              "get",
              "--raw",
              `/api/v1/namespaces/${systemNamespace}/services/http:executor-k8s-openhands:8030/proxy/readyz`,
            ),
          ) as { executor_id?: string };
          if (!ready.executor_id)
            throw new Error("system Executor readiness omitted executor_id");
          return { executorID: ready.executor_id, namespace: systemNamespace };
        };

        await use({
          clusterName,
          kubeconfig,
          artifactsDir,
          registry,
          registryBaseURL: registry.baseUrl,
          executorID: ready.executor_id,
          realAgentImage,
          teamA,
          teamB,
          kubectl,
          registryFetch,
          ingestTask,
          gatewayFetch,
          installSystemExecutor,
        });
      } finally {
        await fs
          .writeFile(
            path.join(artifactsDir, "mock-llm.log"),
            Buffer.concat(mockLLMOutput),
          )
          .catch(() => undefined);
        if (created && kubeconfig !== "") {
          await collectDiagnostics(kubeconfig, artifactsDir).catch(
            () => undefined,
          );
        }
        if (created) {
          await run("k3d", ["cluster", "delete", clusterName]).catch(
            () => undefined,
          );
        }
        await registry?.teardown().catch(() => undefined);
        mockLLM?.kill("SIGTERM");
        await new Promise<void>((resolve) => {
          if (!registryProxy) {
            resolve();
            return;
          }
          registryProxy.close(() => resolve());
        });
        await removeNodeDataDir(nodeDataDir);
      }
    },
    { scope: "worker", timeout: 1_200_000 },
  ],
});

export { expect };
