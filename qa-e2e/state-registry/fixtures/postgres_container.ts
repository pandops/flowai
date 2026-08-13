// PostgreSQL container lifecycle for the state-registry qa-e2e suite.
// The harness starts an ephemeral postgres:16 container bound EXCLUSIVELY
// to loopback via `--publish 127.0.0.1::5432`, then asks the runtime
// for the OS-assigned host port. Real cleanup failures propagate.
//
// TLS-mode (opt-in):
//   When tlsPostgresCertPath + tlsPostgresKeyPath are supplied, the
//   fixture stages the cert + key in a private tmpdir and writes a
//   small custom entrypoint next to them. The custom entrypoint
//   copies the cert + key into /var/lib/postgresql/ (writable on
//   the postgres image's parent directory) with ownership
//   postgres:postgres and mode 0600 (key) / 0644 (cert), then
//   execs the original /usr/local/bin/docker-entrypoint.sh with
//   the same args. The fixture mounts the staging dir as
//   /etc/flowai/pg-tls (read-only) and the custom entrypoint as
//   /custom-entrypoint.sh, and overrides --entrypoint to that
//   path. The fixture also appends
//   `-c ssl=on -c ssl_cert_file=... -c ssl_key_file=...` to the
//   postgres command line so the temp server inside the
//   entrypoint finds the cert + key on first start. The DSN
//   stays byte-for-byte identical to the non-TLS default; the
//   State Registry's Go client overrides sslmode/sslrootcert
//   via the STATE_REGISTRY_POSTGRES_TLS_* env vars when the
//   v0002.20 transport contract demands verify-full. When no
//   TLS options are supplied, the existing default path
//   (plaintext, sslmode=disable) is preserved byte-for-byte.
import { randomBytes } from "node:crypto";
import { chmodSync, existsSync, rmSync } from "node:fs";
import { copyFile, mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve as pathResolve } from "node:path";
import {
  DetectRuntime,
  type ContainerRuntime,
  type ContainerHandle,
  ContainerRuntimeError,
} from "./container_runtime";

export interface PostgresContainer {
  handle: ContainerHandle;
  dsn: string;
  password: string;
  hostPort: number;
  containerPort: number;
  runtime: ContainerRuntime;
  /**
   * When TLS mode is active, the staged cert/key/initscript
   * directory inside the host filesystem. The fixture removes the
   * directory on every successful Stop AND on every failed-stop
   * cleanup path. The directory is private to the fixture and is
   * only readable by the current process.
   */
  tlsStagingDir: string | null;
}

export interface PostgresContainerOptions {
  image?: string;
  databaseName?: string;
  containerPort?: number;
  /**
   * v0002.20 opt-in: TLS material. When BOTH tlsPostgresCertPath
   * and tlsPostgresKeyPath are set, the fixture starts the
   * postgres container with `ssl=on` and the supplied cert/key,
   * signed by a CA the State Registry's Go client also trusts.
   * When unset (the default), the postgres container listens on
   * plain TCP and the DSN uses sslmode=disable; this is the path
   * every other contract test in the suite uses.
   */
  tlsPostgresCertPath?: string;
  tlsPostgresKeyPath?: string;
  // Test-only fault-injection: the first N `remove` calls throw
  // a synthetic container runtime error. Use to deterministically
  // exercise the primary+cleanup AggregateError path.
  removeFailureCount?: number;
}

const DefaultImage = "docker.io/library/postgres:16";
const DefaultContainerPort = 5432;
const DefaultDatabaseName = "flowai";

// sslmode=disable is acceptable only for loopback test transport to
// a fresh ephemeral postgres:16 container; production registries never
// use a disabled SSL mode. The State Registry's Go client overrides
// sslmode via STATE_REGISTRY_POSTGRES_TLS_MODE in the v0002.20
// transport contract, so the DSN we hand the Go process can stay
// `sslmode=disable` regardless of whether the postgres container
// itself is serving TLS.
const TlsHint = "sslmode=disable";

// startupPollInterval bounds the readiness poll inside the harness.
const startupPollInterval = 100;

// startupTimeoutMs bounds the wait for postgres to accept SQL clients.
const startupTimeoutMs = 20_000;
const readyLogLine = "database system is ready to accept connections";

// In-container paths the fixture writes to when TLS mode is active.
// The staging dir is mounted at /etc/flowai/pg-tls (read-only). The
// custom entrypoint copies the cert + key from there into the
// postgres data-parent directory (/var/lib/postgresql/) and sets
// ownership / mode to what postgres enforces, then execs the
// original entrypoint. The postgres process then reads the cert
// + key from /var/lib/postgresql/server.{crt,key} via -c.
const InContainerTlsDir = "/etc/flowai/pg-tls";
const InContainerStagedCertName = "server.crt";
const InContainerStagedKeyName = "server.key";
const InContainerEntrypointName = "custom-entrypoint.sh";
const InContainerEntrypointPath = `/${InContainerEntrypointName}`;
const InContainerFinalCert = "/var/lib/postgresql/server.crt";
const InContainerFinalKey = "/var/lib/postgresql/server.key";

export async function StartPostgresContainer(
  opts: PostgresContainerOptions = {},
): Promise<PostgresContainer> {
  const runtime = await DetectRuntime();
  const image = opts.image ?? DefaultImage;
  const containerPort = opts.containerPort ?? DefaultContainerPort;
  const password = randomBytes(16).toString("hex");
  const containerName = `state-registry-pg-${Date.now()}-${randomBytes(4).toString("hex")}`;
  const databaseName = opts.databaseName ?? DefaultDatabaseName;
  const removeFailureCountRef = { count: opts.removeFailureCount ?? 0 };
  const tlsPostgresCertPath = opts.tlsPostgresCertPath ?? null;
  const tlsPostgresKeyPath = opts.tlsPostgresKeyPath ?? null;
  if ((tlsPostgresCertPath === null) !== (tlsPostgresKeyPath === null)) {
    throw new Error(
      "StartPostgresContainer: tlsPostgresCertPath and tlsPostgresKeyPath must be supplied together",
    );
  }
  if (tlsPostgresCertPath !== null && tlsPostgresKeyPath !== null) {
    if (!existsSync(tlsPostgresCertPath)) {
      throw new Error(
        `StartPostgresContainer: tlsPostgresCertPath does not exist on disk: ${tlsPostgresCertPath}`,
      );
    }
    if (!existsSync(tlsPostgresKeyPath)) {
      throw new Error(
        `StartPostgresContainer: tlsPostgresKeyPath does not exist on disk: ${tlsPostgresKeyPath}`,
      );
    }
  }

  await runtime.pull(image);

  // Stage the TLS material + custom entrypoint in a private
  // tmpdir. The tmpdir is created BEFORE the container so the
  // mount path is stable; the fixture removes it on every
  // successful start AND on every failed-start cleanup path. The
  // custom entrypoint is a minimal bash that runs as root BEFORE
  // the postgres entrypoint, copies the staged cert + key into
  // the postgres data-parent directory with the ownership / mode
  // postgres enforces, and execs the original entrypoint. The
  // script never prints the cert bytes or the key bytes.
  let tlsStagingDir: string | null = null;
  if (tlsPostgresCertPath !== null && tlsPostgresKeyPath !== null) {
    tlsStagingDir = await stageTlsMaterial(
      tlsPostgresCertPath,
      tlsPostgresKeyPath,
    );
  }
  const removeTlsStaging = (): void => {
    if (tlsStagingDir !== null) {
      // Swallow the staging-removal error here because the
      // caller is already in a failure-cleanup path; the
      // surfaced primary error is the test's diagnostic of
      // record. StopPostgresContainer is the path that surfaces
      // staging-removal errors as part of its cleanup contract.
      removeTlsStagingDir(tlsStagingDir);
      tlsStagingDir = null;
    }
  };

  const runArgs: string[] = [
    "run",
    "--detach",
    "--rm",
    "--name",
    containerName,
    // Loopback-only publication: the host port is OS-assigned and
    // never reachable from outside the test runner.
    "--publish",
    `127.0.0.1::${containerPort}`,
    // The postgres image declares /var/lib/postgresql/data as a volume.
    // Rootless Podman retains that anonymous volume after --rm, so repeated
    // test runs can fill the host. PG data is test-only and belongs in tmpfs.
    "--tmpfs",
    "/var/lib/postgresql/data:rw",
    "--env",
    `POSTGRES_PASSWORD=${password}`,
    "--env",
    `POSTGRES_DB=${databaseName}`,
  ];
  if (tlsStagingDir !== null) {
    // Mount the staged cert + key as a read-only bind. The
    // custom entrypoint reads from here and writes to
    // /var/lib/postgresql/ (the postgres data-parent directory,
    // which is writable because the data subdirectory lives on
    // tmpfs, NOT the parent).
    runArgs.push(
      "--mount",
      `type=bind,source=${tlsStagingDir},target=${InContainerTlsDir},readonly`,
    );
    // The custom entrypoint is a single small bash file that
    // copies the cert + key and execs the original entrypoint.
    // We bind it on top of /custom-entrypoint.sh and override
    // the image's ENTRYPOINT to that path.
    runArgs.push(
      "--mount",
      `type=bind,source=${pathResolve(tlsStagingDir, InContainerEntrypointName)},target=${InContainerEntrypointPath},readonly`,
    );
    runArgs.push("--entrypoint", InContainerEntrypointPath);
  }
  runArgs.push(image);
  if (tlsStagingDir !== null) {
    // Override the postgres command with the explicit SSL config
    // and reference the staged cert / key paths. The custom
    // entrypoint has already copied the files into the data
    // parent with the right ownership / mode, so the postgres
    // process (running as the postgres user) can read them on
    // first start.
    runArgs.push(
      "postgres",
      "-c",
      "ssl=on",
      "-c",
      `ssl_cert_file=${InContainerFinalCert}`,
      "-c",
      `ssl_key_file=${InContainerFinalKey}`,
    );
  }

  let handle: ContainerHandle;
  try {
    handle = await runtime.runDetached(runArgs);
  } catch (primary) {
    removeTlsStaging();
    throw primary;
  }

  let hostPort: number;
  try {
    hostPort = await pollMappedHostPort(runtime, handle, containerPort);
  } catch (primary) {
    const cleanupError = await StopPostgresContainer(
      handle
        ? {
            handle,
            dsn: "",
            password,
            hostPort: 0,
            containerPort,
            runtime,
            tlsStagingDir,
          }
        : null,
      removeFailureCountRef,
    );
    if (cleanupError) {
      throw new AggregateError(
        [primary, cleanupError],
        `StartPostgresContainer failed during mapped-port poll: ${String(primary)}`,
      );
    }
    throw primary;
  }

  try {
    await waitForPostgresReady(
      runtime,
      handle,
      hostPort,
      databaseName,
      startupTimeoutMs,
    );
  } catch (primary) {
    const cleanupError = await StopPostgresContainer(
      handle
        ? {
            handle,
            dsn: "",
            password,
            hostPort: 0,
            containerPort,
            runtime,
            tlsStagingDir,
          }
        : null,
      removeFailureCountRef,
    );
    if (cleanupError) {
      throw new AggregateError(
        [primary, cleanupError],
        `StartPostgresContainer failed during readiness: ${String(primary)}`,
      );
    }
    throw primary;
  }

  const dsn = `postgresql://postgres:${encodeURIComponent(password)}@127.0.0.1:${hostPort}/${databaseName}?${TlsHint}`;
  return {
    handle,
    dsn,
    password,
    hostPort,
    containerPort,
    runtime,
    tlsStagingDir,
  };
}

// StopPostgresContainer surfaces every cleanup error. The fixture
// guarantees the test fails if the runtime refuses to remove a
// container that is still tracked by the harness. Returns the
// container runtime error so callers can aggregate it with the
// primary cause; null if removal succeeded or the container was
// already gone. The TLS staging dir is removed on every successful
// stop AND on every cleanup-error path so the test runner never
// leaks host-side secret material. When both removal steps fail,
// the returned Error is an AggregateError so the caller can
// observe every failure without losing any one.
export async function StopPostgresContainer(
  container: PostgresContainer | null,
  removeFailureCountRef: { count: number } = { count: 0 },
): Promise<Error | null> {
  if (!container) {
    return null;
  }
  if (removeFailureCountRef.count > 0) {
    removeFailureCountRef.count -= 1;
    const stagingErr = removeTlsStagingDir(container.tlsStagingDir);
    if (stagingErr) {
      return new AggregateError(
        [new Error("injected postgres remove failure"), stagingErr],
        "injected postgres remove failure + tls staging cleanup failure",
      );
    }
    return new Error("injected postgres remove failure");
  }
  const containerErr = await safeRemove(container.runtime, container.handle);
  // ALWAYS remove the staging dir, even on remove failure, so the
  // test does not leak host-side key material. safeRemove above
  // already tolerates already-removed containers. When the
  // staging removal itself fails, surface it to the caller so
  // the test can fail loudly rather than silently leaking host-
  // side key material.
  const stagingErr = removeTlsStagingDir(container.tlsStagingDir);
  if (containerErr && stagingErr) {
    return new AggregateError(
      [containerErr, stagingErr],
      `container remove + tls staging cleanup failed: ${containerErr.message}`,
    );
  }
  if (containerErr) {
    return containerErr;
  }
  return stagingErr;
}

// removeTlsStagingDir removes the staging dir and returns either
// null (success or path was null) or an Error describing a real
// removal failure. ENOENT resolves to null (the dir is already
// gone, which is the success state for an idempotent cleanup).
// Any other error is surfaced so the caller (StopPostgresContainer
// or the test) can decide how to react.
function removeTlsStagingDir(path: string | null): Error | null {
  if (path === null) {
    return null;
  }
  try {
    rmSync(path, { recursive: true, force: true });
    return null;
  } catch (err) {
    const code = (err as { code?: string } | null | undefined)?.code;
    if (code === "ENOENT") {
      return null;
    }
    return err instanceof Error ? err : new Error(String(err));
  }
}

async function safeRemove(
  runtime: ContainerRuntime,
  handle: ContainerHandle,
): Promise<Error | null> {
  try {
    await runtime.remove(handle);
    return null;
  } catch (err) {
    if (
      err instanceof ContainerRuntimeError &&
      runtime.isAlreadyRemovedError(err.stderr)
    ) {
      return null;
    }
    return err instanceof Error ? err : new Error(String(err));
  }
}

// stageTlsMaterial creates a private tmpdir containing:
//   - the postgres server cert (copied from the caller-supplied path)
//   - the postgres server key (copied from the caller-supplied path)
//   - a custom entrypoint that copies the cert + key into the
//     postgres data-parent directory with ownership
//     postgres:postgres and mode 0600 (key) / 0644 (cert), then
//     execs the original /usr/local/bin/docker-entrypoint.sh.
//
// The custom entrypoint is the only piece of logic that touches
// the files inside the container. It runs as root (before
// postgres drops privileges) and never prints the cert bytes or
// the key bytes. The fixture passes the tmpdir path to the
// runtime via a bind mount, and the entrypoint handles the rest.
async function stageTlsMaterial(
  certPath: string,
  keyPath: string,
): Promise<string> {
  const stagingDir = await mkdtemp(join(tmpdir(), "flowai-pg-tls-"));
  // Security: owner-only (0700) on the staging dir so the key is
  // never readable by other users on a shared host.
  chmodSync(stagingDir, 0o700);
  const stagedCert = pathResolve(stagingDir, InContainerStagedCertName);
  const stagedKey = pathResolve(stagingDir, InContainerStagedKeyName);
  const stagedEntrypoint = pathResolve(stagingDir, InContainerEntrypointName);
  // Copy the cert + key from the caller's paths so the bind
  // mount surfaces exactly the files the custom entrypoint
  // expects, regardless of how the caller named them. RSA 2048
  // keys are ~1.7KB; certs ~1.3KB so the copy is effectively
  // atomic.
  await copyFile(certPath, stagedCert);
  await copyFile(keyPath, stagedKey);
  // Security: key is owner-only (0600) so a leaked staging dir
  // never carries a world-readable postgres server key. The cert
  // stays world-readable (0644) so the in-container postgres
  // process can load it after chown to postgres.
  chmodSync(stagedCert, 0o644);
  chmodSync(stagedKey, 0o600);
  // The custom entrypoint copies the mounted cert + key into
  // /var/lib/postgresql/ (writable) with ownership
  // postgres:postgres and mode 0600 (key) / 0644 (cert), then
  // execs the original docker-entrypoint.sh. It runs as root
  // (the entrypoint chain drops to postgres AFTER the custom
  // entrypoint completes) so the chown succeeds on every
  // container runtime, including rootless Podman / Docker where
  // the host file uid may not match the container uid. The
  // script never prints the cert bytes or the key bytes.
  const entrypointScript = [
    "#!/bin/bash",
    "set -eu",
    `SRC_DIR="${InContainerTlsDir}"`,
    `DST_CERT="${InContainerFinalCert}"`,
    `DST_KEY="${InContainerFinalKey}"`,
    'cp -f "${SRC_DIR}/server.crt" "${DST_CERT}"',
    'cp -f "${SRC_DIR}/server.key" "${DST_KEY}"',
    'chown postgres:postgres "${DST_CERT}" "${DST_KEY}"',
    'chmod 0644 "${DST_CERT}"',
    'chmod 0600 "${DST_KEY}"',
    'exec /usr/local/bin/docker-entrypoint.sh "$@"',
    "",
  ].join("\n");
  await writeFile(stagedEntrypoint, entrypointScript, { mode: 0o755 });
  return stagingDir;
}

async function pollMappedHostPort(
  runtime: ContainerRuntime,
  handle: ContainerHandle,
  containerPort: number,
): Promise<number> {
  const deadline = Date.now() + startupTimeoutMs;
  let lastErr: unknown;
  while (Date.now() < deadline) {
    try {
      const port = await runtime.inspectMappedHostPort(handle, containerPort);
      if (Number.isFinite(port) && port > 0) {
        return port;
      }
      lastErr = new Error(`mapped port=${port}`);
    } catch (err) {
      lastErr = err;
    }
    await new Promise((r) => setTimeout(r, startupPollInterval));
  }
  throw new Error(
    `postgres mapped host port never resolved: ${String(lastErr)}`,
  );
}

async function waitForPostgresReady(
  runtime: ContainerRuntime,
  handle: ContainerHandle,
  hostPort: number,
  databaseName: string,
  timeoutMs: number,
): Promise<void> {
  const net = await import("node:net");
  const deadline = Date.now() + timeoutMs;
  let lastErr: unknown;
  while (Date.now() < deadline) {
    const socket = new net.Socket();
    try {
      await new Promise<void>((resolve, reject) => {
        const onError = (err: Error) => {
          socket.destroy();
          reject(err);
        };
        socket.once("error", onError);
        socket.connect(hostPort, "127.0.0.1", () => {
          socket.end();
          resolve();
        });
      });
      const logs = await runtime.logs(handle);
      const readyOccurrences = logs.split(readyLogLine).length - 1;
      if (readyOccurrences < 2) {
        throw new Error(
          `postgres stable-ready log occurrences=${String(readyOccurrences)}, want at least 2`,
        );
      }
      await runtime.exec(handle, [
        "pg_isready",
        "--username",
        "postgres",
        "--dbname",
        databaseName,
      ]);
      return;
    } catch (err) {
      lastErr = err;
      await new Promise((r) => setTimeout(r, startupPollInterval));
    }
  }
  throw new Error(
    `postgres never became SQL-ready on loopback port ${hostPort}: ${String(lastErr)}`,
  );
}
