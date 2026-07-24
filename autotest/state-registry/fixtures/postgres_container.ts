// PostgreSQL container lifecycle for the state-registry autotest suite.
// The harness starts an ephemeral postgres:16 container bound EXCLUSIVELY
// to loopback via `--publish 127.0.0.1::5432`, then asks the runtime
// for the OS-assigned host port. Real cleanup failures propagate.
import { randomBytes } from 'node:crypto';
import { DetectRuntime, type ContainerRuntime, type ContainerHandle, ContainerRuntimeError } from './container_runtime';

export interface PostgresContainer {
  handle: ContainerHandle;
  dsn: string;
  password: string;
  hostPort: number;
  containerPort: number;
  runtime: ContainerRuntime;
}

export interface PostgresContainerOptions {
  image?: string;
  databaseName?: string;
  containerPort?: number;
  // Test-only fault-injection: the first N `remove` calls throw
  // a synthetic container runtime error. Use to deterministically
  // exercise the primary+cleanup AggregateError path.
  removeFailureCount?: number;
}

const DefaultImage = 'docker.io/library/postgres:16';
const DefaultContainerPort = 5432;
const DefaultDatabaseName = 'flowai';

// sslmode=disable is acceptable only for loopback test transport to
// a fresh ephemeral postgres:16 container; production registries never
// use a disabled SSL mode.
const TlsHint = 'sslmode=disable';

// startupPollInterval bounds the readiness poll inside the harness.
const startupPollInterval = 100;

// startupTimeoutMs bounds the wait for postgres to accept SQL clients.
const startupTimeoutMs = 20_000;
const readyLogLine = 'database system is ready to accept connections';

export async function StartPostgresContainer(opts: PostgresContainerOptions = {}): Promise<PostgresContainer> {
  const runtime = await DetectRuntime();
  const image = opts.image ?? DefaultImage;
  const containerPort = opts.containerPort ?? DefaultContainerPort;
  const password = randomBytes(16).toString('hex');
  const containerName = `state-registry-pg-${Date.now()}-${randomBytes(4).toString('hex')}`;
  const databaseName = opts.databaseName ?? DefaultDatabaseName;
  const removeFailureCountRef = { count: opts.removeFailureCount ?? 0 };

  let pulled = false;
  try {
    await runtime.pull(image);
    pulled = true;
  } catch (primary) {
    throw primary;
  }
  void pulled;

  const runArgs = [
    'run',
    '--detach',
    '--rm',
    '--name',
    containerName,
    // Loopback-only publication: the host port is OS-assigned and
    // never reachable from outside the test runner.
    '--publish',
    `127.0.0.1::${containerPort}`,
    // The postgres image declares /var/lib/postgresql/data as a volume.
    // Rootless Podman retains that anonymous volume after --rm, so repeated
    // test runs can fill the host. PG data is test-only and belongs in tmpfs.
    '--tmpfs',
    '/var/lib/postgresql/data:rw',
    '--env',
    `POSTGRES_PASSWORD=${password}`,
    '--env',
    `POSTGRES_DB=${databaseName}`,
    image,
  ];
  let handle: ContainerHandle;
  try {
    handle = await runtime.runDetached(runArgs);
  } catch (primary) {
    throw primary;
  }

  let hostPort: number;
  try {
    hostPort = await pollMappedHostPort(runtime, handle, containerPort);
  } catch (primary) {
    const cleanupError = await StopPostgresContainer(handle ? { handle, dsn: '', password, hostPort: 0, containerPort, runtime } : null, removeFailureCountRef);
    if (cleanupError) {
      throw new AggregateError(
        [primary, cleanupError],
        `StartPostgresContainer failed during mapped-port poll: ${String(primary)}`,
      );
    }
    throw primary;
  }

  try {
    await waitForPostgresReady(runtime, handle, hostPort, databaseName, startupTimeoutMs);
  } catch (primary) {
    const cleanupError = await StopPostgresContainer(handle ? { handle, dsn: '', password, hostPort: 0, containerPort, runtime } : null, removeFailureCountRef);
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
  };
}

// StopPostgresContainer surfaces every cleanup error. The fixture
// guarantees the test fails if the runtime refuses to remove a
// container that is still tracked by the harness. Returns the
// container runtime error so callers can aggregate it with the
// primary cause; null if removal succeeded or the container was
// already gone.
export async function StopPostgresContainer(
  container: PostgresContainer | null,
  removeFailureCountRef: { count: number } = { count: 0 },
): Promise<Error | null> {
  if (!container) {
    return null;
  }
  if (removeFailureCountRef.count > 0) {
    removeFailureCountRef.count -= 1;
    return new Error('injected postgres remove failure');
  }
  return safeRemove(container.runtime, container.handle);
}

async function safeRemove(
  runtime: ContainerRuntime,
  handle: ContainerHandle,
): Promise<Error | null> {
  try {
    await runtime.remove(handle);
    return null;
  } catch (err) {
    if (err instanceof ContainerRuntimeError && runtime.isAlreadyRemovedError(err.stderr)) {
      return null;
    }
    return err instanceof Error ? err : new Error(String(err));
  }
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
  throw new Error(`postgres mapped host port never resolved: ${String(lastErr)}`);
}

async function waitForPostgresReady(
  runtime: ContainerRuntime,
  handle: ContainerHandle,
  hostPort: number,
  databaseName: string,
  timeoutMs: number,
): Promise<void> {
  const net = await import('node:net');
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
        socket.once('error', onError);
        socket.connect(hostPort, '127.0.0.1', () => {
          socket.end();
          resolve();
        });
      });
      const logs = await runtime.logs(handle);
      const readyOccurrences = logs.split(readyLogLine).length - 1;
      if (readyOccurrences < 2) {
        throw new Error(`postgres stable-ready log occurrences=${String(readyOccurrences)}, want at least 2`);
      }
      await runtime.exec(handle, ['pg_isready', '--username', 'postgres', '--dbname', databaseName]);
      return;
    } catch (err) {
      lastErr = err;
      await new Promise((r) => setTimeout(r, startupPollInterval));
    }
  }
  throw new Error(`postgres never became SQL-ready on loopback port ${hostPort}: ${String(lastErr)}`);
}
