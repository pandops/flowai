// Shared helpers for the cross-service Docker Executor e2e tests.
//
// Each test spawns both the mocked-task-server and the docker-executor as
// real subprocesses, then drives them via Playwright's `request` fixture.

import { request, APIRequestContext } from '@playwright/test';
import { spawn, ChildProcess } from 'node:child_process';
import { existsSync } from 'node:fs';
import { resolve } from 'node:path';

export const MOCKED_BIND = '127.0.0.1:18080';
export const EXECUTOR_BIND = '127.0.0.1:18020';
export const MOCKED_BIND_ALT = '127.0.0.1:18081';
export const EXECUTOR_BIND_ALT = '127.0.0.1:18021';

export const MOCKED_BASE = `http://${MOCKED_BIND}/v1`;
export const EXECUTOR_BASE = `http://${EXECUTOR_BIND}/v1`;

export interface ServiceHandles {
  mocked: ChildProcess;
  executor: ChildProcess;
  api: APIRequestContext;
  executorProbe: APIRequestContext;
}

export interface StartOptions {
  /** Path to the mocked-task-server binary. Auto-detected if omitted. */
  mockedBinary?: string;
  /** Path to the docker-executor binary. Auto-detected if omitted. */
  executorBinary?: string;
  /** Routing target the executor advertises. */
  routingTarget?: string;
  /** Max concurrent containers. */
  maxContainers?: number;
  /** Docker socket path (e.g. unix://... or tcp://...). */
  dockerSocket?: string;
  /** Image to use for OpenHands containers. */
  openHandsImage?: string;
  /** Host port range for OpenHands containers. */
  openHandsPortStart?: number;
  openHandsPortEnd?: number;
  /** OpenHands timing. */
  openHandsStartupTimeout?: number;
  openHandsInterruptTimeout?: number;
  openHandsDrainTimeout?: number;
  /** Executor poll interval, milliseconds. */
  pollIntervalMs?: number;
  /** Override the bind addresses (defaults to MOCKED_BIND / EXECUTOR_BIND). */
  mockedBind?: string;
  executorBind?: string;
  /** Workspace directory passed to OpenHands as part of the task spec. */
  workspaceDir?: string;
}

export function findBinaries(): { mocked: string; executor: string; config: string } {
  const altMocked = '/tmp/mocked-task-server';
  const altExecutor = '/tmp/docker-executor';
  const repo = resolve(__dirname, '..', '..', '..');
  const config = resolve(repo, 'executor-docker', 'configs', 'docker-executor.yaml');
  if (existsSync(altMocked) && existsSync(altExecutor)) {
    return { mocked: altMocked, executor: altExecutor, config };
  }
  return {
    mocked: process.env.MOCKED_TASK_SERVER_BIN ?? resolve(repo, 'mocked-task-server', 'cmd', 'mocked-task-server', 'main.go'),
    executor: process.env.DOCKER_EXECUTOR_BIN ?? resolve(repo, 'executor-docker', 'cmd', 'docker-executor', 'main.go'),
    config,
  };
}

async function waitForHttp(url: string, timeoutMs: number): Promise<void> {
  const api = await request.newContext({ baseURL: url });
  const deadline = Date.now() + timeoutMs;
  let lastErr: unknown;
  let lastStatus: number | undefined;
  while (Date.now() < deadline) {
    try {
      const resp = await api.get('/v1/livez');
      lastStatus = resp.status();
      if (resp.ok()) {
        await api.dispose();
        return;
      }
    } catch (err) {
      lastErr = err;
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  await api.dispose();
  throw new Error(`timeout waiting for ${url}/v1/livez (last status=${lastStatus}, last err: ${String(lastErr)})`);
}

export async function startServices(opts: StartOptions = {}): Promise<ServiceHandles> {
  const { mocked: mockedBin, executor: executorBin, config: configPath } = findBinaries();
  const mockedBind = opts.mockedBind ?? MOCKED_BIND;
  const executorBind = opts.executorBind ?? EXECUTOR_BIND;
  const mockedBase = `http://${mockedBind}/v1`;

  const mocked = spawn(mockedBin, ['-bind', mockedBind], {
    stdio: ['ignore', 'pipe', 'pipe'],
    env: {
      ...process.env,
      // Enable the test-only seed endpoints on the mocked task server.
      MOCKED_SERVER_TEST_MODE: 'true',
    },
  });

  const executorEnv: NodeJS.ProcessEnv = {
    ...process.env,
    MOCKED_SERVER_URL: mockedBase,
    ROUTING_TARGET: opts.routingTarget ?? 'openhands',
    EXECUTOR_MAX_CONTAINERS: String(opts.maxContainers ?? 2),
  };
  if (opts.dockerSocket) executorEnv.DOCKER_SOCKET_PATH = opts.dockerSocket;
  if (opts.openHandsImage) executorEnv.OPENHANDS_IMAGE = opts.openHandsImage;
  if (opts.openHandsPortStart) executorEnv.OPENHANDS_HOST_PORT_START = String(opts.openHandsPortStart);
  if (opts.openHandsPortEnd) executorEnv.OPENHANDS_HOST_PORT_END = String(opts.openHandsPortEnd);
  if (opts.openHandsStartupTimeout) {
    executorEnv.OPENHANDS_STARTUP_TIMEOUT_SECONDS = String(opts.openHandsStartupTimeout);
  }
  if (opts.openHandsInterruptTimeout) {
    executorEnv.OPENHANDS_INTERRUPT_TIMEOUT_SECONDS = String(opts.openHandsInterruptTimeout);
  }
  if (opts.openHandsDrainTimeout) {
    executorEnv.OPENHANDS_DRAIN_TIMEOUT_SECONDS = String(opts.openHandsDrainTimeout);
  }
  if (opts.pollIntervalMs) {
    executorEnv.EXECUTOR_POLL_INTERVAL = `${opts.pollIntervalMs}ms`;
  }

  const executor = spawn(executorBin, ['-config', configPath, '-bind', executorBind], {
    stdio: ['ignore', 'pipe', 'pipe'],
    env: executorEnv,
  });

  for (const child of [mocked, executor]) {
    child.stdout?.on('data', (b: Buffer) => process.stdout.write(`[${child === mocked ? 'mocked' : 'exec'}] ${b}`));
    child.stderr?.on('data', (b: Buffer) => process.stderr.write(`[${child === mocked ? 'mocked' : 'exec'}] ${b}`));
  }

  await waitForHttp(`http://${mockedBind}`, 10_000);
  await waitForHttp(`http://${executorBind}`, 10_000);

  const api = await request.newContext({ baseURL: `http://${mockedBind}` });
  const executorProbe = await request.newContext({ baseURL: `http://${executorBind}` });

  return { mocked, executor, api, executorProbe };
}

export async function stopServices(handles: ServiceHandles): Promise<void> {
  await handles.api.dispose();
  await handles.executorProbe.dispose();
  for (const child of [handles.executor, handles.mocked]) {
    if (!child.killed) {
      child.kill('SIGTERM');
      await new Promise<void>((resolve) => {
        const t = setTimeout(() => {
          if (!child.killed) child.kill('SIGKILL');
          resolve();
        }, 5_000);
        child.once('exit', () => {
          clearTimeout(t);
          resolve();
        });
      });
    }
  }
}

/** Seed a task into the mocked task server via the test-only POST endpoint. */
export async function seedTask(handles: ServiceHandles, task: Record<string, unknown>): Promise<void> {
  const resp = await handles.api.post('/v1/_test/tasks', { data: task });
  if (resp.status() !== 201) {
    throw new Error(`failed to seed task: status=${resp.status()} body=${await resp.text()}`);
  }
}

/** Clear all seeded tasks. */
export async function clearTasks(handles: ServiceHandles): Promise<void> {
  await handles.api.delete('/v1/_test/tasks');
}

/** Discover the executor_id from its livez response. */
export async function discoverExecutorId(handles: ServiceHandles): Promise<string> {
  for (let i = 0; i < 50; i++) {
    try {
      const resp = await handles.executorProbe.get('/v1/livez');
      if (resp.ok()) {
        const body = await resp.json();
        if (body.executor_id) return body.executor_id;
      }
    } catch {
      // service not ready yet
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error('executor never became ready');
}