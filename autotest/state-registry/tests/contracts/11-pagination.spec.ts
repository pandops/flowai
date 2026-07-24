import { expect, test } from '@playwright/test';
import { randomUUID } from 'node:crypto';

import { systemAdministrator, gatewayFor } from '../../fixtures/identities';
import { startRegistryWorker, type RegistryWorker } from '../../fixtures/registry_worker';
import { imageReference } from './_setup';

let worker: RegistryWorker;

async function sql(sqlText: string): Promise<void> {
  await worker.postgres.runtime.exec(worker.postgres.handle, [
    'psql',
    '--username',
    'postgres',
    '--dbname',
    'flowai',
    '--set',
    'ON_ERROR_STOP=1',
    '--command',
    sqlText,
  ]);
}

function tamper(cursor: string): string {
  const parts = cursor.split('.');
  expect(parts).toHaveLength(3);
  const payload = parts[1] ?? '';
  const replacement = payload.startsWith('A') ? 'B' : 'A';
  parts[1] = replacement + payload.slice(1);
  return parts.join('.');
}

test.beforeAll(async () => {
  worker = await startRegistryWorker();
});

test.afterAll(async () => {
  await worker?.teardown();
});

test('section 3b opaque cursors advance and reject tampering, changed scope, endpoint, and filters', async () => {
  const suffix = randomUUID().replaceAll('-', '');
  const admin = systemAdministrator();
  const adminAPI = await admin.api(worker.baseUrl);

  const teamResponse = await adminAPI.post('/admin/teams', {
    data: {
      team_name: `cursor-${suffix}`,
      default_image: imageReference(`cursor-${suffix}`),
    },
  });
  expect(teamResponse.status()).toBe(201);
  const teamID = (await teamResponse.json() as { team_id: string }).team_id;

  const sourceResponse = await adminAPI.post('/admin/source-systems', {
    data: { team_id: teamID, listener_identity: `listener-${suffix}` },
  });
  expect(sourceResponse.status()).toBe(201);
  const sourceSystemID = (await sourceResponse.json() as { source_system_id: string }).source_system_id;

  const taskTypeIDs: string[] = [];
  for (const executionTag of ['alpha', 'beta']) {
    const response = await adminAPI.post('/admin/task-types', {
      data: { team_id: teamID, execution_tag: executionTag },
    });
    expect(response.status()).toBe(201);
    taskTypeIDs.push((await response.json() as { task_type_id: string }).task_type_id);
  }

  const firstTagPage = await adminAPI.get(`/admin/tags?team_id=${teamID}&limit=1`);
  expect(firstTagPage.status()).toBe(200);
  const firstTagBody = await firstTagPage.json() as {
    items: Array<{ execution_tag: string }>;
    page: { next_cursor: string | null };
  };
  expect(firstTagBody.items.map((item) => item.execution_tag)).toEqual(['alpha']);
  expect(firstTagBody.page.next_cursor).toEqual(expect.any(String));

  const secondTagPage = await adminAPI.get(
    `/admin/tags?team_id=${teamID}&limit=1&cursor=${encodeURIComponent(firstTagBody.page.next_cursor!)}`,
  );
  expect(secondTagPage.status()).toBe(200);
  const secondTagBody = await secondTagPage.json() as { items: Array<{ execution_tag: string }> };
  expect(secondTagBody.items.map((item) => item.execution_tag)).toEqual(['beta']);

  const taskRows = [
    { id: `task-${suffix}-a`, source: `source-${suffix}-a`, at: '2026-01-01T00:00:00Z' },
    { id: `task-${suffix}-b`, source: `source-${suffix}-b`, at: '2026-01-02T00:00:00Z' },
  ];
  for (const row of taskRows) {
    await sql(`INSERT INTO tasks (task_id, team_id, source_system_id, source_id, task_type_id, required_tag, payload, ingested_at)
      VALUES ('${row.id}', '${teamID}', '${sourceSystemID}', '${row.source}', '${taskTypeIDs[0]}', 'alpha', '{}'::jsonb, '${row.at}')`);
  }

  const gateway = gatewayFor({ teamId: teamID, operatorId: `operator-${suffix}` });
  const gatewayAPI = await gateway.api(worker.baseUrl);
  const firstTaskPage = await gatewayAPI.get('/v1/tasks?limit=1');
  expect(firstTaskPage.status()).toBe(200);
  const firstTaskBody = await firstTaskPage.json() as {
    items: Array<{ task_id: string }>;
    page: { next_cursor: string | null };
  };
  expect(firstTaskBody.items.map((item) => item.task_id)).toEqual([taskRows[1]!.id]);
  expect(firstTaskBody.page.next_cursor).toEqual(expect.any(String));

  const nextCursor = firstTaskBody.page.next_cursor!;
  const secondTaskPage = await gatewayAPI.get(`/v1/tasks?limit=1&cursor=${encodeURIComponent(nextCursor)}`);
  expect(secondTaskPage.status()).toBe(200);
  const secondTaskBody = await secondTaskPage.json() as { items: Array<{ task_id: string }> };
  expect(secondTaskBody.items.map((item) => item.task_id)).toEqual([taskRows[0]!.id]);

  const tampered = await gatewayAPI.get(`/v1/tasks?limit=1&cursor=${encodeURIComponent(tamper(nextCursor))}`);
  expect(tampered.status()).toBe(400);
  expect((await tampered.json() as { code: string }).code).toBe('invalid_pagination');

  const changedFilter = await gatewayAPI.get(
    `/v1/tasks?limit=1&state=pending&cursor=${encodeURIComponent(nextCursor)}`,
  );
  expect(changedFilter.status()).toBe(400);

  const otherGatewayAPI = await gatewayFor({ teamId: `other-${suffix}`, operatorId: `operator-${suffix}` }).api(worker.baseUrl);
  const changedScope = await otherGatewayAPI.get(`/v1/tasks?limit=1&cursor=${encodeURIComponent(nextCursor)}`);
  expect(changedScope.status()).toBe(400);

  const crossEndpoint = await adminAPI.get(`/admin/tasks?limit=1&cursor=${encodeURIComponent(nextCursor)}`);
  expect(crossEndpoint.status()).toBe(400);

  const malformedLimit = await adminAPI.get('/admin/tags?limit=abc');
  expect(malformedLimit.status()).toBe(400);

  await otherGatewayAPI.dispose();
  await gatewayAPI.dispose();
  await adminAPI.dispose();
});
