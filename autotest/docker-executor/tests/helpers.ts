// Shared helpers for the cross-service Docker Executor e2e tests.
//
// Each test spawns both the mocked-task-server and the docker-executor as
// real subprocesses, then drives them via Playwright's `request` fixture.
// A tiny in-process OpenAI-compatible LLM server (FakeLLM) is started
// before each service spawn so the OpenHands V1 agent-server can
// resolve chat completions during the real-V1 E2E tests without
// requiring external secrets.

import { request, APIRequestContext } from '@playwright/test';
import { spawn, ChildProcess } from 'node:child_process';
import { existsSync } from 'node:fs';
import * as http from 'node:http';
import { resolve } from 'node:path';

export const MOCKED_BIND = '127.0.0.1:18080';
export const EXECUTOR_BIND = '127.0.0.1:18020';
export const MOCKED_BIND_ALT = '127.0.0.1:18081';
export const EXECUTOR_BIND_ALT = '127.0.0.1:18021';

export const MOCKED_BASE = `http://${MOCKED_BIND}/v1`;
export const EXECUTOR_BASE = `http://${EXECUTOR_BIND}/v1`;

export interface FakeLLM {
  baseUrl: string;
  hostBaseUrl: string; // reachable from inside the container via host.containers.internal
  port: number;
  close(): Promise<void>;
}

// FakeLLM is a tiny local HTTP server that responds to OpenAI-compatible
// chat-completions and a few sibling endpoints the V1 agent-server
// probes. Its behavior is deterministic: it always returns a single
// assistant message and then terminates the conversation. The
// container can reach this server via host.containers.internal (rootless
// podman) or host.docker.internal (docker desktop).
export function startFakeLLM(): Promise<FakeLLM> {
  return new Promise<FakeLLM>((resolve) => {
    const srv = http.createServer((req, res) => {
      if (req.url === '/v1/models' || req.url === '/v1/models?') {
        res.writeHead(200, { 'content-type': 'application/json' });
        res.end(JSON.stringify({ data: [{ id: 'flowai-fake-llm', object: 'model' }] }));
        return;
      }
      if (req.url?.startsWith('/v1/chat/completions')) {
        const chunks: Buffer[] = [];
        req.on('data', (c) => chunks.push(c));
        req.on('end', () => {
          const body = Buffer.concat(chunks).toString('utf-8');
          let prompt = '';
          try {
            const parsed = JSON.parse(body) as { messages?: Array<{ role: string; content: unknown }> };
            const msgs = parsed.messages ?? [];
            for (const m of msgs) {
              if (typeof m.content === 'string') {
                prompt += m.content;
              } else if (Array.isArray(m.content)) {
                for (const part of m.content) {
                  const p = part as { type?: string; text?: string };
                  if (p.type === 'text' && typeof p.text === 'string') {
                    prompt += p.text;
                  }
                }
              }
            }
          } catch {
            // ignore parse errors; respond with a static message.
          }
          res.writeHead(200, { 'content-type': 'application/json' });
          res.end(
            JSON.stringify({
              id: 'chatcmpl-fake-' + Date.now(),
              object: 'chat.completion',
              created: Math.floor(Date.now() / 1000),
              model: 'flowai-fake-llm',
              choices: [
                {
                  index: 0,
                  finish_reason: 'stop',
                  message: {
                    role: 'assistant',
                    content: `FAKE_LLM_OK ${prompt.slice(0, 80)}`,
                  },
                },
              ],
              usage: { prompt_tokens: 10, completion_tokens: 10, total_tokens: 20 },
            }),
          );
        });
        return;
      }
      if (req.url?.startsWith('/v1/embeddings')) {
        res.writeHead(200, { 'content-type': 'application/json' });
        res.end(JSON.stringify({ data: [{ embedding: [0.0, 0.0, 0.0] }] }));
        return;
      }
      res.writeHead(404, { 'content-type': 'application/json' });
      res.end(JSON.stringify({ error: 'not found', path: req.url }));
    });
    srv.listen(0, '127.0.0.1', () => {
      const addr = srv.address();
      if (typeof addr === 'object' && addr !== null) {
        resolve({
          baseUrl: `http://127.0.0.1:${addr.port}/v1`,
          hostBaseUrl: `http://host.containers.internal:${addr.port}/v1`,
          port: addr.port,
          close: () => new Promise<void>((resolveClose) => {
            srv.closeAllConnections();
            srv.close(() => resolveClose());
          }),
        });
      }
    });
  });
}

export interface ServiceHandles {
  mocked: ChildProcess;
  executor: ChildProcess;
  api: APIRequestContext;
  executorProbe: APIRequestContext;
  fakeLlm?: FakeLLM;
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
  /** When true, spawns a local FakeLLM and points the executor at it. */
  useFakeLlm?: boolean;
  /** Workspace directory passed to OpenHands as part of the task spec. */
  workspaceDir?: string;
  /** Whether initial_message.run is true (the V1 server kicks off the
   * agent loop). Default: true. Set to false for tests that just need
   * the conversation to be created (e.g. interrupt E2E). */
  initialRun?: boolean;
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

  let fakeLlm: FakeLLM | undefined;
  if (opts.useFakeLlm) {
    fakeLlm = await startFakeLLM();
  }

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
  const dockerSocket = opts.dockerSocket ?? process.env.FLOWAI_DOCKER_SOCKET;
  if (dockerSocket) executorEnv.DOCKER_SOCKET_PATH = dockerSocket;
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
  if (opts.initialRun !== undefined) {
    executorEnv.OPENHANDS_INITIAL_RUN = opts.initialRun ? 'true' : 'false';
  }
  if (fakeLlm) {
    executorEnv.OPENHANDS_LLM_BASE_URL = fakeLlm.hostBaseUrl;
    executorEnv.OPENHANDS_LLM_MODEL = 'openai/gpt-4o-mini';
    executorEnv.OPENHANDS_LLM_API_KEY = 'flowai-fake-llm';
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

  return { mocked, executor, api, executorProbe, fakeLlm };
}

export async function stopServices(handles: ServiceHandles): Promise<void> {
  await handles.api.dispose();
  await handles.executorProbe.dispose();
  for (const child of [handles.executor, handles.mocked]) {
    if (child.exitCode !== null || child.signalCode !== null) continue;
    // SIGTERM first; wait for the child to exit; escalate to SIGKILL
    // only if it has not exited by the deadline.
    await new Promise<void>((resolve) => {
      let done = false;
      const finish = () => { if (!done) { done = true; resolve(); } };
      const timer = setTimeout(() => {
        try { child.kill('SIGKILL'); } catch { /* ignore */ }
        // Give SIGKILL a short window to take effect before resolving.
        setTimeout(finish, 500);
      }, 5_000)
      child.once('exit', () => {
        clearTimeout(timer);
        finish();
      });
      try { child.kill('SIGTERM'); } catch { finish(); }
    });
  }
  if (handles.fakeLlm) {
    // Always close the local fake-LLM server so subsequent tests do
    // not collide on its port.
    await handles.fakeLlm.close();
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

/** Update the task that the stub returns to the executor (used to push a new action). */
export async function replaceTask(handles: ServiceHandles, taskId: string, patch: Record<string, unknown>): Promise<void> {
  await seedTask(handles, { task_id: taskId, ...patch });
}

/** Return all task events recorded by the mocked task server. */
export async function listTaskEvents(handles: ServiceHandles, taskId: string): Promise<Array<Record<string, unknown>>> {
  const resp = await handles.api.get('/v1/tasks/' + taskId + '/events');
  if (!resp.ok()) {
    throw new Error(`failed to list task events: status=${resp.status()} body=${await resp.text()}`);
  }
  return (await resp.json()) as Array<Record<string, unknown>>;
}

/** Return the current task list the stub is serving. */
export async function listTasks(handles: ServiceHandles): Promise<Array<Record<string, unknown>>> {
  const resp = await handles.api.get('/v1/tasks');
  if (!resp.ok()) {
    throw new Error(`failed to list tasks: status=${resp.status()} body=${await resp.text()}`);
  }
  const body = (await resp.json()) as { tasks?: Array<Record<string, unknown>> };
  return body.tasks ?? [];
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

/**
 * Wait until a specific task event has been recorded for taskId. Polls the
 * mocked State Registry at small intervals. Returns the matching event
 * record, or throws when the deadline elapses.
 */
export async function waitForTaskEvent(
  handles: ServiceHandles,
  taskId: string,
  eventType: string,
  timeoutMs: number,
  predicate?: (ev: Record<string, unknown>) => boolean,
): Promise<Record<string, unknown>> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const events = await listTaskEvents(handles, taskId);
    for (const ev of events) {
      if (ev.type === eventType && (!predicate || predicate(ev))) {
        return ev;
      }
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`task event ${eventType} for task ${taskId} not seen within ${timeoutMs}ms`);
}
