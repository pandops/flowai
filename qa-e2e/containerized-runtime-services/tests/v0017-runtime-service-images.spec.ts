import { expect, test } from "@playwright/test";
import { lstat, readFile, readdir } from "node:fs/promises";
import http from "node:http";
import path from "node:path";
import { buildImagesOnce, type ImageManifest } from "../fixtures/images";
import { preflightSocket } from "../fixtures/socket-preflight";
import { run } from "../fixtures/runtime";
import { startRegistryWorker } from "../../state-registry/fixtures/registry_worker";
import { startDockerService } from "../fixtures/docker-service";
import { startK3dPod } from "../fixtures/k3d-pod";

let manifest: ImageManifest;

test.beforeAll(async () => {
  manifest = await buildImagesOnce();
});

test("v0017.1 / state-registry uses a fresh owned-image container", async () => {
  expect(
    manifest.errors.stateRegistry,
    "State Registry production image must build from svc/state-registry/Containerfile",
  ).toBeUndefined();
  expect(manifest.images.stateRegistry?.id).toMatch(/^sha256:/);
  const first = await startRegistryWorker({
    serviceImage: manifest.images.stateRegistry!.id,
    productionMode: true,
  });
  try {
    expect((await fetch(`${first.baseUrl}/v1/livez`)).status).toBe(200);
    const actual = await run("docker", [
      "inspect",
      (first.postgres.handle as { id: string }).id,
      "--format",
      "{{.Name}}",
    ]);
    expect(actual).toContain("state-registry-pg-");
  } finally {
    await first.teardown();
  }
  const second = await startRegistryWorker({
    serviceImage: manifest.images.stateRegistry!.id,
    productionMode: true,
  });
  try {
    expect(second.workerExe).toBe(manifest.images.stateRegistry!.id);
  } finally {
    await second.teardown();
  }
});

test("v0017.1 / docker executor uses a fresh owned-image container", async () => {
  expect(
    manifest.errors.dockerExecutor,
    "Docker Executor production image must build from executor/docker_openhands/Containerfile",
  ).toBeUndefined();
  const host = process.env.DOCKER_HOST ?? "";
  expect(
    host.startsWith("unix://"),
    `configured Docker endpoint must be an explicit Unix socket: ${host}`,
  ).toBeTruthy();
  const socket = await preflightSocket(host.slice("unix://".length));
  const info = await lstat(socket.hostPath);
  expect(info.gid).toBe(socket.gid);
  expect(info.mode & 0o777).not.toBe(0o666);
  const fake = http.createServer((req, res) => {
    if (req.method === "POST" && req.url === "/v1/executors") {
      res.writeHead(201, { "content-type": "application/json" });
      res.end(
        JSON.stringify({
          executor_id: "v0017-executor",
          scope: "system",
          team_id: null,
          executor_type: "executor_docker_openhands",
          authorized_tag: "openhands",
          max_capacity: 1,
          running_count: 0,
          runtime_metadata: {},
        }),
      );
      return;
    }
    if (req.url?.includes("/tasks")) {
      res.writeHead(204);
      res.end();
      return;
    }
    res.writeHead(202, { "content-type": "application/json" });
    res.end("{}");
  });
  await new Promise<void>((resolve) => fake.listen(0, "0.0.0.0", resolve));
  const address = fake.address();
  if (!address || typeof address === "string")
    throw new Error("fake registry did not bind");
  const executor = await startDockerService({
    imageId: manifest.images.dockerExecutor!.id,
    containerPort: 8020,
    mounts: [`type=bind,source=${socket.hostPath},target=${socket.hostPath}`],
    groupAdd: [socket.gid],
    env: {
      EXECUTOR_API_BIND: "0.0.0.0:8020",
      EXECUTOR_STATE_REGISTRY_URL: `http://host.containers.internal:${address.port}`,
      EXECUTOR_SCOPE: "system",
      EXECUTOR_AUTHORIZED_TAG: "openhands",
      EXECUTOR_MAX_CONTAINERS: "1",
      EXECUTOR_CACHE_DIR: "/tmp/flowai-cache",
      DOCKER_SOCKET_PATH: socket.hostPath,
      OPENHANDS_IMAGE: "ghcr.io/openhands/agent-server:latest-python",
      OPENHANDS_AGENT_PROFILE_ID: "flowai-default",
    },
  });
  try {
    const deadline = Date.now() + 15_000;
    let status = 0;
    while (Date.now() < deadline) {
      try {
        status = (await fetch(`${executor.baseUrl}/v1/livez`)).status;
        if (status === 200) break;
      } catch {}
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    expect(status).toBe(200);
    const inspection = JSON.parse(
      await run("docker", ["inspect", executor.id]),
    )[0];
    expect(
      inspection.Image.startsWith(
        manifest.images.dockerExecutor!.id.replace("sha256:", ""),
      ) || `sha256:${inspection.Image}` === manifest.images.dockerExecutor!.id,
    ).toBeTruthy();
    expect(inspection.HostConfig.Privileged).toBe(false);
    expect(inspection.Config.User).not.toMatch(/^(0|root)(:|$)/);
    expect(inspection.HostConfig.GroupAdd).toEqual([String(socket.gid)]);
    expect(inspection.HostConfig.Binds ?? []).toHaveLength(1);
    expect(inspection.Mounts).toHaveLength(1);
    expect(inspection.Mounts[0].Source).toBe(socket.hostPath);
  } finally {
    await executor.teardown();
    await new Promise<void>((resolve) => fake.close(() => resolve()));
  }
});

test("v0017.1 / k8s executor uses a fresh owned-image pod", async () => {
  test.setTimeout(360_000);
  expect(
    manifest.errors.k8sExecutor,
    "K8s Executor production image must build from executor/k8s-openhands/Containerfile (Dockerfile naming is forbidden)",
  ).toBeUndefined();
  expect(manifest.images.k8sExecutor?.id).toMatch(/^sha256:/);
  const sharedCluster = process.env.FLOWAI_E2E_K3D_CLUSTER;
  const cluster =
    sharedCluster ?? `v0017-${process.pid}-${Date.now().toString(36)}`;
  const fake = http.createServer((req, res) => {
    if (req.method === "POST" && req.url === "/v1/executors") {
      res.writeHead(201, { "content-type": "application/json" });
      res.end(
        JSON.stringify({
          executor_id: "v0017-k8s",
          scope: "system",
          team_id: null,
          executor_type: "executor_k8s_openhands",
          authorized_tag: "openhands",
          max_capacity: 1,
          running_count: 0,
          runtime_metadata: {},
        }),
      );
      return;
    }
    if (req.url?.includes("/tasks")) {
      res.writeHead(204);
      res.end();
      return;
    }
    res.writeHead(202, { "content-type": "application/json" });
    res.end("{}");
  });
  await new Promise<void>((resolve) => fake.listen(0, "0.0.0.0", resolve));
  const address = fake.address();
  if (!address || typeof address === "string")
    throw new Error("fake registry did not bind");
  try {
    if (!sharedCluster) {
      await run(
        "k3d",
        [
          "cluster",
          "create",
          cluster,
          "--wait",
          "--timeout",
          "180s",
          "--k3s-arg",
          "--kubelet-arg=feature-gates=KubeletInUserNamespace=true@server:0",
          "--k3s-arg",
          "--kubelet-arg=eviction-hard=nodefs.available<100Mi,imagefs.available<100Mi,nodefs.inodesFree<1%,imagefs.inodesFree<1%@server:0",
          "--k3s-arg",
          "--kube-proxy-arg=conntrack-max-per-core=0@server:0",
        ],
        240_000,
      );
    }
    const hosts = await run("docker", [
      "exec",
      `k3d-${cluster}-server-0`,
      "cat",
      "/etc/hosts",
    ]);
    const hostGateway = hosts
      .split("\n")
      .find((line) => line.includes("host.containers.internal"))
      ?.trim()
      .split(/\s+/)[0];
    if (!hostGateway)
      throw new Error("k3d node lacks host.containers.internal gateway");
    const pod = await startK3dPod({
      cluster,
      immutableImageId: manifest.images.k8sExecutor!.id,
      registryUrl: `http://${hostGateway}:${address.port}`,
    });
    try {
      expect((await fetch(`${pod.baseUrl}/livez`)).status).toBe(200);
      expect(
        pod.imageId.replace("docker-pullable://", "").replace("docker://", ""),
      ).toContain(manifest.images.k8sExecutor!.id.replace("sha256:", ""));
    } finally {
      await pod.teardown();
    }
  } finally {
    if (!sharedCluster)
      await run("k3d", ["cluster", "delete", cluster], 180_000).catch(
        () => undefined,
      );
    await new Promise<void>((resolve) => fake.close(() => resolve()));
  }
});

test("v0017.1 / qa-e2e has no real-service host launch path", async () => {
  const root = path.resolve(__dirname, "../..");
  const violations: string[] = [];
  async function scan(dir: string): Promise<void> {
    for (const entry of await readdir(dir, { withFileTypes: true })) {
      if (
        entry.name === "node_modules" ||
        entry.name === "reports" ||
        entry.name === "test-cases" ||
        entry.name === "containerized-runtime-services"
      )
        continue;
      const file = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        await scan(file);
        continue;
      }
      if (!/\.(ts|js)$/.test(entry.name)) continue;
      const source = await readFile(file, "utf8");
      const startsRegistryHost =
        /STATE_REGISTRY_BINARY|["']go["']\s*,\s*\[[^\]]*["']run["']|cmd\/state-registry\/main\.go/.test(
          source,
        );
      const startsExecutorHost =
        /startExecutorBinary|EXECUTOR_DOCKER_OPEHANDS_BIN|cmd\/executor_docker_openhands\/main\.go/.test(
          source,
        );
      if (startsRegistryHost || startsExecutorHost)
        violations.push(path.relative(root, file));
    }
  }
  await scan(root);
  expect(
    violations,
    `real FlowAI services must not launch as host binaries:\n${violations.join("\n")}`,
  ).toEqual([]);
});
