import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";

const repoRoot = path.resolve(import.meta.dirname, "../..");
const cluster = `flowai-exec-k8s-s${process.pid}`;
const dataDir = await mkdtemp(
  path.join(
    process.env.FLOWAI_K3D_DATA_ROOT ?? os.tmpdir(),
    ".flowai-e2e-k3d-",
  ),
);

function run(command, args, env = process.env) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, {
      cwd: repoRoot,
      env,
      stdio: "inherit",
    });
    child.once("error", reject);
    child.once("exit", (code, signal) =>
      code === 0
        ? resolve()
        : reject(
            new Error(`${command} ${args.join(" ")} exited ${code ?? signal}`),
          ),
    );
  });
}

try {
  await run("k3d", [
    "cluster",
    "create",
    cluster,
    "--wait",
    "--timeout",
    "180s",
    "--volume",
    `${dataDir}:/var/lib/rancher/k3s@server:0`,
    "--k3s-arg",
    "--disable=traefik@server:0",
    "--k3s-arg",
    "--disable=metrics-server@server:0",
    "--k3s-arg",
    "--kubelet-arg=feature-gates=KubeletInUserNamespace=true@server:0",
    "--k3s-arg",
    "--kube-proxy-arg=conntrack-max-per-core=0@server:0",
  ]);
  const kubeconfig = await new Promise((resolve, reject) => {
    const chunks = [];
    const child = spawn("k3d", ["kubeconfig", "write", cluster], {
      cwd: repoRoot,
      stdio: ["ignore", "pipe", "inherit"],
    });
    child.stdout.on("data", (chunk) => chunks.push(chunk));
    child.once("error", reject);
    child.once("exit", (code) =>
      code === 0
        ? resolve(Buffer.concat(chunks).toString("utf8").trim())
        : reject(new Error(`k3d kubeconfig write exited ${code}`)),
    );
  });
  const env = {
    ...process.env,
    FLOWAI_E2E_K3D_CLUSTER: cluster,
    FLOWAI_E2E_KUBECONFIG: kubeconfig,
    KUBECONFIG: kubeconfig,
  };
  for (const suite of [
    "state-registry",
    "web-ui",
    "containerized-runtime-services",
    "executor_k8s_openhands",
  ]) {
    await run("npm", ["--prefix", `qa-e2e/${suite}`, "test"], env);
  }
} finally {
  await run("k3d", ["cluster", "delete", cluster]).catch(() => undefined);
  await run("podman", ["unshare", "find", dataDir, "-depth", "-delete"]).catch(
    async () => {
      await rm(dataDir, { recursive: true, force: true }).catch(
        () => undefined,
      );
    },
  );
}
