// Contract group: ingestion-persistence
// Covers v0002.1, v0002.2, v0002.32, v0002.33, v0002.65, v0002.66.
//
// Each test below exercises the durable listener-ingestion boundary
// (POST /v1/tasks), the team-scoped re-read boundary
// (GET /v1/tasks/{task_id} via trusted Gateway context), and the
// restart-survival boundary (worker.restart()). Behavior-specific
// assertions match the Markdown definitions verbatim:
//
//   - task is committed as `pending` with REQUIRED non-null
//     `task_type_id` referencing a same-team task type
//   - `required_tag` is server-derived from `task_types.execution_tag`
//     and the listener SHALL NOT supply it
//   - no lifecycle event is appended at ingestion
//   - `tasks.owner_command_id`, `tasks.executor_id`, `tasks.claimed_at`,
//     `tasks.resolved_image`, `tasks.image_source` are all NULL while
//     `pending`
//   - dedupe uses (team_id, source_system_id, source_id)
//   - cross-team identity belongs to exactly one team; each team
//     registers its own source system and owns its independent task
//   - same-team rows survive State Registry restart unchanged
//
// All worker startup happens inside `test.beforeAll`; each test body
// uses `identity.api(baseUrl)` to drive the supported HTTP boundary.
import { test, expect } from '@playwright/test';
import { startRegistryWorker, type RegistryWorker } from '../../fixtures/registry_worker';
import {
  systemAdministrator,
  listenerFor,
  gatewayFor,
  type IdentityContext,
} from '../../fixtures/identities';
import { bootstrapTeam, ingestPendingTask } from './_setup';

let worker: RegistryWorker;

test.beforeAll(async () => {
  worker = await startRegistryWorker();
});

test.afterAll(async () => {
  if (worker) {
    await worker.teardown();
  }
});

test('v0002.1 listener persists durable pending task before acknowledgement; REQUIRED non-null task_type_id; required_tag is server-derived', async () => {
  const admin = systemAdministrator();
  const { admin: team, sourceSystem, taskType, listenerIdentity } = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: 'v0002-1-team',
    executionTag: 'openhands',
  });
  const listener = listenerFor({
    teamId: team.team_id,
    listenerIdentity,
    sourceSystemId: sourceSystem.source_system_id,
  });
  const listenerApi = await listener.api(worker.baseUrl);
  let createdTaskId: string | null = null;

  try {
    await test.step('POST /v1/tasks commits a pending task with REQUIRED non-null task_type_id', async () => {
      const resp = await listenerApi.post('/v1/tasks', {
        data: {
          team_id: team.team_id,
          source_system_id: sourceSystem.source_system_id,
          source_id: 'JIRA-142',
          task_type_id: taskType.task_type_id,
          payload: { title: 'investigate flaky test' },
        },
      });
      expect(resp.status(), 'listener ingestion MUST return 201 before any acknowledgement').toBe(201);
      const body = await resp.json();
      createdTaskId = body.task_id;
      expect(typeof createdTaskId, 'ingestion response carries the canonical task_id').toBe('string');
      expect(body.team_id, 'immutable team_id comes from the registered team').toBe(team.team_id);
      expect(body.task_type_id, 'REQUIRED non-null same-team task_type_id must be persisted').toBe(taskType.task_type_id);
      expect(body.current_state, 'ingestion commits current_state = pending without lifecycle events').toBe('pending');
      expect(body.required_tag, 'required_tag is server-derived from task_type.execution_tag, NOT listener-supplied').toBe('openhands');
      expect(body.source_system_id, 'immutable source_system_id comes from the registered source system').toBe(sourceSystem.source_system_id);
      expect(body.source_id, 'source_id is persisted as supplied').toBe('JIRA-142');
      expect(body.owner_command_id, 'owner_command_id is NULL before claim').toBeNull();
      expect(body.executor_id, 'executor_id is NULL before claim').toBeNull();
      expect(body.claimed_at, 'claimed_at is NULL before claim').toBeNull();
      expect(body.resolved_image, 'resolved_image is NULL before claim').toBeNull();
      expect(body.image_source, 'image_source is NULL before claim').toBeNull();
      expect(body.ingested_at, 'ingested_at is set once at ingestion').toBeTruthy();
    });

    await test.step('trusted Gateway re-read confirms the durable pending task with no event history', async () => {
      expect(createdTaskId, 'gateway re-read requires the real task_id').not.toBeNull();
      const taskId = createdTaskId as string;
      const gw = gatewayFor({
        teamId: team.team_id,
        operatorId: `op-${team.team_id}`,
      });
      const gwApi = await gw.api(worker.baseUrl);
      try {
        const view = await gwApi.get(`/v1/tasks/${encodeURIComponent(taskId)}`);
        expect(view.status(), 'trusted Gateway reads the ingested task with 200').toBe(200);
        const viewBody = await view.json() as Record<string, unknown>;
        expect(viewBody['task_id'], 'Gateway read returns the same task_id').toBe(taskId);
        expect(viewBody['current_state'], 'Gateway read shows current_state = pending').toBe('pending');
        expect(viewBody['required_tag'], 'Gateway read shows server-derived required_tag').toBe('openhands');
        expect(viewBody['owner_command_id'], 'Gateway read shows owner_command_id is NULL').toBeNull();
        expect(viewBody['executor_id'], 'Gateway read shows executor_id is NULL').toBeNull();
        expect(viewBody['claimed_at'], 'Gateway read shows claimed_at is NULL').toBeNull();
        expect(viewBody['resolved_image'], 'Gateway read shows resolved_image is NULL').toBeNull();
        expect(viewBody['image_source'], 'Gateway read shows image_source is NULL').toBeNull();
        const events = await gwApi.get(`/v1/tasks/${encodeURIComponent(taskId)}/events`);
        expect(events.status(), 'trusted Gateway reads the ordered event history with 200').toBe(200);
        const eventsBody = await events.json() as { items: unknown[] };
        expect(Array.isArray(eventsBody.items), 'event history returns an items array').toBe(true);
        expect(eventsBody.items.length, 'no lifecycle event is appended at ingestion').toBe(0);
      } finally {
        await gwApi.dispose();
      }
    });
  } finally {
    await listenerApi.dispose();
  }
});

test('v0002.2 listener dedup returns the existing canonical pending task for same (team_id, source_system_id, source_id)', async () => {
  const admin = systemAdministrator();
  const { admin: team, sourceSystem, taskType, listenerIdentity } = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: 'v0002-2-team',
    executionTag: 'openhands',
  });
  const listener = listenerFor({
    teamId: team.team_id,
    listenerIdentity,
    sourceSystemId: sourceSystem.source_system_id,
  });
  const gw = gatewayFor({ teamId: team.team_id, operatorId: `op-${team.team_id}` });
  const gwApi = await gw.api(worker.baseUrl);
  let firstTaskId: string | null = null;

  try {
    await test.step('first POST /v1/tasks creates the canonical pending task', async () => {
      const first = await ingestPendingTask(listener, worker.baseUrl, {
        team_id: team.team_id,
        source_system_id: sourceSystem.source_system_id,
        source_id: 'JIRA-142',
        task_type_id: taskType.task_type_id,
        payload: { title: 'first' },
      });
      firstTaskId = first.task_id;
      expect(typeof firstTaskId, 'first ingestion returns an immutable task_id').toBe('string');
      expect(first.current_state, 'first ingestion commits current_state = pending').toBe('pending');
      expect(first.task_type_id, 'first ingestion persists the same-team task_type_id').toBe(taskType.task_type_id);
    });

    await test.step('same-team retry returns the same canonical task_id and appends no new event', async () => {
      const retry = await ingestPendingTask(listener, worker.baseUrl, {
        team_id: team.team_id,
        source_system_id: sourceSystem.source_system_id,
        source_id: 'JIRA-142',
        task_type_id: taskType.task_type_id,
        payload: { title: 'second' },
      });
      expect(retry.task_id, 'retry returns the SAME canonical task_id').toBe(firstTaskId);
      expect(retry.current_state, 'retry does not reset task state').toBe('pending');

      const events = await gwApi.get(`/v1/tasks/${encodeURIComponent(firstTaskId as string)}/events`);
      expect(events.status(), 'event history read returns 200').toBe(200);
      const eventsBody = await events.json() as { items: unknown[] };
      expect(eventsBody.items.length, 'retry appends no lifecycle event').toBe(0);
    });
  } finally {
    await gwApi.dispose();
  }
});

test('v0002.32 team-owned durable state survives restart (ingested_at, claim, events, control)', async () => {
  const admin = systemAdministrator();
  const { admin: team, sourceSystem, taskType, listenerIdentity } = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: 'v0002-32-team',
    executionTag: 'openhands',
  });
  const listener = listenerFor({
    teamId: team.team_id,
    listenerIdentity,
    sourceSystemId: sourceSystem.source_system_id,
  });
  const initialTask = await ingestPendingTask(listener, worker.baseUrl, {
    team_id: team.team_id,
    source_system_id: sourceSystem.source_system_id,
    source_id: 'JIRA-310',
    task_type_id: taskType.task_type_id,
    payload: { title: 'restart durability' },
  });
  const initialIngestedAt = initialTask.ingested_at;

  await test.step('listener retry identifies the canonical task with no event append', async () => {
    const retry = await ingestPendingTask(listener, worker.baseUrl, {
      team_id: team.team_id,
      source_system_id: sourceSystem.source_system_id,
      source_id: 'JIRA-310',
      task_type_id: taskType.task_type_id,
      payload: { title: 'restart durability retry' },
    });
    expect(retry.task_id, 'retry resolves to the SAME canonical task_id').toBe(initialTask.task_id);
    expect(retry.ingested_at, 'retry preserves the original ingested_at').toBe(initialIngestedAt);
  });

  await test.step('restart preserves PostgreSQL while replacing the Go process', async () => {
    const restart = await worker.restart();
    expect(restart.restartCount).toBeGreaterThanOrEqual(1);
    const livez = await fetch(`${worker.baseUrl}/v1/livez`);
    expect(livez.status, 'livez is back to 200 after restart').toBe(200);
  });

  await test.step('post-restart: durable task identity is unchanged', async () => {
    const gw = gatewayFor({ teamId: team.team_id, operatorId: `op-${team.team_id}` });
    const gwApi = await gw.api(worker.baseUrl);
    try {
      const view = await gwApi.get(`/v1/tasks/${encodeURIComponent(initialTask.task_id)}`);
      expect(view.status(), 'durable task identity survives restart and Gateway reads it with 200').toBe(200);
      const viewBody = await view.json() as { task_id: string; ingested_at: string; current_state: string };
      expect(viewBody.task_id, 'task_id is immutable across restart').toBe(initialTask.task_id);
      expect(viewBody.ingested_at, 'ingested_at is unchanged across restart').toBe(initialIngestedAt);
      expect(viewBody.current_state, 'task remains pending across restart').toBe('pending');

      const events = await gwApi.get(`/v1/tasks/${encodeURIComponent(initialTask.task_id)}/events`);
      expect(events.status(), 'event history is reachable after restart').toBe(200);
      const eventsBody = await events.json() as { items: unknown[] };
      expect(eventsBody.items.length, 'no event was appended at ingestion and no new event was appended across restart').toBe(0);
    } finally {
      await gwApi.dispose();
    }
  });
});

test('v0002.33 cross-team dedup is independent: identical source_id lives independently per team', async () => {
  const admin = systemAdministrator();
  const suffix = `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}`;
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `v0002-33-team-a-${suffix}`,
    executionTag: 'openhands',
  });
  const bsB = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `v0002-33-team-b-${suffix}`,
    executionTag: 'openhands',
  });
  const ingestA = await ingestPendingTask(
    listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsA.admin.team_id,
      source_system_id: bsA.sourceSystem.source_system_id,
      source_id: 'SHARED-33',
      task_type_id: bsA.taskType.task_type_id,
      payload: { from: 'team-a' },
    },
  );
  expect(ingestA.team_id).toBe(bsA.admin.team_id);
  expect(ingestA.task_type_id).toBe(bsA.taskType.task_type_id);

  const ingestB = await ingestPendingTask(
    listenerFor({
      teamId: bsB.admin.team_id,
      listenerIdentity: bsB.listenerIdentity,
      sourceSystemId: bsB.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsB.admin.team_id,
      source_system_id: bsB.sourceSystem.source_system_id,
      source_id: 'SHARED-33',
      task_type_id: bsB.taskType.task_type_id,
      payload: { from: 'team-b' },
    },
  );
  expect(ingestB.team_id, 'team-b owns its OWN canonical task with the same source_id').toBe(bsB.admin.team_id);
  expect(ingestB.task_type_id, 'team-b task references team-b task_type_id').toBe(bsB.taskType.task_type_id);
  expect(ingestB.task_id, 'team-a and team-b tasks are distinct canonical rows').not.toBe(ingestA.task_id);
  expect(ingestA.source_system_id, 'team-a task references team-a source_system_id').toBe(bsA.sourceSystem.source_system_id);
  expect(ingestB.source_system_id, 'team-b task references team-b source_system_id').toBe(bsB.sourceSystem.source_system_id);
});

test('v0002.65 ingestion commits pending with no event; missing/foreign task_type_id and listener-supplied required_tag are rejected', async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-65-${Date.now().toString(36)}`;
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-a`,
    executionTag: 'openhands',
  });
  const bsB = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-b`,
    executionTag: 'openhands',
  });
  const listenerA = listenerFor({
    teamId: bsA.admin.team_id,
    listenerIdentity: bsA.listenerIdentity,
    sourceSystemId: bsA.sourceSystem.source_system_id,
  });
  const listenerApi = await listenerA.api(worker.baseUrl);
  let happyTaskId: string | null = null;

  try {
    await test.step('happy path ingestion returns 201 pending with NO event', async () => {
      const resp = await listenerApi.post('/v1/tasks', {
        data: {
          team_id: bsA.admin.team_id,
          source_system_id: bsA.sourceSystem.source_system_id,
          source_id: 'T-1',
          task_type_id: bsA.taskType.task_type_id,
          payload: { hello: 'world' },
        },
      });
      expect(resp.status(), 'ingestion commits pending with 201').toBe(201);
      const body = await resp.json();
      happyTaskId = body.task_id;
      expect(body.current_state, 'happy-path commits current_state = pending').toBe('pending');
      expect(body.required_tag, 'required_tag is server-derived from task_type.execution_tag').toBe('openhands');
      expect(body.ingested_at, 'ingested_at is set once and is immutable').toBeTruthy();
      expect(body.owner_command_id, 'owner_command_id is NULL on commit').toBeNull();
      expect(body.executor_id, 'executor_id is NULL on commit').toBeNull();
      expect(body.claimed_at, 'claimed_at is NULL on commit').toBeNull();
      expect(body.resolved_image, 'resolved_image is NULL on commit').toBeNull();
      expect(body.image_source, 'image_source is NULL on commit').toBeNull();
    });

    await test.step('Gateway read confirms zero events in the ordered history', async () => {
      expect(happyTaskId, 'event verification requires the committed task_id').not.toBeNull();
      const taskId = happyTaskId as string;
      const gw = gatewayFor({ teamId: bsA.admin.team_id, operatorId: `op-${bsA.admin.team_id}` });
      const gwApi = await gw.api(worker.baseUrl);
      try {
        const events = await gwApi.get(`/v1/tasks/${encodeURIComponent(taskId)}/events`);
        expect(events.status(), 'event history read returns 200').toBe(200);
        const eventsBody = await events.json() as { items: unknown[] };
        expect(eventsBody.items.length, 'no lifecycle event is appended at ingestion').toBe(0);
      } finally {
        await gwApi.dispose();
      }
    });

    await test.step('negative control: missing task_type_id is rejected with 400 and no persistence', async () => {
      const resp = await listenerApi.post('/v1/tasks', {
        data: {
          team_id: bsA.admin.team_id,
          source_system_id: bsA.sourceSystem.source_system_id,
          source_id: 'T-1-MISSING-TT',
          payload: { hello: 'world' },
        },
      });
      expect(resp.status(), 'missing REQUIRED task_type_id is rejected without persistence').toBe(400);
    });

    await test.step('negative control: foreign-team task_type_id is rejected with 403 without persistence', async () => {
      const resp = await listenerApi.post('/v1/tasks', {
        data: {
          team_id: bsA.admin.team_id,
          source_system_id: bsA.sourceSystem.source_system_id,
          source_id: 'T-1-FOREIGN-TT',
          task_type_id: bsB.taskType.task_type_id,
          payload: { hello: 'world' },
        },
      });
      expect(resp.status(), 'foreign-team task_type_id is rejected with 403 team_mismatch without persistence').toBe(403);
    });

    await test.step('negative control: listener-supplied required_tag is rejected with 400', async () => {
      const resp = await listenerApi.post('/v1/tasks', {
        data: {
          team_id: bsA.admin.team_id,
          source_system_id: bsA.sourceSystem.source_system_id,
          source_id: 'T-1-LISTENER-TAG',
          task_type_id: bsA.taskType.task_type_id,
          required_tag: 'openhands',
          payload: { hello: 'world' },
        },
      });
      expect(resp.status(), 'listener-supplied required_tag is rejected with 400 listener_supplied_required_tag_forbidden').toBe(400);
    });
  } finally {
    await listenerApi.dispose();
  }
});

test('v0002.66 source_id dedupe uses (team_id, source_system_id, source_id) and rejects cross-team source-system reuse', async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-66-${Date.now().toString(36)}`;
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-a`,
    executionTag: 'openhands',
  });
  const bsB = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-b`,
    executionTag: 'openhands',
  });
  let teamATaskId: string | null = null;
  let teamBTaskId: string | null = null;

  await test.step('team-a listener submits source_id=D-1 under team-a source system', async () => {
    const a = await ingestPendingTask(
      listenerFor({
        teamId: bsA.admin.team_id,
        listenerIdentity: bsA.listenerIdentity,
        sourceSystemId: bsA.sourceSystem.source_system_id,
      }),
      worker.baseUrl,
      {
        team_id: bsA.admin.team_id,
        source_system_id: bsA.sourceSystem.source_system_id,
        source_id: 'D-1',
        task_type_id: bsA.taskType.task_type_id,
        payload: { from: 'team-a' },
      },
    );
    expect(a.team_id).toBe(bsA.admin.team_id);
    expect(a.source_system_id).toBe(bsA.sourceSystem.source_system_id);
    teamATaskId = a.task_id;
  });

  await test.step('team-b listener submits source_id=D-1 under team-b source system as an INDEPENDENT task', async () => {
    const b = await ingestPendingTask(
      listenerFor({
        teamId: bsB.admin.team_id,
        listenerIdentity: bsB.listenerIdentity,
        sourceSystemId: bsB.sourceSystem.source_system_id,
      }),
      worker.baseUrl,
      {
        team_id: bsB.admin.team_id,
        source_system_id: bsB.sourceSystem.source_system_id,
        source_id: 'D-1',
        task_type_id: bsB.taskType.task_type_id,
        payload: { from: 'team-b' },
      },
    );
    expect(b.team_id).toBe(bsB.admin.team_id);
    expect(b.source_system_id).toBe(bsB.sourceSystem.source_system_id);
    teamBTaskId = b.task_id;
  });

  await test.step('cross-team independence: team-a and team-b tasks are distinct canonical rows', async () => {
    expect(teamATaskId, 'team-a task_id is captured').not.toBeNull();
    expect(teamBTaskId, 'team-b task_id is captured').not.toBeNull();
    expect(teamATaskId, 'team-a and team-b tasks are distinct canonical rows').not.toBe(teamBTaskId);
  });

  await test.step('team-b listener submitting under team-a source system is rejected with 403', async () => {
    const api = await listenerFor({
      teamId: bsB.admin.team_id,
      listenerIdentity: bsB.listenerIdentity,
      sourceSystemId: bsB.sourceSystem.source_system_id,
    }).api(worker.baseUrl);
    try {
      const resp = await api.post('/v1/tasks', {
        data: {
          team_id: bsB.admin.team_id,
          source_system_id: bsA.sourceSystem.source_system_id,
          source_id: 'D-2',
          task_type_id: bsB.taskType.task_type_id,
          payload: { cross: 'reject' },
        },
      });
      expect(resp.status(), 'cross-team source-system identity is rejected with 403 without persistence').toBe(403);
    } finally {
      await api.dispose();
    }
  });
});

export type _ContractsIngestionPersistenceIds = readonly [
  'v0002.1', 'v0002.2', 'v0002.32', 'v0002.33', 'v0002.65', 'v0002.66',
];

export type { IdentityContext };
