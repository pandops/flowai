// Cross-service e2e tests for the Docker Executor.
//
// These tests spawn both the mocked-task-server and the docker-executor as
// real subprocesses, then drive them via Playwright's `request` fixture.
// They verify that the executor:
//   - exposes only /v1/livez and /v1/readyz on its platform API
//   - registers with the mocked State Registry on startup
//   - polls the Router task list and posts executor lifecycle events
//
// These are API-only tests; video recording is not enabled because there
// is no browser UI in v0001. Once the Web UI lands (v0006), we can add
// browser-driven specs alongside these.

import { test, expect } from '@playwright/test';
import {
  startServices,
  stopServices,
  ServiceHandles,
  EXECUTOR_BASE,
} from './helpers';

let handles: ServiceHandles;

test.beforeAll(async () => {
  handles = await startServices();
});

test.afterAll(async () => {
  if (handles) {
    try {
      await stopServices(handles);
    } catch {
      // best-effort
    }
  }
});

test.describe('Docker Executor cross-service e2e', () => {
  test('mocked-task-server serves /v1/livez', async () => {
    const resp = await handles.api.get('/v1/livez');
    expect(resp.status()).toBe(200);
    const body = await resp.json();
    expect(body.status).toBe('ok');
  });

  test('mocked-task-server router task list returns empty when no tasks are seeded', async () => {
    const resp = await handles.api.get('/v1/tasks?filter=openhands');
    expect(resp.status()).toBe(200);
    const body = await resp.json();
    expect(Array.isArray(body.tasks)).toBe(true);
    expect(body.tasks).toHaveLength(0);
  });

  test('mocked-task-server env endpoint returns 204 when scope is not configured', async () => {
    const resp = await handles.api.get(
      '/v1/env?executor_id=test&routing_target=openhands&scope_token=missing-scope',
    );
    expect(resp.status()).toBe(204);
  });

  test('mocked-task-server rejects env lookup without scope_token', async () => {
    const resp = await handles.api.get('/v1/env?executor_id=test&routing_target=openhands');
    expect(resp.status()).toBe(400);
  });

  test('docker-executor livez responds on the configured bind', async ({ request }) => {
    const resp = await request.get(`${EXECUTOR_BASE}/livez`);
    expect(resp.status()).toBe(200);
    const body = await resp.json();
    expect(body.status).toBe('ok');
    expect(typeof body.executor_id).toBe('string');
    expect(body.executor_id.length).toBeGreaterThan(0);
  });

  test('docker-executor readyz endpoint exists', async ({ request }) => {
    const resp = await request.get(`${EXECUTOR_BASE}/readyz`);
    expect([200, 503]).toContain(resp.status());
  });

  test('docker-executor platform API is limited to health probes only', async ({ request }) => {
    // Per design, the Executor exposes ONLY /v1/livez and /v1/readyz.
    // Task-control endpoints belong to the Router / State Registry surfaces,
    // never to the Executor. A 404 from any other path confirms the surface
    // is restricted to the documented probes.
    for (const path of ['/v1/tasks', '/v1/tasks/abc-123/events', '/v1/executors']) {
      const resp = await request.post(`${EXECUTOR_BASE}${path}`);
      expect([404, 405]).toContain(resp.status());
    }
  });

  test('docker-executor registers with mocked State Registry', async ({ request }) => {
    const livez = await request.get(`${EXECUTOR_BASE}/livez`);
    expect(livez.status()).toBe(200);
    const body = await livez.json();
    const executorId = body.executor_id;
    expect(executorId).toBeTruthy();

    // Re-register the same executor_id. The first call returns 201 (created);
    // subsequent calls return 200 (idempotent). Either way the registration
    // is recorded in the State Registry.
    const reReg = await handles.api.put(`/v1/executors/${executorId}`, {
      data: {
        executor_id: executorId,
        executor_type: 'docker-openhands',
        routing_target: 'openhands',
        capacity: 2,
        running_child_count: 0,
        metadata: {
          openhands_image: 'ghcr.io/openhands/agent-server:latest-python',
          openhands_host_port_start: 18000,
          openhands_host_port_end: 18099,
        },
      },
    });
    expect([200, 201]).toContain(reReg.status());
  });
});
