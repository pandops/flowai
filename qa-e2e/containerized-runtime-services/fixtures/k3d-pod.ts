import { spawn, type ChildProcess } from "node:child_process";
import { run, uniqueName } from "./runtime";

async function importImage(cluster: string, image: string): Promise<void> {
  await new Promise<void>((resolve, reject) => {
    const save = spawn("docker", ["save", image], {
      stdio: ["ignore", "pipe", "pipe"],
    });
    const load = spawn(
      "docker",
      [
        "exec",
        "--interactive",
        `k3d-${cluster}-server-0`,
        "ctr",
        "--namespace",
        "k8s.io",
        "images",
        "import",
        "-",
      ],
      { stdio: ["pipe", "ignore", "pipe"] },
    );
    save.stdout!.pipe(load.stdin!);
    let errors = "";
    save.stderr?.on("data", (chunk) => {
      errors += String(chunk);
    });
    load.stderr?.on("data", (chunk) => {
      errors += String(chunk);
    });
    let saveCode: number | null = null;
    let loadCode: number | null = null;
    const done = (): void => {
      if (saveCode === null || loadCode === null) return;
      if (saveCode === 0 && loadCode === 0) resolve();
      else
        reject(
          new Error(
            `image import failed save=${saveCode} load=${loadCode}: ${errors}`,
          ),
        );
    };
    save.once("exit", (code) => {
      saveCode = code;
      done();
    });
    load.once("exit", (code) => {
      loadCode = code;
      done();
    });
    save.once("error", reject);
    load.once("error", reject);
  });
}

export interface K3dPod {
  namespace: string;
  pod: string;
  imageId: string;
  baseUrl: string;
  teardown(): Promise<void>;
}

export async function startK3dPod(options: {
  cluster: string;
  immutableImageId: string;
  registryUrl: string;
}): Promise<K3dPod> {
  const namespace = uniqueName("flowai-v0017");
  const pod = "executor";
  const localTag = `localhost/${namespace}:immutable`;
  let forward: ChildProcess | undefined;
  try {
    await run("docker", ["tag", options.immutableImageId, localTag]);
    await importImage(options.cluster, localTag);
    await run("kubectl", ["create", "namespace", namespace]);
    await run("kubectl", [
      "create",
      "role",
      "executor",
      "-n",
      namespace,
      "--verb=get,list,watch,create,delete,patch",
      "--resource=pods,pods/log,pods/status",
    ]);
    await run("kubectl", [
      "create",
      "rolebinding",
      "executor",
      "-n",
      namespace,
      "--role=executor",
      `--serviceaccount=${namespace}:default`,
    ]);
    await run("kubectl", [
      "run",
      pod,
      "-n",
      namespace,
      `--image=${localTag}`,
      "--image-pull-policy=Never",
      "--restart=Never",
      "--port=8020",
      "--env=EXECUTOR_API_BIND=0.0.0.0:8020",
      `--env=EXECUTOR_STATE_REGISTRY_URL=${options.registryUrl}`,
      "--env=EXECUTOR_SCOPE=system",
      "--env=EXECUTOR_AUTHORIZED_TAG=openhands",
      `--env=EXECUTOR_NAMESPACE=${namespace}`,
      "--env=EXECUTOR_SERVICE_ACCOUNT=default",
      "--env=OPENHANDS_AGENT_PROFILE_ID=flowai-default",
      "--env=EXECUTOR_CACHE_DIR=/tmp/flowai-cache",
    ]);
    try {
      await run("kubectl", [
        "wait",
        "-n",
        namespace,
        "--for=condition=Ready",
        `pod/${pod}`,
        "--timeout=60s",
      ]);
    } catch (error) {
      const details = await run("kubectl", [
        "describe",
        "pod",
        "-n",
        namespace,
        pod,
      ]).catch(() => "");
      const logs = await run("kubectl", ["logs", "-n", namespace, pod]).catch(
        () => "",
      );
      throw new Error(`${String(error)}\n${details}\n${logs}`);
    }
    const statusID = await run("kubectl", [
      "get",
      "pod",
      pod,
      "-n",
      namespace,
      "-o",
      "jsonpath={.status.containerStatuses[0].imageID}",
    ]);
    const localPort = 28000 + Math.floor(Math.random() * 10000);
    forward = spawn(
      "kubectl",
      ["port-forward", "-n", namespace, `pod/${pod}`, `${localPort}:8020`],
      { stdio: "ignore" },
    );
    const baseUrl = `http://127.0.0.1:${localPort}`;
    const deadline = Date.now() + 15_000;
    let ready = false;
    while (Date.now() < deadline) {
      try {
        if ((await fetch(`${baseUrl}/livez`)).ok) {
          ready = true;
          break;
        }
      } catch {}
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    if (!ready) {
      const logs = await run("kubectl", ["logs", "-n", namespace, pod]).catch(
        () => "",
      );
      throw new Error(`K8s Executor Pod did not expose its test port: ${logs}`);
    }
    return {
      namespace,
      pod,
      imageId: statusID,
      baseUrl,
      teardown: async () => {
        forward?.kill("SIGTERM");
        await run("kubectl", [
          "delete",
          "namespace",
          namespace,
          "--wait=true",
        ]).catch(() => undefined);
        await run("docker", ["image", "rm", localTag]).catch(() => undefined);
      },
    };
  } catch (error) {
    forward?.kill("SIGTERM");
    await run("kubectl", [
      "delete",
      "namespace",
      namespace,
      "--wait=true",
    ]).catch(() => undefined);
    await run("docker", ["image", "rm", localTag]).catch(() => undefined);
    throw error;
  }
}
