// Cross-service e2e tests where the docker-executor drives a real
// Docker-compatible daemon (rootless podman in this environment) to
// create + start a real container.
//
// Prerequisites:
//   - FLOWAI_DOCKER_SOCKET points to a Docker-compatible UNIX socket.
//     Default: /run/user/1000/podman/podman.sock (rootless podman on Linux).
//   - FLOWAI_TEST_IMAGE is present locally. Default: alpine:3.21 for the
//     health-check-failure path; FLOWAI_V1_IMAGE (default
//     agent-openhands-image:latest) for the end-to-end happy path.
//     V1 agent-server provides /health and supports full task lifecycle,
//     so we can exercise terminal-task events from the executor to the
//     mocked State Registry.
//
// The two real-V1 tests start a tiny OpenAI-compatible LLM stub
// (FakeLLM in helpers) so the V1 agent-server can run real
// conversations end-to-end without external secrets.

import { test, expect, request as playwrightRequest } from '@playwright/test';
import * as net from 'node:net';
import * as fs from 'node:fs';
import {
  startServices,
  stopServices,
  seedTask,
  replaceTask,
  clearTasks,
  discoverExecutorId,
  listTaskEvents,
  ServiceHandles,
  waitForTaskEvent,
} from './helpers';

const DOCKER_SOCKET = process.env.FLOWAI_DOCKER_SOCKET ?? '/run/user/1000/podman/podman.sock';
const TEST_IMAGE = process.env.FLOWAI_TEST_IMAGE ?? 'alpine:3.21';
const V1_IMAGE = process.env.FLOWAI_V1_IMAGE ?? 'agent-openhands-image:latest';

interface DockerContainer {
  Id: string;
  Names: string[];
  Image: string;
  Labels: Record<string, string> | null;
  State: string;
  Status: string;
}

let proxy: { port: number; close: () => void } | null = null;

test.beforeAll(() => {
  if (!fs.existsSync(DOCKER_SOCKET)) {
    throw new Error(
      `Docker socket not found at ${DOCKER_SOCKET}. Set FLOWAI_DOCKER_SOCKET or install podman.`,
    );
  }
});

async function dockerSocketToTCP(socketPath: string): Promise<{ port: number; close: () => void }> {
  return new Promise((resolve, reject) => {
    const server = net.createServer((tcpConn: net.Socket) => {
      const unixConn = net.createConnection(socketPath);
      tcpConn.pipe(unixConn);
      unixConn.pipe(tcpConn);
      tcpConn.on('error', () => unixConn.destroy());
      unixConn.on('error', () => tcpConn.destroy());
    });
    server.on('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      if (typeof addr === 'object' && addr !== null) {
        resolve({ port: addr.port, close: () => server.close() });
      } else {
        reject(new Error('failed to get server address'));
      }
    });
  });
}

test.beforeAll(async () => {
  proxy = await dockerSocketToTCP(DOCKER_SOCKET);
});

test.afterAll(async () => {
  if (proxy) proxy.close();
});

test.afterEach(async () => {
  // Defensive cleanup: the test's try/finally already stops the
  // services it spawned; afterEach is a no-op for that path. The
  // per-test `removeAllOhContainers` and `removeContainer` calls handle
  // the container-name collision case without any global process kill.
});

async function listContainers(docker: Awaited<ReturnType<typeof playwrightRequest.newContext>>, label: string): Promise<DockerContainer[]> {
  const resp = await docker.get(
    `/v1.41/containers/json?all=true&filters=${encodeURIComponent(JSON.stringify({ label: [label] }))}`,
  );
  if (!resp.ok()) return [];
  return (await resp.json()) as DockerContainer[];
}

async function removeContainer(docker: Awaited<ReturnType<typeof playwrightRequest.newContext>>, id: string): Promise<void> {
  await docker.delete(`/v1.41/containers/${id}?force=true&v=true`).catch(() => undefined);
}

async function removeContainersByName(
  docker: Awaited<ReturnType<typeof playwrightRequest.newContext>>,
  name: string,
): Promise<void> {
  // name filter matches by substring in the Docker API.
  const resp = await docker.get(
    `/v1.41/containers/json?all=true&filters=${encodeURIComponent(JSON.stringify({ name: [name] }))}`,
  );
  if (!resp.ok()) return;
  const containers = (await resp.json()) as Array<{ Id: string; Names?: string[] }>;
  for (const c of containers) {
    await removeContainer(docker, c.Id);
  }
}

// removeAllOhContainers removes any container whose name starts with
// "oh-". The "oh-" prefix is reserved for the executor's OpenHands
// containers. Used as a defensive cleanup to wipe stale state from
// interrupted or failed test runs that left the daemon holding the
// container name.
async function removeAllOhContainers(
  docker: Awaited<ReturnType<typeof playwrightRequest.newContext>>,
): Promise<void> {
  // The Docker API supports prefix match via the `name` filter. We
  // pass the full "oh-" prefix to match any flowai-created container.
  const resp = await docker.get(
    `/v1.41/containers/json?all=true&filters=${encodeURIComponent(JSON.stringify({ name: ["oh-"] }))}`,
  );
  if (!resp.ok()) return;
  const containers = (await resp.json()) as Array<{ Id: string }>;
  for (const c of containers) {
    await removeContainer(docker, c.Id);
  }
}

async function v1ImageAvailable(imageName: string): Promise<boolean> {
  const dockerProbe = await playwrightRequest.newContext({
    baseURL: `http://127.0.0.1:${proxy!.port}`,
  });
  try {
    const r = await dockerProbe.get(`/v1.41/images/${imageName}/json`);
    return r.ok();
  } catch {
    return false;
  } finally {
    await dockerProbe.dispose();
  }
}

test.describe('Real container execution via Docker daemon', () => {
  test('executor pulls the image, creates a labeled container, and cleans up on health-check failure', async () => {
    const handles: ServiceHandles = await startServices({
      dockerSocket: DOCKER_SOCKET,
      openHandsImage: TEST_IMAGE,
      openHandsPortStart: 20000,
      openHandsPortEnd: 20010,
      openHandsStartupTimeout: 30,   // V1 agent-server can take a while to start
      openHandsInterruptTimeout: 30,
      openHandsDrainTimeout: 30,
      pollIntervalMs: 500,
      // Use alt ports so this test does not collide with the wire-contract
      // tests in executor.spec.ts which use the default ports.
      mockedBind: '127.0.0.1:18081',
      executorBind: '127.0.0.1:18021',
    });

    const docker = await playwrightRequest.newContext({
      baseURL: `http://127.0.0.1:${proxy!.port}`,
    });

    try {
      const executorId = await discoverExecutorId(handles);

      const leftovers = await listContainers(docker, `flowai.executor_id=${executorId}`);
      for (const c of leftovers) await removeContainer(docker, c.Id);
      await removeAllOhContainers(docker);

      await clearTasks(handles);

      const taskId = '11111111-1111-1111-1111-111111111111';
      await seedTask(handles, {
        task_id: taskId,
        routing_target: 'openhands',
        agent_runtime: 'openhands',
        status: 'queued',
        prompt: 'echo real-container-test',
        metadata: { scenario: 'real-container-e2e' },
      });

      const labelFilter = `flowai.executor_id=${executorId}`;

      const createDeadline = Date.now() + 60_000;
      let observed: DockerContainer | null = null;
      while (Date.now() < createDeadline && !observed) {
        const list = await listContainers(docker, labelFilter);
        if (list.length > 0) observed = list[0];
        if (!observed) await new Promise((r) => setTimeout(r, 250));
      }
      expect(observed, 'executor did not create a container within 60s').not.toBeNull();
      // Docker canonicalizes image refs.
      expect(observed!.Image).toContain(TEST_IMAGE.split(':')[0]);
      const labels = observed!.Labels ?? {};
      expect(labels['flowai.executor_id']).toBe(executorId);
      expect(labels['flowai.runtime']).toBe('openhands');
      expect(labels['flowai.task_id']).toBe(taskId);

      const cleanupDeadline = Date.now() + 20_000;
      while (Date.now() < cleanupDeadline) {
        const remaining = await listContainers(docker, labelFilter);
        if (remaining.length === 0) break;
        await new Promise((r) => setTimeout(r, 250));
      }
      const afterCleanup = await listContainers(docker, labelFilter);
      expect(afterCleanup.length, 'executor did not clean up the container').toBe(0);

      await clearTasks(handles);
      await docker.dispose();
      await stopServices(handles);
    } catch (err) {
      await docker.dispose().catch(() => undefined);
      await stopServices(handles).catch(() => undefined);
      throw err;
    }
  });

  // V1 happy-path E2E: drives a real V1 agent-server with a local
  // OpenAI-compatible LLM stub. The executor must record task.started,
  // task.start_message, and the new openhands.conversation_started
  // event we use as the real-runtime "container is ready" signal.
  test('executor records V1 task events through the real V1 agent-server', async () => {
    test.setTimeout(240_000); // V1 agent-server startup + LLM round-trip can be slow
    if (!(await v1ImageAvailable(V1_IMAGE))) {
      test.skip(true, `image ${V1_IMAGE} not pre-pulled`);
    }

    const handles: ServiceHandles = await startServices({
      dockerSocket: DOCKER_SOCKET,
      openHandsImage: V1_IMAGE,
      openHandsPortStart: 20100,
      openHandsPortEnd: 20120,
      openHandsStartupTimeout: 90,
      openHandsInterruptTimeout: 30,
      openHandsDrainTimeout: 30,
      pollIntervalMs: 500,
      // Use alt ports to avoid colliding with the wire-contract tests.
      mockedBind: '127.0.0.1:18082',
      executorBind: '127.0.0.1:18022',
      // Wire the executor at the in-process OpenAI-compatible LLM
      // stub. The stub listens on the host; the executor container
      // reaches it via host.containers.internal.
      useFakeLlm: true,
    });
    const docker = await playwrightRequest.newContext({ baseURL: `http://127.0.0.1:${proxy!.port}` });
    try {
      const executorId = await discoverExecutorId(handles);
      const leftovers = await listContainers(docker, `flowai.executor_id=${executorId}`);
      for (const c of leftovers) await removeContainer(docker, c.Id);
      await removeAllOhContainers(docker);
      await clearTasks(handles);

      const taskId = '22222222-2222-2222-2222-222222222222';
      await seedTask(handles, {
        task_id: taskId,
        routing_target: 'openhands',
        agent_runtime: 'openhands',
        status: 'queued',
        prompt: 'echo v1-happy-path',
        metadata: { scenario: 'v1-happy-path' },
      });

      // Wait for the executor to create the container, then for the
      // real-runtime "conversation created" signal. Both events must
      // arrive; without conversation_started the V1 agent-server did
      // not accept the prompt and the subsequent interrupt / terminal
      // assertions would race.
      await waitForTaskEvent(handles, taskId, 'task.started', 60_000);
      await waitForTaskEvent(handles, taskId, 'openhands.conversation_started', 90_000,
        (ev) => {
          const p = ev.payload as { openhands_conversation_id?: unknown } | undefined;
          return typeof p?.openhands_conversation_id === 'string';
        });

      await waitForTaskEvent(handles, taskId, 'openhands.event', 30_000);
      const events = await listTaskEvents(handles, taskId);
      const types = events.map((e) => e.type);
      expect(types).toContain('task.started');
      expect(types).toContain('task.start_message');
      expect(types).toContain('openhands.conversation_started');
      expect(types.filter((t) => t === 'openhands.event').length).toBeGreaterThan(0);
    } finally {
      await docker.dispose().catch(() => undefined);
      await stopServices(handles).catch(() => undefined);
    }
  });

  // V1 interrupt E2E: drives a real V1 agent-server, then submits an
  // interrupt_task action via the stub and verifies the executor
  // forwards it to OpenHands and emits task.cancel_requested plus a
  // terminal task.failed chain.
  test('executor forwards Router pending_actions interrupt_task to OpenHands and records the cancel chain', async () => {
    test.setTimeout(240_000);
    if (!(await v1ImageAvailable(V1_IMAGE))) {
      test.skip(true, `image ${V1_IMAGE} not pre-pulled`);
    }

    const handles: ServiceHandles = await startServices({
      dockerSocket: DOCKER_SOCKET,
      openHandsImage: V1_IMAGE,
      openHandsPortStart: 20200,
      openHandsPortEnd: 20220,
      openHandsStartupTimeout: 90,
      openHandsInterruptTimeout: 5,
      openHandsDrainTimeout: 10,
      pollIntervalMs: 500,
      mockedBind: '127.0.0.1:18083',
      executorBind: '127.0.0.1:18023',
      // run=false skips the agent loop on the V1 server side so the
      // interrupt path doesn't depend on a working LLM. The
      // conversation is still created and the WS stream is live, so
      // pending_actions drive task.cancel_requested +
      // task.failed(phase=interrupt_timeout). The interrupt test does
      // NOT exercise the LLM stub.
      initialRun: false,
      useFakeLlm: true,
    });
    const docker = await playwrightRequest.newContext({ baseURL: `http://127.0.0.1:${proxy!.port}` });
    try {
      const executorId = await discoverExecutorId(handles);
      const leftovers = await listContainers(docker, `flowai.executor_id=${executorId}`);
      for (const c of leftovers) await removeContainer(docker, c.Id);
      await removeAllOhContainers(docker);
      await clearTasks(handles);

      const taskId = '33333333-3333-3333-3333-333333333333';
      await seedTask(handles, {
        task_id: taskId,
        routing_target: 'openhands',
        agent_runtime: 'openhands',
        status: 'queued',
        prompt: 'echo v1-interrupt',
        metadata: { scenario: 'v1-interrupt' },
      });

      // Wait for the executor to create the conversation on the V1
      // agent-server. conversation_started is the deterministic
      // real-runtime signal that pending_actions are safe to send.
      await waitForTaskEvent(handles, taskId, 'openhands.conversation_started', 90_000,
        (ev) => {
          const p = ev.payload as { openhands_conversation_id?: unknown } | undefined;
          return typeof p?.openhands_conversation_id === 'string';
        });

      // Submit the interrupt_task pending_action. The executor's poll
      // loop will pick it up on the next tick and run the unified
      // interrupt path.
      await replaceTask(handles, taskId, {
        routing_target: 'openhands',
        agent_runtime: 'openhands',
        status: 'queued',
        prompt: 'echo v1-interrupt',
        pending_actions: [
          { type: 'interrupt_task', action_id: 'a1', task_id: taskId, reason: 'e2e-interrupt' },
        ],
        metadata: { scenario: 'v1-interrupt' },
      });

      // task.cancel_requested arrives as soon as the interrupt path
      // fires. task.failed(phase=interrupt_timeout) follows because
      // OpenHandsInterruptTimeout=5s expires before the fake LLM
      // (which is not used here) returns anything.
      await waitForTaskEvent(handles, taskId, 'task.cancel_requested', 30_000);

      // Exactly one terminal event must follow.
      const eventsDeadline = Date.now() + 30_000;
      let terminal = 0;
      let cancelReq = 0;
      while (Date.now() < eventsDeadline) {
        const events = await listTaskEvents(handles, taskId);
        cancelReq = events.filter((e) => e.type === 'task.cancel_requested').length;
        terminal = events.filter(
          (e) => e.type === 'task.failed' || e.type === 'task.interrupted' || e.type === 'task.finished',
        ).length;
        if (terminal >= 1) break;
        await new Promise((r) => setTimeout(r, 200));
      }
      expect(cancelReq, 'task.cancel_requested event missing').toBeGreaterThanOrEqual(1);
      expect(terminal, `expected exactly 1 terminal event, got ${terminal}`).toBe(1);
    } finally {
      await docker.dispose().catch(() => undefined);
      await stopServices(handles).catch(() => undefined);
    }
  });
});
