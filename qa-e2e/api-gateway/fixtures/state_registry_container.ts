import { execFile } from "node:child_process";
import { createServer } from "node:net";
import type { AddressInfo } from "node:net";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

const exec = promisify(execFile);
const runtime = process.env.FLOWAI_CONTAINER_RUNTIME ?? "docker";
const repoRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../../..",
);
const image = "flowai-state-registry-v0007-e2e:local";
let build: Promise<string> | undefined;

export async function startStateRegistryContainer(
  postgresURL: string,
  admin: {
    issuer: string;
    audience: string;
    jwksURL: string;
    jwksTimeout?: string;
    tokenMaxAge?: string;
  },
  databaseURLs?: { runtime: string; migration: string },
) {
  const imageID = await buildImage();
  const port = await availablePort();
  let controlPort = await availablePort();
  while (controlPort === port) controlPort = await availablePort();
  const controlToken = "flowai-v0007-test-control";
  const environment: Record<string, string> = {
    STATE_REGISTRY_BIND_HOST: "127.0.0.1",
    STATE_REGISTRY_BIND_PORT: String(port),
    STATE_REGISTRY_TEST_MODE: "true",
    STATE_REGISTRY_POSTGRES_URL: databaseURLs?.runtime ?? postgresURL,
    STATE_REGISTRY_MIGRATION_POSTGRES_URL:
      databaseURLs?.migration ?? postgresURL,
    STATE_REGISTRY_AES_KEY_HEX: "7a".repeat(32),
    STATE_REGISTRY_SCOPE_TOKEN_KEY_ID: "scope-v0007",
    STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX: "6b".repeat(32),
    STATE_REGISTRY_CURSOR_KEY_ID: "cursor-v0007",
    STATE_REGISTRY_CURSOR_KEY_HEX: "5c".repeat(32),
    STATE_REGISTRY_ADMIN_ISSUER: admin.issuer,
    STATE_REGISTRY_ADMIN_AUDIENCE: admin.audience,
    STATE_REGISTRY_ADMIN_JWKS_URL: admin.jwksURL,
    STATE_REGISTRY_ADMIN_ROLE_CLAIM_POINTER: "/realm_access/roles",
    STATE_REGISTRY_ADMIN_ALGORITHMS: "RS256",
    STATE_REGISTRY_ADMIN_JWKS_TIMEOUT: admin.jwksTimeout ?? "5s",
    STATE_REGISTRY_ADMIN_TOKEN_MAX_AGE: admin.tokenMaxAge ?? "5m",
    STATE_REGISTRY_TEST_CONTROL_ENABLED: "true",
    STATE_REGISTRY_TEST_CONTROL_BIND_ADDRESS: `127.0.0.1:${controlPort}`,
    STATE_REGISTRY_TEST_CONTROL_TOKEN: controlToken,
    STATE_REGISTRY_TEST_CONTROL_BARRIER_TIMEOUT_MS: "10000",
  };
  const args = [
    "run",
    "--detach",
    "--network",
    "host",
    "--label",
    "flowai.e2e.service=state-registry-v0007",
  ];
  for (const [name, value] of Object.entries(environment))
    args.push("--env", `${name}=${value}`);
  args.push(image);
  const started = await exec(runtime, args, { cwd: repoRoot });
  const containerID = started.stdout.trim();
  if (!containerID) throw new Error("no State Registry container ID");
  const baseURL = `http://127.0.0.1:${port}`;
  try {
    let last: unknown;
    for (let attempt = 0; attempt < 300; attempt += 1) {
      try {
        const response = await fetch(`${baseURL}/v1/livez`);
        if (response.ok)
          return {
            baseURL,
            testControlURL: `http://127.0.0.1:${controlPort}`,
            testControlToken: controlToken,
            imageID,
            close: async () => {
              await exec(runtime, ["rm", "--force", containerID]).catch(
                () => undefined,
              );
            },
          };
        last = response.status;
      } catch (error) {
        last = error;
      }
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    throw new Error(`State Registry readiness failed: ${String(last)}`);
  } catch (error) {
    const logs = await exec(runtime, ["logs", containerID], {
      maxBuffer: 10 * 1024 * 1024,
    }).catch(() => ({ stdout: "" }));
    await exec(runtime, ["rm", "--force", containerID]).catch(() => undefined);
    throw new Error(`${String(error)}\n${logs.stdout}`);
  }
}

export async function runStateRegistryExpectStartupFailure(
  environment: Record<string, string>,
): Promise<string> {
  await buildImage();
  const args = ["run", "--rm"];
  for (const [name, value] of Object.entries(environment))
    args.push("--env", `${name}=${value}`);
  args.push(image);
  try {
    await exec(runtime, args, { cwd: repoRoot, timeout: 15_000 });
  } catch (error) {
    const value = error as { stdout?: string; stderr?: string };
    return `${value.stdout ?? ""}${value.stderr ?? ""}`;
  }
  throw new Error(
    "State Registry unexpectedly started with invalid configuration",
  );
}

function buildImage(): Promise<string> {
  build ??= (async () => {
    await exec(
      runtime,
      [
        "build",
        "--build-arg",
        "FLOWAI_GO_TAGS=state_registry_test_harness",
        "--file",
        "svc/state-registry/Containerfile",
        "--tag",
        image,
        ".",
      ],
      { cwd: repoRoot, maxBuffer: 20 * 1024 * 1024 },
    );
    const result = await exec(
      runtime,
      ["image", "inspect", image, "--format", "{{.Id}}"],
      { cwd: repoRoot },
    );
    return result.stdout.trim();
  })();
  return build;
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
