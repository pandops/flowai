// Infrastructure smoke for the state-registry autotest harness. The test
// verifies in order:
//   1. liveness probe on the real State Registry process succeeds
//   2. readiness probe reflects PostgreSQL reachability and configured
//      32-byte AES key presence
//   3. decrypt-operation observation snapshot starts at zero
//   4. the implemented protected POST /admin/teams contract returns 201
import { test, expect } from '@playwright/test';
import { startRegistryWorker, type RegistryWorker } from '../fixtures/registry_worker';
import { systemAdministrator } from '../fixtures/identities';
import { snapshotDecryptOps } from '../fixtures/decrypt_observer';
import { imageReference } from './contracts/_setup';

let worker: RegistryWorker;

test.beforeAll(async () => {
  worker = await startRegistryWorker();
});

test.afterAll(async () => {
  if (worker) {
    await worker.teardown();
  }
});

test.describe('state-registry harness smoke', () => {
  test('infrastructure healthy and protected POST /admin/teams creates a team', async () => {
    const livez = await fetch(`${worker.baseUrl}/v1/livez`);
    expect(livez.status, 'livez must report 200 when the process is alive').toBe(200);
    const livezBody = (await livez.json()) as { service?: string; status?: string };
    expect(livezBody.status).toBe('ok');
    expect(livezBody.service).toBe('state-registry');

    const readyz = await fetch(`${worker.baseUrl}/v1/readyz`);
    expect(readyz.status, 'readyz must report 200 once postgres + AES key are present').toBe(200);
    const readyzBody = (await readyz.json()) as {
      status?: string;
      dependencies?: Record<string, boolean>;
    };
    expect(readyzBody.status).toBe('ready');
    expect(readyzBody.dependencies?.postgres, 'postgres dependency must be reachable').toBe(true);
    expect(readyzBody.dependencies?.aes_key, 'AES key dependency must be present').toBe(true);

    const before = await snapshotDecryptOps(worker.baseUrl);
    expect(before.raw, 'decrypt-operation counter must start at zero').toBe(0);

    const admin = systemAdministrator();
    const resp = await fetch(`${worker.baseUrl}/admin/teams`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        ...admin.attach(),
      },
      body: JSON.stringify({
        team_name: 'team-smoke',
        default_image: imageReference('registry.example/team-smoke'),
      }),
    });
    expect(
      resp.status,
      'protected POST /admin/teams must return 201 per the OpenAPI contract',
    ).toBe(201);
  });

  test('restart preserves PostgreSQL container handle while replacing the Go process', async () => {
    const beforeContainerId = worker.postgres.handle.id;
    const beforeBaseUrl = worker.baseUrl;
    const beforeBindPort = worker.bindPort;
    const beforeDecrypt = await snapshotDecryptOps(worker.baseUrl);
    const result = await worker.restart();
    expect(result.restartCount).toBeGreaterThanOrEqual(1);
    expect(result.postgresContainerId, 'same PostgreSQL container handle must survive a Go-process restart').toBe(beforeContainerId);
    expect(result.previousProcessSignal, 'previous Go process must have exited via signal-driven termination').toBeTruthy();
    expect(result.sanitizedLogs).not.toContain('STATE_REGISTRY_AES_KEY_HEX=');
    expect(result.sanitizedLogs).not.toContain(worker.postgres.password);
    expect(beforeDecrypt.raw).toBe(0);
    const afterDecrypt = await snapshotDecryptOps(worker.baseUrl);
    expect(afterDecrypt.raw, 'decrypt-operation counter must remain zero after restart').toBe(0);
    const readyz = await fetch(`${worker.baseUrl}/v1/readyz`);
    expect(readyz.status, 'readiness must return to 200 after restart').toBe(200);
    const staleLivez = await fetch(`${beforeBaseUrl}/v1/livez`).catch(() => null);
    if (staleLivez !== null) {
      expect(staleLivez.status, 'pre-restart baseUrl must not serve the new process').not.toBe(200);
    }
    expect(worker.baseUrl, 'baseUrl must reflect the current process after restart').not.toBe(beforeBaseUrl);
    expect(worker.bindPort).toBeGreaterThan(0);
    void beforeBindPort;
  });
});
