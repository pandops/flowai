// Local helper to spawn the real executor_docker_openhands concrete
// Executor binary as a subprocess for the v0002 state-registry contract
// tests. The cross-service qa-e2e/executor_docker_openhands/tests/helpers.ts
// owns the canonical helper that drives the v0001 wire protocol; the
// state-registry qa-e2e does NOT import that package directly because
// AGENTS.md mandates per-service isolation. Instead, this fixture
// duplicates the spawn shape and adds v0002-future-facing startup
// options the v0002 Executor implementation task must consume, then
// observes the real binary's wire behaviour through Registry reads and
// Docker container metadata when the Markdown requires it.
//
// V0002 startup options surfaced to the spawned process (each maps to a
// stable environment variable; v0001 ignores them and v0002 reads them
// during its startup register + claim loop):
//
//   EXECUTOR_STATE_REGISTRY_URL          -> the v0002 State Registry base URL the
//                                          binary must talk to (host + port only;
//                                          the v0002 binary appends its own path
//                                          prefixes per the OpenAPI).
//   EXECUTOR_SCOPE                       -> "team" or "system".
//   EXECUTOR_TEAM_ID                     -> the immutable team_id when scope=team
//                                          (empty string -> null when scope=system).
//   EXECUTOR_AUTHORIZED_TAG              -> the one and only execution_tag the
//                                          binary will register / discover.
//   EXECUTOR_LOCAL_IMAGE                 -> the locally configured OpenHands image
//                                          that the v0002 binary MUST NEVER
//                                          substitute for resolved_image.
//   EXECUTOR_MAX_CONTAINERS              -> local capacity ceiling (also funnelled
//                                          to Docker supervision so the binary's
//                                          "no third claim while locally full"
//                                          predicate can be observed).
//   EXECUTOR_DOCKER_SOCKET_PATH          -> Docker daemon socket the binary should
//                                          supervise (defaults to what the YAML
//                                          already provides).
//   EXECUTOR_OPENHANDS_IMAGE             -> mirroring of the v0001 env var; the
//                                          v0002 binary should treat this as its
//                                          speculative local image IF a registry
//                                          claim ever needs it, but never use it
//                                          in place of resolved_image.
//   EXECUTOR_POLL_INTERVAL               -> explicit dormancy window (must remain
//                                          conservative to avoid masking a real
//                                          v0002 binary that polls continuously).
//
// The current v0001 binary ingores every EXECUTOR_STATE_REGISTRY_* /
// EXECUTOR_SCOPE / EXECUTOR_TEAM_ID / EXECUTOR_AUTHORIZED_TAG /
// EXECUTOR_LOCAL_IMAGE variable. v0001 still attempts its v0001 PUT
// /v1/executors/{id} against the localhost mocked task server URL it
// spawns itself; pointing the binary at the v0002 State Registry URL via
// existing v0001 env vars does not retarget that internal mocked server,
// so v0001 cannot actually register against v0002. That structural gap
// is what turns every test in this file into a precise behaviour-specific
// RED: the canonical Registry state contains no row whose executor_id
// equals the binary's auto-generated id, so the binary's effects on
// tasks/events/containers/audit are equally absent, and the test can
// attribute each absence to the precise v0002 contract element that
// the (still v0001) binary cannot yet satisfy. When the v0002 client
// implementation lands, every RED flips to green without any harness
// rewrite because the assertions read canonical Registry state that the
// v0002 binary populates through its normal startup and runtime flow.
import { randomBytes } from "node:crypto";
import { spawn, type ChildProcess } from "node:child_process";
import { existsSync, mkdtempSync, rmSync } from "node:fs";
import { resolve as pathResolve } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { request, type APIRequestContext } from "@playwright/test";
import { sanitize } from "./redact";

export interface ExecutorStartOptions {
  /** Bind host for the Executor platform health API (default 127.0.0.1). */
  bindHost?: string;
  /** Bind port for the Executor platform health API (0 = OS-assigned). */
  bindPort?: number;
  /**
   * v0002 State Registry URL the binary must talk to. The harness
   * passes this through the EXECUTOR_STATE_REGISTRY_URL future-facing
   * variable; v0001 ingores it entirely.
   */
  registryUrl?: string;
  /**
   * Legacy backend HTTP TLS/mTLS env vars the binary may accept
   * for staged configuration cleanup. v0009: the binary never
   * reads them; the deployment network policy owns the
   * caller boundary.
   */
  legacyTLSServerCert?: string;
  legacyTLSServerKey?: string;
  legacyTLSClientCA?: string;
  legacyTLSRequireClientCert?: string;
  /**
   * Ownership scope. Future-facing: surface as EXECUTOR_SCOPE.
   * v0001 ingores it.
   */
  scope?: "team" | "system";
  /**
   * Immutable team_id binding for scope=team. Future-facing:
   * EXECUTOR_TEAM_ID. v0001 ingores it.
   */
  teamId?: string;
  /**
   * The one execution tag the binary registers and discovers.
   * Future-facing: EXECUTOR_AUTHORIZED_TAG. v0001 ingores it.
   */
  authorizedTag?: string;
  /**
   * Local OpenHands image the v0002 binary must NEVER substitute for
   * resolved_image. Future-facing: EXECUTOR_LOCAL_IMAGE. v0001 ingores
   * it (the v0001 binary does not implement image precedence).
   */
  localImage?: string;
  /** Local Docker socket override (also forwarded to v0001 as DOCKER_SOCKET_PATH). */
  dockerSocket?: string;
  /** Polling interval in ms the v0002 binary should use. */
  pollIntervalMs?: number;
  /** Max containers the Executor will supervise locally. */
  maxContainers?: number;
  /** Mirror of the v0001 OPENHANDS_IMAGE env var; documented for symmetry. */
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
  process: ChildProcess;
  bindHost: string;
  bindPort: number;
  baseUrl: string;
  probe: APIRequestContext;
  /** The real binary's auto-generated executor_id (from /v1/livez). */
  executorId: string;
  /** The future-facing env vars we passed to the binary (for diagnostics). */
  v0002Options: Readonly<Record<string, string>>;
  /** Redacted captured stdout+stderr (bounded). Empty until the binary writes. */
  redactedLogs(): string;
  teardown(): Promise<void>;
}

const RepoRoot = pathResolve(__dirname, "..", "..", "..");
const ExecutorBinary = (() => {
  const envOverride = process.env["EXECUTOR_DOCKER_OPEHANDS_BIN"];
  if (envOverride && envOverride.length > 0) {
    return envOverride;
  }
  return pathResolve(
    RepoRoot,
    "executor",
    "docker_openhands",
    "cmd",
    "executor_docker_openhands",
    "main.go",
  );
})();

const BindPattern = /(?:"bind":"|bind=")127\.0\.0\.1:(\d+)/;
const DefaultStartupTimeoutMs = 10_000;
const DefaultExecutorIdTimeoutMs = 5_000;
const CaptureMaxBytes = 64 * 1024;

function pushBounded(
  store: { chunks: string[]; bytes: number; cap: number },
  chunk: string,
): void {
  store.chunks.push(chunk);
  store.bytes += chunk.length;
  while (store.bytes > store.cap && store.chunks.length > 1) {
    const dropped = store.chunks.shift();
    if (dropped !== undefined) {
      store.bytes -= dropped.length;
    }
  }
}

async function discoverBindPort(
  proc: ChildProcess,
  capture: { chunks: string[]; bytes: number; cap: number },
  deadlineMs: number,
): Promise<number> {
  return await new Promise<number>((resolve, reject) => {
    let settled = false;
    const timer = setTimeout(() => {
      if (settled) return;
      settled = true;
      cleanup();
      reject(
        new Error(
          `executor binary never logged bind port within ${deadlineMs}ms; captured=${capture.chunks.join("")}`,
        ),
      );
    }, deadlineMs);
    const cleanup = (): void => {
      proc.stdout?.off("data", onChunk);
      proc.stderr?.off("data", onChunk);
      proc.off("exit", onExit);
      proc.off("error", onError);
    };
    const onChunk = (): void => {
      const snapshot = capture.chunks.join("");
      const m = snapshot.match(BindPattern);
      if (m && m[1] && !settled) {
        settled = true;
        clearTimeout(timer);
        cleanup();
        resolve(Number(m[1]));
      }
    };
    const onExit = (
      code: number | null,
      signal: NodeJS.Signals | null,
    ): void => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      cleanup();
      reject(
        new Error(
          `executor binary exited before logging bind port code=${String(code)} signal=${String(signal)}`,
        ),
      );
    };
    const onError = (err: Error): void => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      cleanup();
      reject(err);
    };
    proc.stdout?.on("data", onChunk);
    proc.stderr?.on("data", onChunk);
    proc.once("exit", onExit);
    proc.once("error", onError);
  });
}

async function waitForLivez(url: string, timeoutMs: number): Promise<void> {
  const api = await request.newContext({ baseURL: url });
  const deadline = Date.now() + timeoutMs;
  let lastStatus: number | undefined;
  let lastErr: unknown;
  try {
    while (Date.now() < deadline) {
      try {
        const resp = await api.get("/v1/livez");
        lastStatus = resp.status();
        if (resp.ok()) {
          return;
        }
      } catch (err) {
        lastErr = err;
      }
      await delay(150);
    }
    throw new Error(
      `executor binary livez at ${url} never returned 2xx within ${timeoutMs}ms (last status=${String(lastStatus)}, last err=${String(lastErr)})`,
    );
  } finally {
    await api.dispose();
  }
}

async function discoverExecutorId(
  probe: APIRequestContext,
  timeoutMs: number,
): Promise<string> {
  const deadline = Date.now() + timeoutMs;
  let lastErr: unknown;
  while (Date.now() < deadline) {
    try {
      const livez = await probe.get("/v1/livez");
      if (livez.ok()) {
        const body = (await livez.json()) as { executor_id?: unknown };
        if (
          typeof body.executor_id === "string" &&
          body.executor_id.length > 0
        ) {
          return body.executor_id;
        }
      }
    } catch (err) {
      lastErr = err;
    }
    await delay(100);
  }
  throw new Error(
    `executor binary livez never exposed executor_id within ${timeoutMs}ms (last err=${String(lastErr)})`,
  );
}

async function terminateProcess(
  proc: ChildProcess,
  timeoutMs: number,
): Promise<void> {
  if (proc.exitCode !== null || proc.signalCode !== null) {
    return;
  }
  const pid = proc.pid;
  let sigtermErr: Error | undefined;
  try {
    if (typeof pid === "number") {
      process.kill(-pid, "SIGTERM");
    } else {
      proc.kill("SIGTERM");
    }
  } catch (err) {
    sigtermErr = err instanceof Error ? err : new Error(String(err));
  }
  const exited = await new Promise<boolean>((resolve) => {
    const timer = setTimeout(() => resolve(false), timeoutMs);
    proc.once("exit", () => {
      clearTimeout(timer);
      resolve(true);
    });
  });
  if (exited && !sigtermErr) {
    return;
  }
  let sigkillErr: Error | undefined;
  try {
    if (typeof pid === "number") {
      process.kill(-pid, "SIGKILL");
    } else {
      proc.kill("SIGKILL");
    }
  } catch (err) {
    sigkillErr = err instanceof Error ? err : new Error(String(err));
  }
  const exitedAfterKill = await new Promise<boolean>((resolve) => {
    const timer = setTimeout(() => resolve(false), 5_000);
    proc.once("exit", () => {
      clearTimeout(timer);
      resolve(true);
    });
  });
  if (!exitedAfterKill) {
    const errs: Error[] = [
      new Error(
        `executor binary refused to exit after SIGTERM (${timeoutMs}ms) and SIGKILL (5s)`,
      ),
    ];
    if (sigtermErr) errs.push(sigtermErr);
    if (sigkillErr) errs.push(sigkillErr);
    throw new AggregateError(
      errs,
      "terminateProcess: executor binary did not exit",
    );
  }
}

export async function startExecutorBinary(
  opts: ExecutorStartOptions = {},
): Promise<ExecutorHandles> {
  const bindHost = opts.bindHost ?? "127.0.0.1";
  const bindPort = opts.bindPort ?? 0;
  const isGoSource = ExecutorBinary.endsWith(".go");
  const cmd = isGoSource ? "go" : ExecutorBinary;
  const args = isGoSource ? ["run", ExecutorBinary] : [];
  const ownsCacheDir = opts.cacheDir === undefined;
  const cacheDir =
    opts.cacheDir ?? mkdtempSync("/tmp/flowai-executor-docker-cache-");
  if (!isGoSource && !existsSync(ExecutorBinary)) {
    throw new Error(`executor binary missing at ${ExecutorBinary}`);
  }

  // Future-facing startup options. v0001 ignores every entry that is
  // not part of the v0001 surface; v0002 will read them at startup.
  const v0002Options: Record<string, string> = {};
  if (opts.registryUrl)
    v0002Options["EXECUTOR_STATE_REGISTRY_URL"] = opts.registryUrl;
  // v0009: the legacy backend HTTP TLS/mTLS env vars are accepted
  // for staged configuration cleanup. The binary never reads
  // them; the deployment network policy owns the caller boundary.
  if (opts.legacyTLSServerCert) {
    v0002Options["EXECUTOR_STATE_REGISTRY_TLS_CLIENT_CERT"] =
      opts.legacyTLSServerCert;
  }
  if (opts.legacyTLSServerKey) {
    v0002Options["EXECUTOR_STATE_REGISTRY_TLS_CLIENT_KEY"] =
      opts.legacyTLSServerKey;
  }
  if (opts.legacyTLSClientCA) {
    v0002Options["EXECUTOR_STATE_REGISTRY_TLS_SERVER_CA"] =
      opts.legacyTLSClientCA;
  }
  if (opts.legacyTLSRequireClientCert) {
    v0002Options["EXECUTOR_STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT"] =
      opts.legacyTLSRequireClientCert;
  }
  if (opts.scope) v0002Options["EXECUTOR_SCOPE"] = opts.scope;
  if (opts.teamId) v0002Options["EXECUTOR_TEAM_ID"] = opts.teamId;
  if (opts.authorizedTag)
    v0002Options["EXECUTOR_AUTHORIZED_TAG"] = opts.authorizedTag;
  if (opts.localImage) v0002Options["EXECUTOR_LOCAL_IMAGE"] = opts.localImage;
  if (typeof opts.maxContainers === "number") {
    v0002Options["EXECUTOR_MAX_CONTAINERS"] = String(opts.maxContainers);
  }
  if (typeof opts.pollIntervalMs === "number") {
    v0002Options["EXECUTOR_POLL_INTERVAL"] = `${opts.pollIntervalMs}ms`;
  }

  const env: NodeJS.ProcessEnv = {
    ...process.env,
    EXECUTOR_BIND_HOST: bindHost,
    EXECUTOR_BIND_PORT: String(bindPort),
    EXECUTOR_API_BIND: `${bindHost}:${String(bindPort)}`,
    // v0001 surface that v0001 already honours; preserved so the
    // binary boots with predictable defaults while we wait for v0002.
    ROUTING_TARGET: opts.authorizedTag ?? "openhands",
    DOCKER_SOCKET_PATH:
      opts.dockerSocket ?? process.env["FLOWAI_DOCKER_SOCKET"] ?? "",
    OPENHANDS_IMAGE: opts.openHandsImage ?? opts.localImage ?? "",
    OPENHANDS_AGENT_PROFILE_ID:
      opts.openHandsAgentProfileId ??
      process.env["OPENHANDS_AGENT_PROFILE_ID"] ??
      "flowai-default",
    OPENHANDS_LLM_MODEL: opts.openHandsLLMModel ?? "",
    OPENHANDS_LLM_API_KEY: opts.openHandsLLMAPIKey ?? "",
    OPENHANDS_LLM_BASE_URL: opts.openHandsLLMBaseURL ?? "",
    OPENHANDS_LLM_USAGE_ID: opts.openHandsLLMUsageID ?? "",
    EXECUTOR_FINISHED_CLEANUP_DELAY: opts.finishedCleanupDelay ?? "0s",
    EXECUTOR_CACHE_DIR: cacheDir,
    EXECUTOR_POLL_INTERVAL: `${opts.pollIntervalMs ?? 1_000}ms`,
    // Surface all future-facing options verbatim so the binary can pick
    // them up once the v0002 client implementation lands.
    ...v0002Options,
  };

  const proc = spawn(cmd, args, {
    stdio: ["ignore", "pipe", "pipe"],
    env,
    detached: true,
  });
  const capture = { chunks: [] as string[], bytes: 0, cap: CaptureMaxBytes };
  const onChunk = (b: Buffer): void => {
    pushBounded(capture, sanitize(b.toString("utf-8")));
  };
  proc.stdout?.on("data", onChunk);
  proc.stderr?.on("data", onChunk);

  let resolvedBindPort: number;
  try {
    resolvedBindPort = await discoverBindPort(
      proc,
      capture,
      DefaultStartupTimeoutMs,
    );
  } catch (err) {
    await terminateProcess(proc, 1_000).catch(() => undefined);
    throw err;
  }
  const baseUrl = `http://${bindHost}:${resolvedBindPort}`;
  try {
    await waitForLivez(baseUrl, DefaultStartupTimeoutMs);
  } catch (err) {
    await terminateProcess(proc, 1_000).catch(() => undefined);
    throw err;
  }
  const probe = await request.newContext({ baseURL: baseUrl });
  let executorId: string;
  try {
    executorId = await discoverExecutorId(probe, DefaultExecutorIdTimeoutMs);
  } catch (err) {
    await probe.dispose().catch(() => undefined);
    await terminateProcess(proc, 1_000).catch(() => undefined);
    throw err;
  }

  let teardownStarted = false;
  const handles: ExecutorHandles = {
    process: proc,
    bindHost,
    bindPort: resolvedBindPort,
    baseUrl,
    probe,
    executorId,
    v0002Options: Object.freeze({ ...v0002Options }),
    redactedLogs(): string {
      return capture.chunks.join("");
    },
    async teardown(): Promise<void> {
      if (teardownStarted) return;
      teardownStarted = true;
      await probe.dispose().catch(() => undefined);
      try {
        await terminateProcess(proc, 3_000);
      } catch (err) {
        // Best-effort; reraise so the caller knows the process did
        // not exit cleanly but the harness still completes.
        throw err;
      } finally {
        if (ownsCacheDir) rmSync(cacheDir, { recursive: true, force: true });
      }
    },
  };
  return handles;
}

export function uniqueExecutorId(): string {
  return `exec-${Date.now().toString(36)}-${randomBytes(3).toString("hex")}`;
}
