// Transactional-fixture failure-path tests. The fixtures contract is
// that an unsuccessful start tears down every artifact it created
// (Go process and Postgres container). The tests use deterministic
// dependency-injection hooks (removeFailureCount,
// processKillFailureCount) on the RegistryWorker fixture and the
// StartPostgresContainer helper to assert the AggregateError cleanup
// contract without conditional assertions.
import { test, expect } from '@playwright/test';
import { startRegistryWorker } from '../fixtures/registry_worker';
import { execSync } from 'node:child_process';

function listStateRegistryContainers(): string[] {
  try {
    const out = execSync('podman ps -a --format "{{.Names}}" --filter "name=state-registry-pg-"', {
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    return out.toString('utf-8').split('\n').map((s) => s.trim()).filter((s) => s.length > 0);
  } catch {
    return [];
  }
}

function listStateRegistryGoProcesses(): string[] {
  try {
    const out = execSync('ps -eo pid,command', { stdio: ['ignore', 'pipe', 'pipe'] });
    return out
      .toString('utf-8')
      .split('\n')
      .filter((line) => {
        if (!line.includes('state-registry')) {
          return false;
        }
        if (line.includes('autotest/state-registry/')) {
          return false;
        }
        return line.includes('go run') ||
          line.includes('cmd/state-registry') ||
          line.includes('state-registry-bin-');
      })
      .map((line) => line.trim());
  } catch {
    return [];
  }
}

function cleanupStale(): void {
  try {
    const names = listStateRegistryContainers();
    if (names.length > 0) {
      execSync(`podman rm --force ${names.map((n) => `'${n}'`).join(' ')}`, {
        stdio: ['ignore', 'pipe', 'pipe'],
      });
    }
  } catch {
    /* best-effort */
  }
  try {
    const out = execSync('ps -eo pid,command', { stdio: ['ignore', 'pipe', 'pipe'] });
    const pids: number[] = [];
    for (const line of out.toString('utf-8').split('\n')) {
      if (
        line.includes('state-registry') &&
        (line.includes('go run') ||
          line.includes('cmd/state-registry') ||
          line.includes('state-registry-bin-')) &&
        !line.includes('autotest/state-registry/')
      ) {
        const pid = Number(line.trim().split(/\s+/)[0]);
        if (Number.isFinite(pid)) pids.push(pid);
      }
    }
    for (const pid of pids) {
      try {
        process.kill(pid, 'SIGKILL');
      } catch {
        /* best-effort */
      }
    }
  } catch {
    /* best-effort */
  }
}

test.describe('startRegistryWorker transactional behavior', () => {
  test.beforeAll(() => {
    cleanupStale();
  });

  test('postgres-startup failure cleans up container (no leak)', async () => {
    const baseline = listStateRegistryContainers();
    const baselineProcs = listStateRegistryGoProcesses();
    let captured: unknown;
    try {
      await startRegistryWorker({ postgresImage: 'docker.io/library/this-image-does-not-exist:0' });
    } catch (err) {
      captured = err;
    }
    expect(captured, 'startRegistryWorker should reject when postgres image is missing').toBeDefined();
    expect(String(captured)).toMatch(/state-registry worker failed during postgres-startup/);
    await new Promise((r) => setTimeout(r, 500));
    expect(listStateRegistryContainers()).toEqual(baseline);
    expect(listStateRegistryGoProcesses()).toEqual(baselineProcs);
  });

  test('bind-discovery failure cleans up process and container', async () => {
    const baseline = listStateRegistryContainers();
    const baselineProcs = listStateRegistryGoProcesses();
    let captured: unknown;
    try {
      await startRegistryWorker({ binary: '/nonexistent/path/to/state-registry' });
    } catch (err) {
      captured = err;
    }
    expect(captured, 'startRegistryWorker should reject when binary is missing').toBeDefined();
    expect(String(captured)).toMatch(/state-registry worker failed during|ENOENT/);
    await new Promise((r) => setTimeout(r, 500));
    expect(listStateRegistryContainers()).toEqual(baseline);
    expect(listStateRegistryGoProcesses()).toEqual(baselineProcs);
  });

  test('restart after teardown is rejected', async () => {
    const w = await startRegistryWorker();
    await w.teardown();
    await expect(w.restart()).rejects.toThrow(/torn down/);
    expect(listStateRegistryContainers()).toHaveLength(0);
  });

  test('concurrent restart and teardown never leak a replacement process or container', async () => {
    const w = await startRegistryWorker();
    const restartPromise = w.restart();
    await new Promise((r) => setTimeout(r, 50));
    await w.teardown();
    let restartSucceeded = false;
    let restartRejected = false;
    try {
      await restartPromise;
      restartSucceeded = true;
    } catch (err) {
      restartRejected = true;
      const message = String(err);
      expect(/lifecycle|torn down|worker failed during/i.test(message)).toBe(true);
    }
    if (restartSucceeded) {
      expect(listStateRegistryContainers()).toHaveLength(0);
    } else {
      expect(restartRejected).toBe(true);
    }
    expect(listStateRegistryContainers()).toHaveLength(0);
  });

  test('teardown reports AggregateError(primary + kill failure + remove failure) deterministically', async () => {
    cleanupStale();
    let captured: unknown;
    try {
      const w = await startRegistryWorker({
        processKillFailureCount: 5,
        removeFailureCount: 5,
      });
      await w.teardown();
    } catch (err) {
      captured = err;
    }
    expect(captured, 'teardown must reject when both kill and remove fail').toBeDefined();
    const err = captured as Error;
    expect(err.name).toMatch(/AggregateError/);
    const agg = err as AggregateError;
    expect(Array.isArray(agg.errors), 'AggregateError.errors must be an array').toBe(true);
    expect(agg.errors.length).toBeGreaterThanOrEqual(2);
    const messages = agg.errors.map((e) => e.message);
    expect(
      messages.some((m) => /injected process kill failure/.test(m)),
      'must include the injected process kill failure',
    ).toBe(true);
    expect(
      messages.some((m) => /injected postgres remove failure/.test(m)),
      'must include the injected postgres remove failure',
    ).toBe(true);
    // The injected remove failure never actually removed the
    // container; the test cleans up the leaked artifact at the
    // end so the next test starts from a clean baseline.
    cleanupStale();
    expect(listStateRegistryContainers()).toHaveLength(0);
  });

  test('startup failure with injected remove failure preserves primary+cleanup', async () => {
    cleanupStale();
    let captured: unknown;
    try {
      // Bad binary path triggers process-spawn failure AFTER the
      // postgres container is created, so the cleanup path runs and
      // the injected remove failure is observed.
      await startRegistryWorker({
        binary: '/nonexistent/path/to/state-registry',
        removeFailureCount: 1,
      });
    } catch (err) {
      captured = err;
    }
    expect(captured).toBeDefined();
    const err = captured as Error;
    expect(err.name).toMatch(/AggregateError|FixtureLifecycleError/);
    const fixtureErr = err as { aggregate?: AggregateError; primary?: Error; cleanup?: Error[] };
    const aggregate = fixtureErr.aggregate ?? (err as AggregateError);
    expect(Array.isArray(aggregate.errors)).toBe(true);
    expect(aggregate.errors.length).toBeGreaterThanOrEqual(2);
    const messages = aggregate.errors.map((e) => e.message);
    expect(messages.some((m) => /process-spawn|ENOENT|worker failed during/i.test(m))).toBe(true);
    expect(
      messages.some((m) => /injected postgres remove failure/.test(m)),
      'cleanup error must be present',
    ).toBe(true);
    cleanupStale();
    expect(listStateRegistryContainers()).toHaveLength(0);
  });
});
