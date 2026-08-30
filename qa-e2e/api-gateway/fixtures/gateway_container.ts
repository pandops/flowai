import { execFile } from "node:child_process";
import path from "node:path";
import { createServer } from "node:net";
import type { AddressInfo } from "node:net";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

const exec = promisify(execFile);
const repoRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../../..",
);
const runtime = process.env.FLOWAI_CONTAINER_RUNTIME ?? "docker";
const imageTag =
  process.env.FLOWAI_API_GATEWAY_IMAGE ?? "flowai-api-gateway-e2e:local";

let imageBuild: Promise<string> | undefined;

export type GatewayContainer = Readonly<{
  baseUrl: string;
  imageID: string;
  close: () => Promise<void>;
}>;

export async function startGatewayContainer(
  environment: Record<string, string> = {},
  options: { hostNetwork?: boolean } = {},
): Promise<GatewayContainer> {
  let lastError: unknown;
  for (let attempt = 0; attempt < 3; attempt += 1) {
    try {
      return await startGatewayContainerOnce(environment, options);
    } catch (error) {
      lastError = error;
      if (!options.hostNetwork) break;
    }
  }
  throw lastError;
}

export async function runGatewayExpectStartupFailure(
  environment: Record<string, string>,
): Promise<string> {
  await buildImage();
  const args = ["run", "--rm"];
  for (const [name, value] of Object.entries(environment))
    args.push("--env", `${name}=${value}`);
  args.push(imageTag);
  try {
    await exec(runtime, args, { cwd: repoRoot, timeout: 15_000 });
  } catch (error) {
    const value = error as { stdout?: string; stderr?: string };
    return `${value.stdout ?? ""}${value.stderr ?? ""}`;
  }
  throw new Error(
    "API Gateway unexpectedly started with invalid configuration",
  );
}

async function startGatewayContainerOnce(
  environment: Record<string, string>,
  options: { hostNetwork?: boolean },
): Promise<GatewayContainer> {
  const imageID = await buildImage();
  const hostPort = options.hostNetwork ? await availablePort() : undefined;
  const gatewayBaseURL = hostPort ? `http://127.0.0.1:${hostPort}` : "";
  environment = Object.fromEntries(
    Object.entries(environment).map(([name, value]) => [
      name,
      value.replaceAll("${GATEWAY_BASE_URL}", gatewayBaseURL),
    ]),
  );
  if (hostPort)
    environment = {
      ...environment,
      API_GATEWAY_BIND_HOST: "127.0.0.1",
      API_GATEWAY_BIND_PORT: String(hostPort),
    };
  const args = ["run", "--detach"];
  if (options.hostNetwork) args.push("--network", "host");
  else args.push("--publish", "127.0.0.1::8080");
  args.push("--label", "flowai.e2e.service=api-gateway");
  for (const [name, value] of Object.entries(environment))
    args.push("--env", `${name}=${value}`);
  args.push(imageTag);

  const started = await exec(runtime, args, { cwd: repoRoot });
  const containerID = started.stdout.trim();
  if (!containerID)
    throw new Error("container runtime returned no API Gateway container id");

  try {
    let port = String(hostPort ?? "");
    if (!options.hostNetwork) {
      const portResult = await exec(
        runtime,
        ["port", containerID, "8080/tcp"],
        { cwd: repoRoot },
      );
      const published = portResult.stdout.trim().split("\n")[0];
      port = published.slice(published.lastIndexOf(":") + 1);
      if (!/^\d+$/.test(port))
        throw new Error(
          `cannot resolve API Gateway published port from ${published}`,
        );
    }
    const baseUrl = `http://127.0.0.1:${port}`;
    await waitUntilHealthy(baseUrl);
    return {
      baseUrl,
      imageID,
      close: async () => {
        await exec(runtime, ["stop", "--time", "2", containerID], {
          cwd: repoRoot,
        }).catch(() => undefined);
        await exec(runtime, ["rm", "--force", containerID], {
          cwd: repoRoot,
        }).catch(() => undefined);
      },
    };
  } catch (error) {
    const logs = await exec(runtime, ["logs", containerID], {
      cwd: repoRoot,
      maxBuffer: 10 * 1024 * 1024,
    }).catch(() => ({ stdout: "", stderr: "" }));
    await exec(runtime, ["stop", "--time", "2", containerID], {
      cwd: repoRoot,
    }).catch(() => undefined);
    await exec(runtime, ["rm", "--force", containerID], {
      cwd: repoRoot,
    }).catch(() => undefined);
    throw new Error(`${String(error)}\n${logs.stdout}${logs.stderr}`);
  }
}

async function availablePort(): Promise<number> {
  const server = createServer();
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const port = (server.address() as AddressInfo).port;
  await new Promise<void>((resolve, reject) =>
    server.close((error) => (error ? reject(error) : resolve())),
  );
  return port;
}

function buildImage(): Promise<string> {
  imageBuild ??= (async () => {
    await exec(
      runtime,
      [
        "build",
        "--file",
        "svc/api-gateway/Containerfile",
        "--tag",
        imageTag,
        ".",
      ],
      {
        cwd: repoRoot,
        maxBuffer: 20 * 1024 * 1024,
      },
    );
    const inspected = await exec(
      runtime,
      ["image", "inspect", imageTag, "--format", "{{.Id}}"],
      { cwd: repoRoot },
    );
    const imageID = inspected.stdout.trim();
    if (!imageID)
      throw new Error("container runtime returned no API Gateway image id");
    return imageID;
  })();
  return imageBuild;
}

async function waitUntilHealthy(baseUrl: string): Promise<void> {
  let lastError: unknown;
  for (let attempt = 0; attempt < 100; attempt += 1) {
    try {
      const response = await fetch(`${baseUrl}/healthz`);
      if (response.ok) return;
      lastError = new Error(`health status ${response.status}`);
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(
    `API Gateway container did not become healthy: ${String(lastError)}`,
  );
}
