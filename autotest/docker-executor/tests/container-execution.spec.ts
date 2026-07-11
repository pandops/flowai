// Cross-service e2e tests where the docker-executor drives a real
// Docker-compatible daemon (rootless podman in this environment) to
// create + start a real container.
//
// Prerequisites:
//   - FLOWAI_DOCKER_SOCKET points to a Docker-compatible UNIX socket.
//     Default: /run/user/1000/podman/podman.sock (rootless podman on Linux).
//   - FLOWAI_TEST_IMAGE is present locally. Default: alpine:3.21.
//     Alpine has no /health endpoint, so the executor's health check
//     times out, journals task.failed, and force-kills the container —
//     which is exactly what we want to observe against the real daemon.

import { test, expect, request as playwrightRequest } from '@playwright/test';
import * as net from 'node:net';
import * as fs from 'node:fs';
import {
  startServices,
  stopServices,
  seedTask,
  clearTasks,
  discoverExecutorId,
  ServiceHandles,
} from './helpers';

const DOCKER_SOCKET = process.env.FLOWAI_DOCKER_SOCKET ?? '/run/user/1000/podman/podman.sock';
const TEST_IMAGE = process.env.FLOWAI_TEST_IMAGE ?? 'agent-openhands-image:latest';

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
    const server = net.createServer((tcpConn) => {
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
});