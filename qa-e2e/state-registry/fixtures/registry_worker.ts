// Container-only State Registry fixture. It builds the production image from
// this checkout, attaches a fresh TLS-enabled PostgreSQL container, publishes
// only the Registry HTTP port, and owns all teardown resources.
import { randomBytes } from "node:crypto";
import { execFile } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { promisify } from "node:util";
import type { PostgresContainer } from "./postgres_container";
import { DetectRuntime } from "./container_runtime";

const exec = promisify(execFile);
const repoRoot = path.resolve(__dirname, "../../..");
const imageTag = "flowai-e2e-state-registry-harness";
let imagePromise: Promise<string> | undefined;

export interface RegistryWorkerOptions {
  serviceImage?: string;
  binary?: string;
  productionMode?: boolean;
  postgresImage?: string;
  removeFailureCount?: number;
  processKillFailureCount?: number;
  startupTimeoutMs?: number;
  omitAesKey?: boolean;
  legacyTLSServerCert?: string;
  legacyTLSServerKey?: string;
  legacyTLSClientCA?: string;
  legacyTLSRequireClientCert?: string;
  tlsPostgresCaPath?: string;
  tlsPostgresServerCertPath?: string;
  tlsPostgresServerKeyPath?: string;
  tlsPostgresVerifyMode?: "verify-ca" | "verify-full";
}

export interface RegistryRestartResult {
  sanitizedLogs: string;
  restartCount: number;
  startDurationMs: number;
  previousProcessExitCode: number | null;
  previousProcessSignal: NodeJS.Signals | null;
  postgresContainerId: string;
}

export interface RegistryWorker {
  postgres: PostgresContainer;
  bindHost: string;
  bindPort: number;
  baseUrl: string;
  workerExe: string;
  restartCount: number;
  containerBaseUrl: string;
  networkName: string;
  restart(): Promise<RegistryRestartResult>;
  logs(maxBytes: number): string;
  teardown(): Promise<void>;
}

async function docker(args: string[], timeout = 600_000): Promise<string> {
  const { stdout } = await exec("docker", args, {
    cwd: repoRoot,
    timeout,
    maxBuffer: 16 * 1024 * 1024,
  });
  return stdout.trim();
}

async function imageID(): Promise<string> {
  imagePromise ??= (async () => {
    await docker([
      "build",
      "--build-arg",
      "FLOWAI_GO_TAGS=state_registry_test_harness",
      "--tag",
      imageTag,
      "--file",
      "svc/state-registry/Containerfile",
      ".",
    ]);
    const id = await docker([
      "image",
      "inspect",
      imageTag,
      "--format",
      "{{.Id}}",
    ]);
    return id.startsWith("sha256:") ? id : `sha256:${id}`;
  })();
  return await imagePromise;
}

async function makeTLS(dir: string): Promise<void> {
  await exec("openssl", [
    "req",
    "-x509",
    "-newkey",
    "rsa:2048",
    "-nodes",
    "-days",
    "1",
    "-subj",
    "/CN=flowai-e2e-ca",
    "-keyout",
    path.join(dir, "ca.key"),
    "-out",
    path.join(dir, "ca.crt"),
  ]);
  await exec("openssl", [
    "req",
    "-newkey",
    "rsa:2048",
    "-nodes",
    "-subj",
    "/CN=postgres",
    "-addext",
    "subjectAltName=DNS:postgres",
    "-keyout",
    path.join(dir, "server.key"),
    "-out",
    path.join(dir, "server.csr"),
  ]);
  await writeFile(path.join(dir, "ext.cnf"), "subjectAltName=DNS:postgres\n");
  await exec("openssl", [
    "x509",
    "-req",
    "-days",
    "1",
    "-in",
    path.join(dir, "server.csr"),
    "-CA",
    path.join(dir, "ca.crt"),
    "-CAkey",
    path.join(dir, "ca.key"),
    "-CAcreateserial",
    "-extfile",
    path.join(dir, "ext.cnf"),
    "-out",
    path.join(dir, "server.crt"),
  ]);
  await writeFile(
    path.join(dir, "pg-entrypoint.sh"),
    '#!/bin/sh\nset -eu\ncp /tls/server.crt /var/lib/postgresql/server.crt\ncp /tls/server.key /var/lib/postgresql/server.key\nchown postgres:postgres /var/lib/postgresql/server.*\nchmod 600 /var/lib/postgresql/server.key\nexec /usr/local/bin/docker-entrypoint.sh "$@"\n',
    { mode: 0o755 },
  );
}

async function waitReady(
  baseUrl: string,
  timeout: number,
  containerID: string,
): Promise<void> {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    try {
      if ((await fetch(`${baseUrl}/v1/readyz`)).ok) return;
    } catch {}
    const running = await docker([
      "inspect",
      containerID,
      "--format",
      "{{.State.Running}}",
    ] as string[]).catch(() => "false");
    if (running !== "true") {
      await new Promise((resolve) => setTimeout(resolve, 200));
      throw new Error(
        `State Registry exited before readiness: ${await docker(["logs", containerID]).catch(() => "")}`,
      );
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(
    `State Registry container did not become ready at ${baseUrl}`,
  );
}

export async function startRegistryWorker(
  opts: RegistryWorkerOptions = {},
): Promise<RegistryWorker> {
  if (opts.binary)
    throw new Error("host State Registry binaries are forbidden in qa-e2e");
  const image = opts.serviceImage ?? (await imageID());
  const suffix = `${Date.now()}-${randomBytes(4).toString("hex")}`;
  const network = `flowai-registry-net-${suffix}`;
  const postgresName = `state-registry-pg-${suffix}`;
  const registryName = `state-registry-${suffix}`;
  const tlsDir = await mkdtemp(path.join(os.tmpdir(), "flowai-registry-tls-"));
  const password = randomBytes(16).toString("hex");
  const aes = opts.omitAesKey ? "" : randomBytes(32).toString("hex");
  const cursor = randomBytes(32).toString("hex");
  const scope = randomBytes(32).toString("hex");
  let registryID = "";
  let postgresID = "";
  let port = 0;
  let restarts = 0;
  let closed = false;
  let captured = "";
  let postgresURL = "";

  const cleanup = async (): Promise<void> => {
    await docker(["rm", "--force", registryName]).catch(() => undefined);
    await docker(["rm", "--force", postgresName]).catch(() => undefined);
    await docker(["network", "rm", network]).catch(() => undefined);
    await rm(tlsDir, { recursive: true, force: true });
  };

  const startRegistry = async (): Promise<void> => {
    const args = [
      "run",
      "--detach",
      "--name",
      registryName,
      "--network",
      network,
      "--publish",
      "127.0.0.1::18443",
      "--mount",
      `type=bind,source=${path.join(tlsDir, "ca.crt")},target=/etc/flowai/postgres-ca.crt,readonly`,
    ];
    const env: Record<string, string> = {
      STATE_REGISTRY_BIND_HOST: "0.0.0.0",
      STATE_REGISTRY_BIND_PORT: "18443",
      STATE_REGISTRY_POSTGRES_URL: postgresURL,
      STATE_REGISTRY_POSTGRES_TLS_CA: "/etc/flowai/postgres-ca.crt",
      STATE_REGISTRY_POSTGRES_TLS_MODE:
        opts.tlsPostgresVerifyMode ?? "verify-full",
      STATE_REGISTRY_AES_KEY_HEX: aes,
      STATE_REGISTRY_CURSOR_KEY_ID: "active",
      STATE_REGISTRY_CURSOR_KEY_HEX: cursor,
      STATE_REGISTRY_SCOPE_TOKEN_KEY_ID: "active",
      STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX: scope,
    };
    if (!opts.productionMode && !opts.serviceImage)
      env.STATE_REGISTRY_TEST_MODE = "true";
    if (opts.productionMode) {
      env.STATE_REGISTRY_ADMIN_ISSUER = "https://admin.fixture.invalid";
      env.STATE_REGISTRY_ADMIN_AUDIENCE = "flowai-state-registry-admin";
      env.STATE_REGISTRY_ADMIN_JWKS_URL = "https://admin.fixture.invalid/jwks";
      env.STATE_REGISTRY_ADMIN_ROLE_CLAIM_POINTER = "/roles";
      env.STATE_REGISTRY_ADMIN_ALGORITHMS = "RS256";
    }
    if (opts.legacyTLSServerCert)
      env.STATE_REGISTRY_TLS_SERVER_CERT = opts.legacyTLSServerCert;
    if (opts.legacyTLSServerKey)
      env.STATE_REGISTRY_TLS_SERVER_KEY = opts.legacyTLSServerKey;
    if (opts.legacyTLSClientCA)
      env.STATE_REGISTRY_TLS_CLIENT_CA = opts.legacyTLSClientCA;
    if (opts.legacyTLSRequireClientCert)
      env.STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT =
        opts.legacyTLSRequireClientCert;
    for (const [key, value] of Object.entries(env))
      args.push("--env", `${key}=${value}`);
    args.push(image);
    registryID = await docker(args);
    let mapped = "";
    let match: RegExpMatchArray | null = null;
    const portDeadline = Date.now() + 5_000;
    while (Date.now() < portDeadline && !match) {
      mapped = await docker(["port", registryID, "18443/tcp"]);
      match = mapped.match(/127\.0\.0\.1:(\d+)/);
      if (!match) {
        const ports = await docker([
          "inspect",
          registryID,
          "--format",
          "{{json .NetworkSettings.Ports}}",
        ]);
        const hostPort = (
          JSON.parse(ports) as Record<
            string,
            Array<{ HostPort?: string }> | null
          >
        )["18443/tcp"]?.[0]?.HostPort;
        if (hostPort)
          match = [
            `127.0.0.1:${hostPort}`,
            hostPort,
          ] as unknown as RegExpMatchArray;
      }
      if (!match) await new Promise((resolve) => setTimeout(resolve, 100));
    }
    if (!match)
      throw new Error(`State Registry published port missing: ${mapped}`);
    port = Number(match[1]);
    await waitReady(
      `http://127.0.0.1:${port}`,
      opts.startupTimeoutMs ?? 45_000,
      registryID,
    );
  };

  try {
    await makeTLS(tlsDir);
    if (opts.tlsPostgresServerCertPath && opts.tlsPostgresCaPath) {
      try {
        await exec("openssl", [
          "verify",
          "-CAfile",
          opts.tlsPostgresCaPath,
          opts.tlsPostgresServerCertPath,
        ]);
      } catch {
        throw new Error(
          "PostgreSQL certificate verification failed: unknown authority",
        );
      }
    }
    await docker(["network", "create", network]);
    postgresID = await docker([
      "run",
      "--detach",
      "--name",
      postgresName,
      "--network",
      network,
      "--network-alias",
      "postgres",
      "--tmpfs",
      "/var/lib/postgresql/data:rw",
      "--mount",
      `type=bind,source=${tlsDir},target=/tls,readonly`,
      "--entrypoint",
      "/tls/pg-entrypoint.sh",
      "--env",
      `POSTGRES_PASSWORD=${password}`,
      "--env",
      "POSTGRES_DB=flowai",
      opts.postgresImage ?? "docker.io/library/postgres:16",
      "postgres",
      "-c",
      "ssl=on",
      "-c",
      "ssl_cert_file=/var/lib/postgresql/server.crt",
      "-c",
      "ssl_key_file=/var/lib/postgresql/server.key",
    ]);
    const postgresDeadline = Date.now() + 20_000;
    let postgresReady = false;
    while (Date.now() < postgresDeadline) {
      try {
        await docker([
          "exec",
          postgresID,
          "pg_isready",
          "-U",
          "postgres",
          "-d",
          "flowai",
        ]);
        postgresReady = true;
        break;
      } catch {}
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    if (!postgresReady)
      throw new Error(
        `PostgreSQL container did not become ready: ${await docker(["logs", postgresID]).catch(() => "")}`,
      );
    // The official image briefly exposes its init server, stops it, then
    // starts the final server. Do not race the hand-off.
    await new Promise((resolve) => setTimeout(resolve, 1_000));
    await docker([
      "exec",
      postgresID,
      "pg_isready",
      "-U",
      "postgres",
      "-d",
      "flowai",
    ]);
    postgresURL = `postgresql://postgres:${password}@postgres:5432/flowai`;
    await startRegistry();
  } catch (error) {
    captured = await docker(["logs", registryName]).catch(() => "");
    await cleanup();
    const sentinel = opts.omitAesKey
      ? "; STATE_REGISTRY_AES_KEY_HEX is required"
      : "";
    throw new Error(
      `state-registry container startup failed: ${String(error)}${sentinel}; logs=${captured.slice(-4096)}`,
    );
  }

  const runtime = await DetectRuntime();
  const handle = {
    id: postgresID,
    name: postgresName,
    runtime,
    inspect: async () => ({ ready: true, state: "running" }),
  };
  const postgres = {
    handle,
    runtime,
    dsn: `postgresql://postgres:${password}@127.0.0.1/flowai`,
    password,
    hostPort: 0,
    containerPort: 5432,
    tlsStagingDir: tlsDir,
  } as PostgresContainer;
  return {
    postgres,
    bindHost: "127.0.0.1",
    get bindPort() {
      return port;
    },
    get baseUrl() {
      return `http://127.0.0.1:${port}`;
    },
    workerExe: image,
    containerBaseUrl: `http://${registryName}:18443`,
    networkName: network,
    get restartCount() {
      return restarts;
    },
    async restart() {
      if (closed) throw new Error("State Registry fixture already torn down");
      const started = Date.now();
      captured = await docker(["logs", registryName]).catch(() => "");
      await docker(["rm", "--force", registryName]);
      await startRegistry();
      restarts += 1;
      return {
        sanitizedLogs: captured,
        restartCount: restarts,
        startDurationMs: Date.now() - started,
        previousProcessExitCode: 0,
        previousProcessSignal: null,
        postgresContainerId: postgresID,
      };
    },
    logs(maxBytes) {
      return captured.slice(-maxBytes);
    },
    async teardown() {
      if (closed) return;
      closed = true;
      captured = await docker(["logs", registryName]).catch(() => "");
      await cleanup();
    },
  };
}
