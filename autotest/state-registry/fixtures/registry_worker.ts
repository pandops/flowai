// Worker-scoped fixture that owns the restartable state-registry Go
// process and the underlying postgres:16 container. The fixture
// follows the official guidance:
//   - worker scope so the shared process state survives across tests
//   - lifecycle queue: concurrent restarts cannot spawn multiple
//     untracked processes; teardown transitions the lifecycle to
//     closed synchronously before queueing cleanup so no later
//     restart can enqueue work; in-progress restart completes before
//     teardown runs
//   - transactional setup: every cleanup operation is attempted even
//     if a previous cleanup failed. The primary cause and every
//     cleanup error are surfaced together via AggregateError so the
//     caller never loses information.
//   - bind ports come from the OS: postgres publishes on
//     `127.0.0.1::5432` (runtime-assigned host port), the Go process
//     binds to 127.0.0.1:0 (OS-assigned port); the actual port is
//     discovered by parsing the "state-registry listening" JSON log
//     line emitted by the Go process. A per-spawn bind buffer is
//     cleared before each spawn so an old bind log cannot match.
//   - readiness polling uses per-request AbortSignal timeout and
//     cancels/drains response bodies so a stuck child never causes
//     a 30-second blind wait
//   - the AES-256-GCM key is generated once outside env() and
//     reused across restart so the same key encrypts the test
//     container and the live process
//   - bindPort and baseUrl are dynamic getters reflecting the current
//     process; callers never cache the URL across restart
//   - external process and container cleanup in finally ensures no
//     stale artifact remains even on a failed test
import { randomBytes } from "node:crypto";
import { spawn, type ChildProcess } from "node:child_process";
import { resolve as pathResolve } from "node:path";
import {
  StartPostgresContainer,
  StopPostgresContainer,
  type PostgresContainer,
} from "./postgres_container";
import { sanitize, truncateTail } from "./redact";

export interface RegistryWorkerOptions {
  binary?: string;
  /**
   * Launch the normal untagged binary with test mode unset. Production mode
   * requires HTTPS+mTLS; it exists so transport tests exercise the real
   * production route graph instead of the header-authenticated harness.
   */
  productionMode?: boolean;
  postgresImage?: string;
  removeFailureCount?: number;
  processKillFailureCount?: number;
  startupTimeoutMs?: number;
  omitAesKey?: boolean;
  /**
   * v0002.20 opt-in: TLS-mode startup. When ANY TLS option is set,
   * the worker forwards STATE_REGISTRY_TLS_* env vars to the Go
   * process and switches the readiness probe + baseUrl to HTTPS
   * with mTLS (require-and-verify-client-cert when the trusted
   * client CA is also provided). The default startRegistryWorker()
   * call (no TLS options) is byte-for-byte behavior compatible
   * with the plain-HTTP harness used by every other contract
   * test; no existing test is affected.
   */
  tlsServerCertPath?: string;
  tlsServerKeyPath?: string;
  tlsClientCaPath?: string;
  tlsRequireClientCert?: boolean;
  /**
   * v0002.20 opt-in: Postgres server-chain verification. When set,
   * the worker forwards STATE_REGISTRY_POSTGRES_TLS_* env vars
   * and the State Registry is expected to refuse to start (or
   * refuse to report ready) when the configured chain is
   * unverifiable.
   */
  tlsPostgresCaPath?: string;
  /**
   * v0002.20 opt-in: the postgres SERVER cert + key the fixture
   * stages into the ephemeral postgres:16 container so the
   * container presents a verifiable server chain. When ANY
   * postgres-TLS option is set, the worker forwards BOTH cert
   * and key to StartPostgresContainer and the postgres
   * container starts with `ssl=on` against the staged files.
   * The cert must be signed by a CA the State Registry's Go
   * client also trusts (e.g. tlsPostgresCaPath).
   */
  tlsPostgresServerCertPath?: string;
  tlsPostgresServerKeyPath?: string;
  tlsPostgresVerifyMode?: "verify-ca" | "verify-full";
  /**
   * Trusted client cert/key used by the readiness probe to verify
   * the HTTPS+mTLS listener. Required when tlsServerCertPath is
   * set; the worker uses the supplied cert/key to perform the
   * readiness request and to verify the server hostname.
   */
  tlsClientCertPath?: string;
  tlsClientKeyPath?: string;
  /**
   * Hostname the readiness probe uses for SNI + x509 hostname
   * verification. Default 'localhost'. The server cert's SAN
   * must include this value (or the OS-assigned 127.0.0.1 IP).
   */
  tlsServername?: string;
}

export interface RegistryWorker {
  postgres: PostgresContainer;
  bindHost: string;
  bindPort: number;
  baseUrl: string;
  workerExe: string;
  restartCount: number;
  restart(): Promise<RegistryRestartResult>;
  logs(maxBytes: number): string;
  teardown(): Promise<void>;
}

export interface RegistryRestartResult {
  sanitizedLogs: string;
  restartCount: number;
  startDurationMs: number;
  previousProcessExitCode: number | null;
  previousProcessSignal: NodeJS.Signals | null;
  postgresContainerId: string;
}

const DefaultBinary = (() => {
  const envOverride = process.env["STATE_REGISTRY_BINARY"];
  if (envOverride && envOverride.length > 0) {
    return envOverride;
  }
  return pathResolve(
    __dirname,
    "..",
    "..",
    "..",
    "state-registry",
    "cmd",
    "state-registry",
    "main.go",
  );
})();

const BindHost = "127.0.0.1";
const RequestPort = 0;
const ReadinessTimeoutMs = 15_000;
const BindDiscoveryTimeoutMs = 5_000;
const ReadinessPollMs = 100;
const CaptureMaxBytes = 64 * 1024;
const FetchTimeoutMs = 1_500;
const ProcessExitTimeoutMs = 5_000;
const ProcessExitBindTimeoutMs = 1_500;

const BindPattern = /"bind":"127\.0\.0\.1:(\d+)"/;

function sanitizeError(err: unknown): Error {
  if (err instanceof Error) {
    return new Error(sanitize(err.message));
  }
  return new Error(sanitize(String(err)));
}

function aggregateErrors(primary: Error, cleanups: Error[]): Error {
  if (cleanups.length === 0) {
    return primary;
  }
  return new AggregateError([primary, ...cleanups], sanitize(primary.message));
}

class FixtureLifecycleError extends Error {
  readonly phase: string;
  readonly diagnostics: string;
  readonly primary: Error;
  readonly cleanup: Error[];
  readonly aggregate: AggregateError;
  constructor(
    phase: string,
    diagnostics: string,
    primary: unknown,
    cleanup: Error[] = [],
  ) {
    const primaryError = sanitizeError(primary);
    const cleanupSummary = cleanup.length
      ? `; cleanup_errors=${cleanup.map((c) => c.message).join(", ")}`
      : "";
    super(
      `state-registry worker failed during ${phase}: ${primaryError.message}${cleanupSummary}`,
    );
    this.phase = phase;
    this.diagnostics = sanitize(diagnostics);
    this.primary = primaryError;
    this.cleanup = cleanup;
    this.name = "FixtureLifecycleError";
    this.aggregate = new AggregateError(
      [primaryError, ...cleanup],
      `state-registry worker failed during ${phase}`,
    );
  }
}

export async function startRegistryWorker(
  opts: RegistryWorkerOptions = {},
): Promise<RegistryWorker> {
  const binary = opts.binary ?? DefaultBinary;
  const lifecycle = new LifecycleQueue();
  const diagnostics = new BoundedCapture(CaptureMaxBytes);
  const aesKey = opts.omitAesKey ? "" : randomBytes(32).toString("hex");
  const cursorKey = randomBytes(32).toString("hex");
  const scopeTokenKey = randomBytes(32).toString("hex");
  const killFailuresRef = { count: opts.processKillFailureCount ?? 0 };

  async function terminateOrThrow(
    proc: ChildProcess,
    deadlineMs: number,
  ): Promise<{ code: number | null; signal: NodeJS.Signals | null }> {
    if (killFailuresRef.count > 0) {
      killFailuresRef.count -= 1;
      throw new Error("injected process kill failure");
    }
    return await terminateOrThrowCore(proc, deadlineMs);
  }
  const removeFailuresRef = { count: opts.removeFailureCount ?? 0 };

  let postgres: PostgresContainer | null = null;
  let current: ChildProcess | null = null;
  let bindPort: number | null = null;
  let bindCapture: BoundedCapture = new BoundedCapture(CaptureMaxBytes);
  let restartCount = 0;

  const tlsMode =
    opts.tlsServerCertPath !== undefined ||
    opts.tlsServerKeyPath !== undefined ||
    opts.tlsClientCaPath !== undefined ||
    opts.tlsRequireClientCert !== undefined ||
    opts.tlsPostgresCaPath !== undefined ||
    opts.tlsPostgresServerCertPath !== undefined ||
    opts.tlsPostgresServerKeyPath !== undefined ||
    opts.tlsPostgresVerifyMode !== undefined;

  if (opts.productionMode && !tlsMode) {
    throw new Error(
      "startRegistryWorker productionMode requires complete TLS configuration",
    );
  }

  const readyUrl = (port: number): string =>
    tlsMode ? `https://${BindHost}:${port}` : `http://${BindHost}:${port}`;

  // The probe materials the readiness check uses. The TLS-mode
  // worker refuses to start when these are missing or when the
  // files do not exist on disk, so the harness never accidentally
  // probes with rejectUnauthorized=false or with the wrong
  // identity.
  const tlsProbeCertPath = opts.tlsClientCertPath ?? null;
  const tlsProbeKeyPath = opts.tlsClientKeyPath ?? null;
  const tlsProbeCaPath = opts.tlsClientCaPath ?? null;
  const tlsProbeServername = opts.tlsServername ?? "localhost";

  // The TLS-mode startup contract is fail-closed: a worker that
  // is asked to serve mTLS must (a) receive every required file
  // path AND (b) have every file present on disk. We validate
  // here so the contract test cannot accidentally probe with
  // rejectUnauthorized=false or with the wrong identity.
  {
    if (tlsMode || tlsProbeCertPath || tlsProbeKeyPath || tlsProbeCaPath) {
      const required: string[] = [];
      if (tlsMode) {
        if (!opts.tlsServerCertPath) required.push("tlsServerCertPath");
        if (!opts.tlsServerKeyPath) required.push("tlsServerKeyPath");
        if (!opts.tlsClientCaPath) required.push("tlsClientCaPath");
      }
      if (tlsMode || tlsProbeCertPath || tlsProbeKeyPath) {
        if (!tlsProbeCertPath) required.push("tlsClientCertPath");
        if (!tlsProbeKeyPath) required.push("tlsClientKeyPath");
        if (!tlsProbeCaPath) required.push("tlsClientCaPath");
      }
      // Postgres-TLS opt-in: when ANY postgres-TLS knob is set
      // (CA, server-cert, server-key, or verify-mode), the
      // server-cert + key must be supplied together so the
      // fixture can stage them into the postgres container.
      if (
        opts.tlsPostgresCaPath !== undefined ||
        opts.tlsPostgresServerCertPath !== undefined ||
        opts.tlsPostgresServerKeyPath !== undefined ||
        opts.tlsPostgresVerifyMode !== undefined
      ) {
        if (!opts.tlsPostgresServerCertPath)
          required.push("tlsPostgresServerCertPath");
        if (!opts.tlsPostgresServerKeyPath)
          required.push("tlsPostgresServerKeyPath");
      }
      if (required.length > 0) {
        throw new Error(
          `startRegistryWorker TLS-mode contract is incomplete: missing ${required.join(", ")}; ` +
            "supplying the file-path is required to opt in",
        );
      }
      const pathsToCheck: Array<[string, string | null | undefined]> = [
        ["tlsServerCertPath", opts.tlsServerCertPath],
        ["tlsServerKeyPath", opts.tlsServerKeyPath],
        ["tlsClientCaPath", opts.tlsClientCaPath],
        ["tlsClientCertPath", tlsProbeCertPath],
        ["tlsClientKeyPath", tlsProbeKeyPath],
        ["tlsPostgresServerCertPath", opts.tlsPostgresServerCertPath],
        ["tlsPostgresServerKeyPath", opts.tlsPostgresServerKeyPath],
      ];
      const { existsSync } = require("node:fs") as typeof import("node:fs");
      const missing: string[] = [];
      for (const [name, path] of pathsToCheck) {
        if (path && !existsSync(path)) missing.push(`${name}=${path}`);
      }
      if (missing.length > 0) {
        throw new Error(
          `startRegistryWorker TLS-mode paths do not exist on disk: ${missing.join(", ")}`,
        );
      }
    }
  }

  const tlsEnv = (): Record<string, string> => {
    const out: Record<string, string> = {};
    if (opts.tlsServerCertPath) {
      out["STATE_REGISTRY_TLS_SERVER_CERT"] = opts.tlsServerCertPath;
    }
    if (opts.tlsServerKeyPath) {
      out["STATE_REGISTRY_TLS_SERVER_KEY"] = opts.tlsServerKeyPath;
    }
    if (opts.tlsClientCaPath) {
      out["STATE_REGISTRY_TLS_CLIENT_CA"] = opts.tlsClientCaPath;
    }
    if (opts.tlsRequireClientCert !== undefined) {
      out["STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT"] = opts.tlsRequireClientCert
        ? "true"
        : "false";
    }
    if (opts.tlsPostgresCaPath) {
      out["STATE_REGISTRY_POSTGRES_TLS_CA"] = opts.tlsPostgresCaPath;
    }
    if (opts.tlsPostgresVerifyMode) {
      out["STATE_REGISTRY_POSTGRES_TLS_MODE"] = opts.tlsPostgresVerifyMode;
    }
    return out;
  };

  const env = () => ({
    ...process.env,
    STATE_REGISTRY_BIND_HOST: BindHost,
    STATE_REGISTRY_BIND_PORT: String(RequestPort),
    STATE_REGISTRY_POSTGRES_URL: postgres!.dsn,
    STATE_REGISTRY_AES_KEY_HEX: aesKey,
    STATE_REGISTRY_CURSOR_KEY_HEX: cursorKey,
    STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX: scopeTokenKey,
    ...(opts.productionMode ? {} : { STATE_REGISTRY_TEST_MODE: "true" }),
    ...tlsEnv(),
  });

  const startProcess = (): ChildProcess => {
    const isGoSource = binary.endsWith(".go");
    const cmd = isGoSource ? "go" : binary;
    // The state_registry_test_harness tag is the only path that
    // opts into header-trusting test mode; a normal production
    // binary rejects STATE_REGISTRY_TEST_MODE=true outright.
    const harnessBuildTag = "state_registry_test_harness";
    const args = isGoSource
      ? opts.productionMode
        ? ["run", binary]
        : ["run", "-tags", harnessBuildTag, binary]
      : [];
    if (!isGoSource) {
      const exists = (() => {
        try {
          const stat = require("node:fs").statSync(cmd);
          return stat.isFile();
        } catch {
          return false;
        }
      })();
      if (!exists) {
        const err = new Error(`spawn ${cmd} ENOENT`);
        (err as NodeJS.ErrnoException).code = "ENOENT";
        throw err;
      }
    }
    return spawn(cmd, args, {
      stdio: ["ignore", "pipe", "pipe"],
      env: env(),
      detached: true,
    });
  };

  async function discoverBindPort(
    child: ChildProcess,
    deadlineMs: number,
  ): Promise<number> {
    bindCapture = new BoundedCapture(CaptureMaxBytes);
    bindCapture.child(child);
    return await new Promise<number>((resolve, reject) => {
      let settled = false;
      const timer = setTimeout(() => {
        if (settled) return;
        settled = true;
        cleanup();
        reject(
          new Error(`process never logged bind port within ${deadlineMs}ms`),
        );
      }, deadlineMs);
      const cleanup = () => {
        child.stdout?.off("data", onChunk);
        child.stderr?.off("data", onChunk);
        child.off("exit", onExit);
        child.off("error", onError);
      };
      const onChunk = () => {
        const match = bindCapture.snapshot().match(BindPattern);
        if (match && match[1] && !settled) {
          settled = true;
          cleanup();
          resolve(Number(match[1]));
        }
      };
      const onExit = (code: number | null, signal: NodeJS.Signals | null) => {
        if (settled) return;
        settled = true;
        cleanup();
        reject(
          new Error(
            `process exited before logging bind port code=${String(code)} signal=${String(signal)}`,
          ),
        );
      };
      const onError = (err: Error) => {
        if (settled) return;
        settled = true;
        cleanup();
        reject(err);
      };
      child.stdout?.on("data", onChunk);
      child.stderr?.on("data", onChunk);
      child.once("exit", onExit);
      child.once("error", onError);
    });
  }

  // httpsProbeReady issues ONE HTTPS request to the worker with
  // mTLS. The probe never disables rejectUnauthorized and never
  // falls back to plain HTTP. The returned promise resolves with
  // { status, body, ok } on success and rejects with the raw TLS
  // error otherwise. The contract test consumes this when the
  // worker is in TLS mode and the Go scaffold has not yet
  // implemented HTTPS, so the rejection becomes a precise
  // behavior-specific RED.
  async function httpsProbeReady(
    url: string,
    timeoutMs: number,
  ): Promise<{ status: number; body: string }> {
    if (!tlsProbeCertPath || !tlsProbeKeyPath || !tlsProbeCaPath) {
      throw new Error(
        "httpsProbeReady called without tlsClientCertPath/tlsClientKeyPath/tlsClientCaPath",
      );
    }
    const { Agent: HttpsAgent, request: nodeHttpsRequest } = await import(
      "node:https"
    );
    const { readFileSync } = await import("node:fs");
    const agent = new HttpsAgent({
      cert: readFileSync(tlsProbeCertPath),
      key: readFileSync(tlsProbeKeyPath),
      ca: readFileSync(tlsProbeCaPath),
      servername: tlsProbeServername,
      rejectUnauthorized: true,
      keepAlive: false,
    });
    const timeout = Math.max(timeoutMs, FetchTimeoutMs);
    return await new Promise<{ status: number; body: string }>(
      (resolve, reject) => {
        const req = nodeHttpsRequest(
          `${url}/v1/readyz`,
          { method: "GET", agent, timeout },
          (res) => {
            const chunks: Buffer[] = [];
            res.on("data", (chunk: Buffer) => chunks.push(chunk));
            res.on("end", () =>
              resolve({
                status: res.statusCode ?? 0,
                body: Buffer.concat(chunks).toString("utf-8"),
              }),
            );
          },
        );
        req.on("error", (err) => reject(err));
        req.on("timeout", () => {
          req.destroy(
            new Error(
              `httpsProbeReady timed out after ${timeout}ms url=${url}`,
            ),
          );
        });
        req.end();
      },
    );
  }

  async function waitForReady(url: string, timeoutMs: number): Promise<void> {
    const deadline = Date.now() + timeoutMs;
    let lastErr: unknown;
    while (Date.now() < deadline) {
      const ctl = new AbortController();
      const timer = setTimeout(() => ctl.abort(), FetchTimeoutMs);
      try {
        if (tlsMode) {
          try {
            const probe = await httpsProbeReady(url, FetchTimeoutMs);
            if (probe.status === 200) {
              clearTimeout(timer);
              return;
            }
            lastErr = new Error(
              `readyz status=${String(probe.status)} body=${probe.body.slice(0, 256)}`,
            );
          } catch (err) {
            lastErr = err;
          }
        } else {
          const resp = await fetch(`${url}/v1/readyz`, { signal: ctl.signal });
          if (resp.status === 200) {
            await drainResponse(resp);
            clearTimeout(timer);
            return;
          }
          lastErr = new Error(`readyz status=${String(resp.status)}`);
          await drainResponse(resp);
        }
      } catch (err) {
        lastErr = err;
      } finally {
        clearTimeout(timer);
      }
      await new Promise((r) => setTimeout(r, ReadinessPollMs));
    }
    const lastErrMsg =
      lastErr instanceof Error ? lastErr.message : String(lastErr);
    if (tlsMode) {
      throw new Error(
        `state-registry TLS-mode readiness probe never reached 200 within ${timeoutMs}ms ` +
          `(last err=${lastErrMsg}); verify that the HTTPS/mTLS listener is bound on ${url} ` +
          `and that State Registry received STATE_REGISTRY_TLS_SERVER_CERT, ` +
          `STATE_REGISTRY_TLS_SERVER_KEY, STATE_REGISTRY_TLS_CLIENT_CA, and ` +
          `STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT=true with a client cert trusted by that CA.`,
      );
    }
    throw new Error(
      `state-registry never reported ready within ${timeoutMs}ms (last err=${lastErrMsg})`,
    );
  }

  // stopPostgres wraps StopPostgresContainer with the
  // remove-failure-injection counter. Each call decrements the
  // counter; while > 0, a synthetic error is returned instead of the
  // real remove result.
  async function stopPostgres(
    pg: PostgresContainer | null,
  ): Promise<Error | null> {
    if (!pg) {
      return null;
    }
    return await StopPostgresContainer(pg, removeFailuresRef);
  }

  try {
    await lifecycle.run(async () => {
      try {
        postgres = await StartPostgresContainer({
          image: opts.postgresImage,
          // When ANY postgres-TLS opt-in knob is set, forward the
          // server cert + key to the fixture. The fixture stages
          // the files inside the postgres:16 container and starts
          // the server with `ssl=on`. When unset, the fixture
          // takes the default plaintext path (preserved
          // byte-for-byte for every other contract test).
          tlsPostgresCertPath: opts.tlsPostgresServerCertPath,
          tlsPostgresKeyPath: opts.tlsPostgresServerKeyPath,
        });
      } catch (primary) {
        throw new FixtureLifecycleError("postgres-startup", "", primary);
      }
      try {
        current = startProcess();
        diagnostics.child(current);
      } catch (primary) {
        const pg = postgres;
        postgres = null;
        const cleanupErrs = await collectCleanupErrors(
          undefined,
          pg,
          removeFailuresRef,
        );
        throw new FixtureLifecycleError(
          "process-spawn",
          "",
          primary,
          cleanupErrs,
        );
      }
      try {
        bindPort = await discoverBindPort(current, BindDiscoveryTimeoutMs);
      } catch (primary) {
        const pg = postgres;
        const child = current;
        postgres = null;
        current = null;
        const cleanupErrs = await collectCleanupErrors(
          child,
          pg,
          removeFailuresRef,
        );
        throw new FixtureLifecycleError(
          "process-bind-discovery",
          diagnostics.snapshot(),
          primary,
          cleanupErrs,
        );
      }
      try {
        await waitForReady(readyUrl(bindPort), ReadinessTimeoutMs);
      } catch (primary) {
        const pg = postgres;
        const child = current;
        postgres = null;
        current = null;
        const cleanupErrs = await collectCleanupErrors(
          child,
          pg,
          removeFailuresRef,
        );
        throw new FixtureLifecycleError(
          "readiness",
          diagnostics.snapshot(),
          primary,
          cleanupErrs,
        );
      }
    });
  } catch (err) {
    // Outer catch: attempt every remaining cleanup, collect
    // every error, throw AggregateError(primary, ...cleanups).
    const primary = err instanceof Error ? err : new Error(String(err));
    const cleanups: Error[] = [];
    if (current) {
      const child = current;
      current = null;
      try {
        await terminateOrThrow(child, ProcessExitBindTimeoutMs);
      } catch (cleanupErr) {
        cleanups.push(sanitizeError(cleanupErr));
      }
    }
    if (postgres) {
      const pg = postgres;
      postgres = null;
      const stopErr = await stopPostgres(pg);
      if (stopErr) {
        cleanups.push(stopErr);
      }
    }
    if (cleanups.length > 0) {
      throw aggregateErrors(primary, cleanups);
    }
    throw primary;
  }

  if (!postgres || bindPort === null || !current) {
    throw new Error(
      "startRegistryWorker invariant: lifecycle.run did not initialise worker state",
    );
  }

  return {
    postgres,
    bindHost: BindHost,
    get bindPort(): number {
      return bindPort ?? 0;
    },
    get baseUrl(): string {
      const p = bindPort;
      if (p === null) {
        throw new Error("baseUrl unavailable: worker has no current bind port");
      }
      return tlsMode ? `https://${BindHost}:${p}` : `http://${BindHost}:${p}`;
    },
    workerExe: binary,
    get restartCount(): number {
      return restartCount;
    },
    async restart(): Promise<RegistryRestartResult> {
      if (!lifecycle.canAcceptNew()) {
        throw new Error("cannot restart: worker is already torn down");
      }
      const startedAt = Date.now();
      let previousExit: { code: number | null; signal: NodeJS.Signals | null } =
        { code: null, signal: null };

      await lifecycle.run(async () => {
        const previous = current;
        if (previous) {
          previousExit = await terminateOrThrow(previous, ProcessExitTimeoutMs);
          current = null;
        }
        try {
          current = startProcess();
          diagnostics.child(current);
        } catch (primary) {
          throw new FixtureLifecycleError(
            "restart-process-spawn",
            diagnostics.snapshot(),
            primary,
          );
        }
        try {
          bindPort = await discoverBindPort(current, BindDiscoveryTimeoutMs);
        } catch (primary) {
          const child = current;
          current = null;
          if (child) {
            let killErr: Error | undefined;
            try {
              await terminateOrThrow(child, ProcessExitBindTimeoutMs);
            } catch (e) {
              killErr = e instanceof Error ? e : new Error(String(e));
            }
            throw new FixtureLifecycleError(
              "restart-bind-discovery",
              diagnostics.snapshot(),
              primary,
              killErr ? [killErr] : [],
            );
          }
          throw new FixtureLifecycleError(
            "restart-bind-discovery",
            diagnostics.snapshot(),
            primary,
          );
        }
        try {
          await waitForReady(readyUrl(bindPort), ReadinessTimeoutMs);
        } catch (primary) {
          const child = current;
          current = null;
          if (child) {
            let killErr: Error | undefined;
            try {
              await terminateOrThrow(child, ProcessExitBindTimeoutMs);
            } catch (e) {
              killErr = e instanceof Error ? e : new Error(String(e));
            }
            throw new FixtureLifecycleError(
              "restart-readiness",
              diagnostics.snapshot(),
              primary,
              killErr ? [killErr] : [],
            );
          }
          throw new FixtureLifecycleError(
            "restart-readiness",
            diagnostics.snapshot(),
            primary,
          );
        }
      });

      restartCount += 1;
      return {
        sanitizedLogs: sanitize(diagnostics.snapshot()),
        restartCount,
        startDurationMs: Date.now() - startedAt,
        previousProcessExitCode: previousExit.code,
        previousProcessSignal: previousExit.signal,
        postgresContainerId: postgres!.handle.id,
      };
    },
    logs(maxBytes: number): string {
      return sanitize(truncateTail(diagnostics.snapshot(), maxBytes));
    },
    async teardown(): Promise<void> {
      if (!lifecycle.rejectNew()) {
        return;
      }
      const cleanupErrors: Error[] = [];
      try {
        await lifecycle.run(async () => {
          const child = current;
          if (child) {
            current = null;
            try {
              await terminateOrThrow(child, ProcessExitTimeoutMs);
            } catch (err) {
              cleanupErrors.push(sanitizeError(err));
            }
          }
          const pg = postgres;
          if (pg) {
            postgres = null;
            const stopErr = await stopPostgres(pg);
            if (stopErr) {
              cleanupErrors.push(stopErr);
            }
          }
        });
      } finally {
        lifecycle.markClosed();
      }
      if (cleanupErrors.length > 0) {
        throw aggregateErrors(
          new Error("teardown reported cleanup failures"),
          cleanupErrors,
        );
      }
    },
  };
}

async function collectCleanupErrors(
  child: ChildProcess | null | undefined,
  pg: PostgresContainer,
  removeFailureCountRef: { count: number },
): Promise<Error[]> {
  const errors: Error[] = [];
  if (child) {
    try {
      await terminateOrThrowCore(child, ProcessExitBindTimeoutMs);
    } catch (err) {
      errors.push(sanitizeError(err));
    }
  }
  const stopErr = await StopPostgresContainer(pg, removeFailureCountRef);
  if (stopErr) {
    errors.push(stopErr);
  }
  return errors;
}

class LifecycleQueue {
  private mutex: Promise<void> = Promise.resolve();
  private closed = false;
  private acceptsNew = true;
  async run(task: () => Promise<void>): Promise<void> {
    if (this.closed) {
      throw new Error("lifecycle closed");
    }
    const next = this.mutex.then(task);
    this.mutex = next.then(
      () => undefined,
      () => undefined,
    );
    await next;
  }
  isClosed(): boolean {
    return this.closed;
  }
  rejectNew(): boolean {
    if (!this.acceptsNew) {
      return false;
    }
    this.acceptsNew = false;
    return true;
  }
  markClosed(): void {
    this.closed = true;
  }
  canAcceptNew(): boolean {
    return this.acceptsNew && !this.closed;
  }
}

class BoundedCapture {
  private chunks: string[] = [];
  private bytes = 0;
  constructor(private readonly cap: number) {}
  snapshot(): string {
    return this.chunks.join("");
  }
  child(proc: ChildProcess): void {
    const onChunk = (b: Buffer) => {
      const text = sanitize(b.toString("utf-8"));
      this.chunks.push(text);
      this.bytes += text.length;
      while (this.bytes > this.cap && this.chunks.length > 1) {
        const dropped = this.chunks.shift();
        if (dropped !== undefined) {
          this.bytes -= dropped.length;
        }
      }
    };
    proc.stdout?.on("data", onChunk);
    proc.stderr?.on("data", onChunk);
  }
}

// isEsrch returns true when an error indicates "no such process",
// meaning the target process has already exited before we could
// signal it. Such an error is benign and must not abort cleanup.
function isEsrch(err: unknown): boolean {
  if (!err || typeof err !== "object") return false;
  const code = (err as { code?: unknown }).code;
  if (code === "ESRCH") return true;
  const message = (err as { message?: unknown }).message;
  if (typeof message === "string" && /no such process/i.test(message)) {
    return true;
  }
  return false;
}

// terminateOrThrow awaits the actual exit event after SIGTERM. If
// SIGTERM does not produce an exit within the deadline, the helper
// escalates to SIGKILL and waits for the actual exit. If the process
// is still alive after the kill budget, the helper throws a bounded
// termination error. Already-exited processes return synchronously.
// Only ESRCH (process already gone) is treated as benign; any other
// signal error is recorded and the helper falls back to direct
// SIGKILL on the child PID.
async function terminateOrThrowCore(
  proc: ChildProcess,
  deadlineMs: number,
): Promise<{ code: number | null; signal: NodeJS.Signals | null }> {
  if (proc.exitCode !== null || proc.signalCode !== null) {
    return {
      code: proc.exitCode,
      signal: proc.signalCode as NodeJS.Signals | null,
    };
  }
  let exitCode: number | null = null;
  let exitSignal: NodeJS.Signals | null = null;
  const pid = proc.pid;
  let sigtermError: Error | undefined;
  try {
    if (pid !== undefined) {
      process.kill(-pid, "SIGTERM");
    } else {
      proc.kill("SIGTERM");
    }
  } catch (err) {
    if (!isEsrch(err)) {
      sigtermError = err instanceof Error ? err : new Error(String(err));
    }
  }
  const exited = await new Promise<{
    code: number | null;
    signal: NodeJS.Signals | null;
  } | null>((resolve) => {
    const timer = setTimeout(() => resolve(null), deadlineMs);
    proc.once("exit", (code, signal) => {
      clearTimeout(timer);
      resolve({ code, signal: signal as NodeJS.Signals | null });
    });
  });
  if (exited !== null) {
    exitCode = exited.code;
    exitSignal = exited.signal;
    if (sigtermError) {
      throw sigtermError;
    }
    return { code: exitCode, signal: exitSignal };
  }
  // Did not exit within deadline. Escalate to SIGKILL on the process
  // group (or the child PID if no group is available).
  let sigkillError: Error | undefined;
  try {
    if (pid !== undefined) {
      process.kill(-pid, "SIGKILL");
    } else {
      proc.kill("SIGKILL");
    }
  } catch (err) {
    if (!isEsrch(err)) {
      sigkillError = err instanceof Error ? err : new Error(String(err));
    }
  }
  const exitedAfterKill = await new Promise<{
    code: number | null;
    signal: NodeJS.Signals | null;
  } | null>((resolve) => {
    const killTimer = setTimeout(() => resolve(null), 5_000);
    proc.once("exit", (code, signal) => {
      clearTimeout(killTimer);
      resolve({ code, signal: signal as NodeJS.Signals | null });
    });
  });
  if (exitedAfterKill === null) {
    const err = new AggregateError(
      [
        new Error(
          `process did not exit within ${deadlineMs}ms after SIGTERM and within 5s after SIGKILL`,
        ),
        ...(sigtermError ? [sigtermError] : []),
        ...(sigkillError ? [sigkillError] : []),
      ],
      "terminateOrThrow: process refused to exit",
    );
    throw err;
  }
  exitCode = exitedAfterKill.code;
  exitSignal = exitedAfterKill.signal;
  const collected: Error[] = [];
  if (sigtermError) collected.push(sigtermError);
  if (sigkillError) collected.push(sigkillError);
  if (collected.length > 0) {
    throw new AggregateError(
      collected,
      "terminateOrThrow: signaling reported errors",
    );
  }
  return { code: exitCode, signal: exitSignal };
}

async function drainResponse(resp: Response): Promise<void> {
  try {
    if (resp.body) {
      await resp.body.cancel();
    }
  } catch {
    // best-effort
  }
}
