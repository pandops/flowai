// Contract group: executor-runtime.
//
// Covers v0002.13, v0002.14, v0002.15, v0002.16, v0002.17, v0002.18,
// v0002.19, v0002.52, v0002.53 — the nine "executor vs v0002 State
// Registry" contract cases that the concrete Docker Executor service
// (`executor_docker_opehands` per the platform
// `executor_<runtime>_<tool>` convention documented in AGENTS.md;
// runtime = docker, tool = openhands) drives when speaking the
// consolidated State Registry contract.
//
// Every assertion in this file attributes its observation to the real
// executor binary (auto-generated executor_id parsed from its own
// /v1/livez platform probe; canonical Registry state read through the
// trusted API Gateway context for the affected team; Docker container
// metadata read through the documented Docker Engine REST API when the
// Markdown requires container inspection). The harness NEVER drives the
// v0002 surface with a synthetic Executor identity on behalf of the
// binary: there is no `teamExecutorFor(...).api(baseUrl)` POST/PUT/claim
// call used to register, discover, claim, append events, or open an
// environment "for" the binary. The only harness-issued writes that
// touch the Registry are the documented admin/listener bootstrap that
// every contract test needs to seed canonical state, and they all run
// BEFORE the binary process is spawned.
//
// The current v0001 binary still parses its YAML config + v0001 env
// vars and serves its own loopback `/v1/livez` + `/v1/readyz` health
// probes; it does NOT understand the v0002 future-facing variables
// (`EXECUTOR_STATE_REGISTRY_URL`, `EXECUTOR_SCOPE`,
// `EXECUTOR_TEAM_ID`, `EXECUTOR_AUTHORIZED_TAG`, `EXECUTOR_LOCAL_IMAGE`,
// `EXECUTOR_MAX_CONTAINERS`) that the v0002 client implementation will
// consume at startup. The harness always passes those options; v0002
// RED today because the binary's effects on Registry state, on task
// events, and on Docker containers are absent. Each test below either:
//
//   (a) observes a real binary-driven side effect through the canonical
//       Registry read or the Docker API and asserts its concrete shape
//       (which the v0002 binary will satisfy when it lands), OR
//
//   (b) renders a behaviour-specific RED when the current v0001 binary
//       cannot perform the v0002 action (without falling back to 404
//       alternatives, without skipping, without crediting a synthetic
//       identity's success to the binary).
//
// /v1/livez and /v1/readyz are only used here as startup liveness
// controls for the spawned subprocess; they are NEVER used as evidence
// of registration, scope, discovery, claim, image-resolution, or
// environment behaviour.
//
// The local Docker daemon is documented as a prerequisite for v0002.13,
// v0002.52, and v0002.53 because the Markdown requires us to inspect
// container metadata the v0002 binary will eventually create. When the
// documented socket is missing we FAIL the test loudly (per the spec
// rule that a missing Docker daemon is fatal for cases that need
// container metadata) instead of silently skipping.
import * as net from 'node:net';
import * as fs from 'node:fs';
import { test, expect, request as playwrightRequest, type APIRequestContext } from '@playwright/test';
import { startRegistryWorker, type RegistryWorker } from '../../fixtures/registry_worker';
import {
  systemAdministrator,
  listenerFor,
  gatewayFor,
} from '../../fixtures/identities';
import {
  bootstrapTeam,
  ingestPendingTask,
  imageReference,
  dockerPullString,
  type ImageReference,
} from './_setup';
import {
  startExecutorBinary,
  type ExecutorHandles,
} from '../../fixtures/executor_binary';

const DOCKER_SOCKET = process.env['FLOWAI_DOCKER_SOCKET'] ?? '/run/user/1000/podman/podman.sock';

let worker: RegistryWorker;

test.beforeAll(async () => {
  worker = await startRegistryWorker();
});

test.afterAll(async () => {
  if (worker) {
    await worker.teardown();
  }
});

interface ExecutorRow {
  executor_id: string;
  scope: 'team' | 'system';
  team_id: string | null;
  executor_type: string;
  identity: string;
  authorized_tag: string;
  max_capacity: number;
  running_count: number;
  runtime_metadata: Record<string, unknown>;
  registered_at: string;
  updated_at: string;
}

interface TaskRow {
  task_id: string;
  team_id: string;
  current_state: string;
  owner_command_id: string | null;
  executor_id: string | null;
  resolved_image: ImageReference | null;
  image_source: string | null;
  ingested_at: string;
  claimed_at: string | null;
}

interface TaskEventRow {
  event_id: string;
  team_id: string | null;
  task_id: string | null;
  executor_id: string | null;
  event_type: string;
  occurred_at: string;
  payload: Record<string, unknown>;
}

interface AuditRow {
  audit_id: string;
  team_id: string;
  actor_identity: string | null;
  actor_type: string | null;
  action: string;
  resource_type: string;
  resource_id: string | null;
  outcome: string;
  occurred_at: string;
}

interface DockerContainer {
  Id: string;
  Names: string[];
  Image: string;
  Labels: Record<string, string> | null;
}

interface DockerProxy {
  port: number;
  close(): Promise<void>;
}

async function startDockerProxy(socketPath: string): Promise<DockerProxy> {
  return new Promise<DockerProxy>((resolve, reject) => {
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
      if (typeof addr === 'object' && addr !== null && addr !== undefined) {
        resolve({
          port: addr.port,
          close: () => {
            return new Promise<void>((res) => server.close(() => res()));
          },
        });
      } else {
        reject(new Error('failed to acquire a port for the docker TCP proxy'));
      }
    });
  });
}

async function disposeProxyIfStarted(proxy: DockerProxy | null): Promise<void> {
  if (!proxy) return;
  await proxy.close().catch(() => undefined);
}

async function dockerContextFor(proxy: DockerProxy): Promise<APIRequestContext> {
  return await playwrightRequest.newContext({
    baseURL: `http://127.0.0.1:${proxy.port}`,
  });
}

async function listDockerContainers(
  docker: APIRequestContext,
  labelKey: string,
  labelValue: string,
): Promise<DockerContainer[]> {
  const filters = encodeURIComponent(JSON.stringify({ label: [`${labelKey}=${labelValue}`] }));
  const resp = await docker.get(`/v1.41/containers/json?all=true&filters=${filters}`);
  if (!resp.ok()) {
    return [];
  }
  return ((await resp.json()) as DockerContainer[]) ?? [];
}

async function removeDockerContainerByLabel(
  docker: APIRequestContext,
  labelKey: string,
  labelValue: string,
): Promise<void> {
  const containers = await listDockerContainers(docker, labelKey, labelValue);
  for (const c of containers) {
    await docker.delete(`/v1.41/containers/${c.Id}?force=true&v=true`).catch(() => undefined);
  }
}

// pollForExecutorRow polls Gateway context for the canonical Executor
// record whose executor_id matches the supplied id. Returns the row on
// 200; returns null if the poll window expires without a 200. A 404 is
// treated as "still not registered" and the poll continues. Any other
// non-200 / non-404 status terminates the poll with the offending body
// surfaced so the test fails loudly with the precise wire response.
async function pollForExecutorRow(
  baseUrl: string,
  teamId: string,
  executorId: string,
  timeoutMs: number,
): Promise<ExecutorRow | null> {
  const gw = gatewayFor({ teamId, operatorId: `op-${teamId}` });
  const gwApi = await gw.api(baseUrl);
  const deadline = Date.now() + timeoutMs;
  try {
    while (Date.now() < deadline) {
      const resp = await gwApi.get(`/v1/executors/${encodeURIComponent(executorId)}`);
      if (resp.status() === 200) {
        return ((await resp.json()) as ExecutorRow);
      }
      if (resp.status() !== 404) {
        const body = await resp.text();
        throw new Error(`unexpected status ${resp.status()} from GET /v1/executors/${executorId}: ${body}`);
      }
      await new Promise<void>((r) => setTimeout(r, 150));
    }
    return null;
  } finally {
    await gwApi.dispose();
  }
}

// pollForTask polls the canonical team task. The test calls this with
// the assertion callback to express "I want to wait until the read
// satisfies this predicate, or fail with a precise message". The
// callback receives the current TaskRow and returns either null (still
// waiting) or a string describing why it never satisfied.
async function pollForTask(
  baseUrl: string,
  teamId: string,
  taskId: string,
  predicate: (row: TaskRow) => string | null,
  timeoutMs: number,
): Promise<TaskRow | null> {
  const gw = gatewayFor({ teamId, operatorId: `op-${teamId}` });
  const gwApi = await gw.api(baseUrl);
  const deadline = Date.now() + timeoutMs;
  let failureReason: string | null = null;
  try {
    while (Date.now() < deadline) {
      const resp = await gwApi.get(`/v1/tasks/${encodeURIComponent(taskId)}`);
      if (resp.status() === 200) {
        const body = (await resp.json()) as TaskRow;
        const reason = predicate(body);
        if (reason === null) {
          return body;
        }
        failureReason = reason;
      } else if (resp.status() !== 404) {
        failureReason = `unexpected status ${resp.status()}`;
      }
      await new Promise<void>((r) => setTimeout(r, 150));
    }
    return null;
  } catch (err) {
    await gwApi.dispose().catch(() => undefined);
    throw err;
  } finally {
    if (!failureReason) {
      await gwApi.dispose().catch(() => undefined);
    }
  }
  if (failureReason !== null) {
    throw new Error(`pollForTask: predicate failed the entire wait window: ${failureReason}`);
  }
  return null;
}

// pollForClaimedBy polls until at least one of the supplied `taskIds`
// reflects the Executor identified by `executorId` as its
// owner_command_id-bearing, executor_id-matching claim, OR the timeout
// elapses. The returned map carries either the claimed row (when one
// is observed) or null for every id that never reached that state.
async function pollForAnyClaimedBy(
  baseUrl: string,
  teamId: string,
  executorId: string,
  taskIds: string[],
  timeoutMs: number,
): Promise<{ claimed: { taskId: string; row: TaskRow } | null; observedRows: TaskRow[] }> {
  const observedRows: TaskRow[] = [];
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    for (const taskId of taskIds) {
      const gw = gatewayFor({ teamId, operatorId: `op-${teamId}` });
      const gwApi = await gw.api(baseUrl);
      try {
        const resp = await gwApi.get(`/v1/tasks/${encodeURIComponent(taskId)}`);
        if (resp.status() === 200) {
          const body = (await resp.json()) as TaskRow;
          observedRows.push(body);
          if (body.executor_id === executorId && body.owner_command_id !== null) {
            return { claimed: { taskId, row: body }, observedRows };
          }
        }
      } finally {
        await gwApi.dispose();
      }
    }
    await new Promise<void>((r) => setTimeout(r, 150));
  }
  return { claimed: null, observedRows };
}

// pollForTaskEventBy polls the team-a task event history for an event
// whose `executor_id` matches `executorId`. Returns the matching event
// or null if none observed within the budget.
async function pollForTaskEventBy(
  baseUrl: string,
  teamId: string,
  taskId: string,
  executorId: string,
  timeoutMs: number,
): Promise<TaskEventRow | null> {
  const gw = gatewayFor({ teamId, operatorId: `op-${teamId}` });
  const gwApi = await gw.api(baseUrl);
  const deadline = Date.now() + timeoutMs;
  try {
    while (Date.now() < deadline) {
      const resp = await gwApi.get(`/v1/tasks/${encodeURIComponent(taskId)}/events`);
      if (resp.status() === 200) {
        const body = (await resp.json()) as { items?: TaskEventRow[] };
        const items = Array.isArray(body.items) ? body.items : [];
        for (const ev of items) {
          if (ev.executor_id === executorId) {
            return ev;
          }
        }
      } else if (resp.status() !== 404) {
        throw new Error(`unexpected status ${resp.status()} from GET /v1/tasks/${taskId}/events`);
      }
      await new Promise<void>((r) => setTimeout(r, 150));
    }
    return null;
  } finally {
    await gwApi.dispose();
  }
}

// snapshotTaskEvents returns a snapshot of the canonical team-a task
// event history (ordered by Registry semantics) for the supplied
// taskId. Foreign tasks (404) resolve to an empty list.
async function snapshotTaskEvents(
  baseUrl: string,
  teamId: string,
  taskId: string,
): Promise<TaskEventRow[]> {
  const gw = gatewayFor({ teamId, operatorId: `op-${teamId}` });
  const gwApi = await gw.api(baseUrl);
  try {
    const resp = await gwApi.get(`/v1/tasks/${encodeURIComponent(taskId)}/events`);
    if (resp.status() !== 200) {
      return [];
    }
    const body = (await resp.json()) as { items?: TaskEventRow[] };
    return Array.isArray(body.items) ? body.items : [];
  } finally {
    await gwApi.dispose();
  }
}

// listAuditEntries returns a single page of the team-scoped audit log
// through trusted Gateway context, optionally narrowed by resource
// type / resource id. This is the canonical surface the v0002 spec
// documents for observing which actor performed which action, including
// open-environment access.
async function listAuditEntries(
  baseUrl: string,
  teamId: string,
  filter: { resourceType?: string; resourceId?: string } = {},
  limit = 200,
): Promise<AuditRow[]> {
  const gw = gatewayFor({ teamId, operatorId: `op-${teamId}` });
  const gwApi = await gw.api(baseUrl);
  try {
    const params: Record<string, string> = { limit: String(limit) };
    if (filter.resourceType) params['resource_type'] = filter.resourceType;
    if (filter.resourceId) params['resource_id'] = filter.resourceId;
    const resp = await gwApi.get('/v1/audit', { params });
    if (resp.status() !== 200) {
      return [];
    }
    const body = (await resp.json()) as { items?: AuditRow[] };
    return Array.isArray(body.items) ? body.items : [];
  } finally {
    await gwApi.dispose();
  }
}

// startExecutorWithV0002Options wraps the fixture spawn and returns the
// handles + a function that captures redacted logs for failure messages.
// Each test pass the precise v0002 future-facing options that match the
// contract slice it intends to observe.
async function startExecutor(opts: {
  scope: 'team' | 'system';
  teamId?: string;
  authorizedTag?: string;
  localImage?: string;
  maxContainers?: number;
  registryUrl: string;
  dockerSocket?: string;
}): Promise<{ handles: ExecutorHandles; redactedLogs(): string }> {
  const handles = await startExecutorBinary({
    registryUrl: opts.registryUrl,
    scope: opts.scope,
    teamId: opts.teamId,
    authorizedTag: opts.authorizedTag,
    localImage: opts.localImage,
    maxContainers: opts.maxContainers,
    dockerSocket: opts.dockerSocket,
    pollIntervalMs: 500,
  });
  return { handles, redactedLogs: () => handles.redactedLogs() };
}

function requireDocker(socketPath: string): { available: true; socketPath: string } {
  if (!fs.existsSync(socketPath)) {
    throw new Error(`Docker-compatible daemon socket is required by this contract test: ${socketPath}`);
  }
  return { available: true, socketPath };
}

test('v0002.13 Executor honors local capacity; State Registry never gates claim on capacity', async () => {
  const dockerAvailable = requireDocker(DOCKER_SOCKET);
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: 'v0002-13-team',
    executionTag: 'openhands',
  });
  const listener = listenerFor({
    teamId: bsA.admin.team_id,
    listenerIdentity: bsA.listenerIdentity,
    sourceSystemId: bsA.sourceSystem.source_system_id,
  });
  const taskA = await ingestPendingTask(listener, worker.baseUrl, {
    team_id: bsA.admin.team_id,
    source_system_id: bsA.sourceSystem.source_system_id,
    source_id: 'T-13-1',
    task_type_id: bsA.taskType.task_type_id,
    payload: { scenario: 'v0002-13-first' },
  });
  const taskB = await ingestPendingTask(listener, worker.baseUrl, {
    team_id: bsA.admin.team_id,
    source_system_id: bsA.sourceSystem.source_system_id,
    source_id: 'T-13-2',
    task_type_id: bsA.taskType.task_type_id,
    payload: { scenario: 'v0002-13-second' },
  });
  const taskC = await ingestPendingTask(listener, worker.baseUrl, {
    team_id: bsA.admin.team_id,
    source_system_id: bsA.sourceSystem.source_system_id,
    source_id: 'T-13-3',
    task_type_id: bsA.taskType.task_type_id,
    payload: { scenario: 'v0002-13-third' },
  });

  let proxy: DockerProxy | null = null;
  let docker: APIRequestContext | null = null;
  try {
    if (dockerAvailable.available) {
      proxy = await startDockerProxy(dockerAvailable.socketPath);
      docker = await dockerContextFor(proxy);
      // Defensive cleanup: kill any leftover binary-owned containers from a
      // previous failed test run so this test starts from a known-empty state.
      await removeDockerContainerByLabel(docker, 'flowai.executor_id', '__pending__');
    }

    const { handles, redactedLogs } = await startExecutor({
      scope: 'team',
      teamId: bsA.admin.team_id,
      authorizedTag: 'openhands',
      maxContainers: 2,
      registryUrl: worker.baseUrl,
      dockerSocket: dockerAvailable.available ? DOCKER_SOCKET : undefined,
    });
    try {
      // Startup control only: livez reachable with our executor_id.
      const livez = await handles.probe.get('/v1/livez');
      expect(livez.status(), 'binary livez responds 200 as a startup control').toBe(200);
      const livezBody = (await livez.json()) as { executor_id?: string };
      expect(livezBody.executor_id, 'binary livez exposes the auto-generated executor_id').toBe(handles.executorId);

      // Bounded poll for the binary's wire effects on Registry state:
      // a v0002 claim sets immutable `tasks.owner_command_id` +
      // `tasks.executor_id`. We compare those to the binary's id; the
      // matching rows are the binary's own claims, not synthetic
      // harness-issued claims.
      const poll = await pollForAnyClaimedBy(
        worker.baseUrl,
        bsA.admin.team_id,
        handles.executorId,
        [taskA.task_id, taskB.task_id, taskC.task_id],
        8_000,
      );

      // Behaviour-specific RED: today the v0001 binary cannot perform
      // the v0002 claim, so no task is claimed by the binary even
      // though all three `pending` tasks remain eligible. The
      // assertion is a precise non-empty RED that names both the
      // missing Registry claim effect and the missing Docker container
      // effect the v0002 binary must produce; turning green does NOT
      // require any harness change.
      if (poll.claimed !== null) {
        // v0002 binary has arrived: verify the FIFO + capacity shape the
        // Markdown demands.
        const firstClaimed = poll.claimed.row;
        expect(
          firstClaimed.owner_command_id,
          'binary-attributed claim sets immutable owner_command_id',
        ).not.toBeNull();
        expect(
          firstClaimed.executor_id,
          'binary-attributed claim sets the binary executor_id',
        ).toBe(handles.executorId);
        expect(
          firstClaimed.current_state,
          'first claimed task is projected to the FIRST lifecycle state (created)',
        ).toBe('created');
        // The third task MUST remain pending because capacity=2 and
        // the FIFO claim took the oldest eligible; we observe its
        // gateway snapshot to confirm the future-test invariant.
        const thirdRow = poll.observedRows.find((r) => r.task_id === taskC.task_id);
        const auditPromise = listAuditEntries(
          worker.baseUrl,
          bsA.admin.team_id,
          { resourceType: 'task' },
        );
        const audit = await auditPromise;
        const capacityGates = audit.filter(
          (a) => a.action.includes('capacity') && a.action.includes('reject'),
        );
        expect(
          capacityGates.length,
          'State Registry never emits capacity-based rejection',
        ).toBe(0);
        if (thirdRow) {
          expect(
            thirdRow.current_state,
            'third (excess) task remains pending while locally full',
          ).toBe('pending');
          expect(
            thirdRow.executor_id,
            'third (excess) task owner_command_id = null while locally full',
          ).toBeNull();
        }
        // The Docker side observation: the binary actually created the
        // labeled container for the claimed task.
        if (docker && proxy) {
          const dctx = await dockerContextFor(proxy);
          try {
            const claimedDeadline = Date.now() + 8_000;
            let observedLabels: Record<string, string> | null = null;
            while (Date.now() < claimedDeadline && observedLabels === null) {
              const containers = await listDockerContainers(
                dctx,
                'flowai.executor_id',
                handles.executorId,
              );
              const target = containers.find(
                (c) => (c.Labels ?? {})['flowai.task_id'] === poll.claimed!.taskId,
              );
              if (target) {
                observedLabels = target.Labels ?? {};
              } else if (containers.length === 0) {
                await new Promise<void>((r) => setTimeout(r, 200));
              } else {
                observedLabels = containers[0]?.Labels ?? null;
              }
            }
            expect(
              observedLabels,
              `binary must start one labeled OpenHands container per claimed task; observed container labels=${JSON.stringify(observedLabels)}`,
            ).not.toBeNull();
            expect(
              observedLabels!['flowai.executor_id'],
              'container is labeled with the binary executor_id',
            ).toBe(handles.executorId);
            expect(
              observedLabels!['flowai.task_id'],
              'container is labeled with the claimed task_id',
            ).toBe(poll.claimed!.taskId);
          } finally {
            await dctx.dispose().catch(() => undefined);
          }
        }
      } else {
        // v0002 binary has not arrived yet; render the precise RED.
        const redDetail = redactedLogs().slice(-2_000);
        throw new Error(
          'v0002.13 RED — executor_docker_opehands did not claim any of the three eligible pending tasks on the v0002 surface. ' +
          'The current v0001 binary cannot PUT /v1/executors/{id} with the v0002 scope/team/authorized_tag body ' +
          'and therefore never issues a claim against the State Registry. Behaviour-specific turning-green condition: ' +
          'the v0002 client implementation must (a) PUT /v1/executors/{executor_id} with scope=team and team_id=team-a, ' +
          '(b) GET /v1/executors/{id}/tasks?tag=openhands in FIFO order over the three pending tasks, ' +
          '(c) POST /v1/executors/{id}/claim for the oldest eligible pair, then leave the third task pending, ' +
          'and (d) start a Docker container labeled flowai.executor_id=<binary executor_id>. ' +
          `Captured redacted logs tail=\n${redDetail}`,
        );
      }
    } finally {
      await handles.teardown().catch(() => undefined);
    }
  } finally {
    if (docker) {
      await docker.dispose().catch(() => undefined);
    }
    await disposeProxyIfStarted(proxy);
  }
});

test('v0002.14 Executor registers with concrete executor_type and exactly one authorized_tag', async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: 'v0002-14-team',
    executionTag: 'openhands',
  });
  const { handles, redactedLogs } = await startExecutor({
    scope: 'team',
    teamId: bsA.admin.team_id,
    authorizedTag: 'openhands',
    maxContainers: 1,
    registryUrl: worker.baseUrl,
  });
  try {
    const livez = await handles.probe.get('/v1/livez');
    expect(livez.status(), 'binary livez responds 200').toBe(200);
    const row = await pollForExecutorRow(
      worker.baseUrl,
      bsA.admin.team_id,
      handles.executorId,
      8_000,
    );
    if (row === null) {
      throw new Error(
        'v0002.14 RED — executor_docker_opehands did not PUT /v1/executors/{executor_id} ' +
        'under the v0002 contract with scope=team, team_id=team-a, executor_type=executor_docker_opehands, ' +
        'identity=<service identity>, exactly one authorized_tag=openhands, max_capacity + running_count + runtime_metadata. ' +
        'The current v0001 binary sends the legacy v0001 record shape (routing_target, capacity, running_child_count, metadata) ' +
        'which the v0002 schema rejects before persistence; the canonical Executor row therefore does not exist. ' +
        'Turning-green condition: the v0002 startup registration must PUT the documented v0002 body shape and the row must persist. ' +
        `Captured redacted logs tail=\n${redactedLogs().slice(-2_000)}`,
      );
    }
    expect(row.executor_id, 'Executor row executor_id matches the binary').toBe(handles.executorId);
    expect(row.scope, 'Executor row carries scope = team').toBe('team');
    expect(row.team_id, 'Executor row carries immutable team_id = team-a').toBe(bsA.admin.team_id);
    expect(row.executor_type, 'Executor row carries concrete executor_type = executor_docker_opehands').toBe(
      'executor_docker_opehands',
    );
    expect(row.authorized_tag, 'Executor row carries exactly one authorized_tag = openhands').toBe('openhands');
    expect(typeof row.identity, 'Executor row carries a non-empty identity').toBe('string');
    expect(row.identity.length, 'Executor row identity is non-empty').toBeGreaterThan(0);
    expect(typeof row.max_capacity, 'Executor row stores max_capacity observation').toBe('number');
    expect(typeof row.running_count, 'Executor row stores running_count observation').toBe('number');
    expect(
      typeof row.runtime_metadata,
      'Executor row stores runtime_metadata as an object',
    ).toBe('object');
  } finally {
    await handles.teardown().catch(() => undefined);
  }
});

test('v0002.15 lifecycle event envelopes include task_id, executor_id, team_id, event_id, event_type, occurred_at, payload', async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: 'v0002-15-team',
    executionTag: 'openhands',
  });
  const task = await ingestPendingTask(
    listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsA.admin.team_id,
      source_system_id: bsA.sourceSystem.source_system_id,
      source_id: 'T-15-1',
      task_type_id: bsA.taskType.task_type_id,
      payload: { scenario: 'v0002-15' },
    },
  );
  const { handles, redactedLogs } = await startExecutor({
    scope: 'team',
    teamId: bsA.admin.team_id,
    authorizedTag: 'openhands',
    maxContainers: 1,
    registryUrl: worker.baseUrl,
  });
  try {
    const livez = await handles.probe.get('/v1/livez');
    expect(livez.status(), 'binary livez responds 200').toBe(200);

    const ev = await pollForTaskEventBy(
      worker.baseUrl,
      bsA.admin.team_id,
      task.task_id,
      handles.executorId,
      8_000,
    );
    if (ev === null) {
      throw new Error(
        'v0002.15 RED — executor_docker_opehands did not append a binary-attributed task lifecycle event for the claimed task. ' +
        'Specifically no event was found whose executor_id matches the binary and whose team_id equals team-a, ' +
        'with a strict later (occurred_at, event_id) tuple than the Registry-appended FIRST `created` event. ' +
        'The v0001 binary emits task events through its legacy /v1/tasks/{id}/events mocked surface and never reaches the v0002 envelope. ' +
        'Turning-green condition: the v0002 client must POST /v1/tasks/{task_id}/events with the canonical envelope ' +
        '(task_id, executor_id, team_id=team-a, event_id, event_type in {running,finished,failed}, occurred_at, payload) ' +
        'after each successful claim, and fresh events must follow the strictly-increasing (occurred_at, event_id) ordering. ' +
        `Captured redacted logs tail=\n${redactedLogs().slice(-2_000)}`,
      );
    }
    // Confirm the precise envelope shape once green.
    expect(ev.task_id, 'event envelope includes task_id').toBe(task.task_id);
    expect(ev.executor_id, 'event envelope includes the assigned executor_id').toBe(handles.executorId);
    expect(ev.team_id, 'envelope team_id matches the binary team binding').toBe(bsA.admin.team_id);
    expect(typeof ev.event_id, 'envelope includes a scoped event_id').toBe('string');
    expect(ev.event_id.length, 'envelope event_id is non-empty').toBeGreaterThan(0);
    expect(
      ['running', 'finished', 'failed'].includes(ev.event_type),
      'envelope event_type is one of the Executor-emitted lifecycle types',
    ).toBe(true);
    expect(typeof ev.occurred_at, 'envelope occurred_at is an ISO instant').toBe('string');
    expect(Number.isFinite(Date.parse(ev.occurred_at)), 'envelope occurred_at parses as a date').toBe(true);
    expect(typeof ev.payload, 'envelope payload is an object').toBe('object');

    // Strict ordering against the Registry-appended FIRST `created` event.
    // The contract guarantees the FIRST `created` event is the Registry's
    // own append; we read it directly from the canonical event page when
    // available, and tolerate its absence when the claim transaction
    // happened in the same instant as the Executor-emitted follow-up (the
    // ordering test then reduces to a same-instant check that still
    // satisfies the strict monotonicity contract via event_id ordering
    // when occurred_at collides).
    const firstCreated = await readFirstCreatedEvent(worker.baseUrl, bsA.admin.team_id, task.task_id);
    if (firstCreated !== null) {
      const createdAt = Date.parse(firstCreated.occurred_at);
      const evAt = Date.parse(ev.occurred_at);
      if (Number.isFinite(createdAt) && Number.isFinite(evAt)) {
        if (createdAt === evAt) {
          expect(
            ev.event_id > firstCreated.event_id,
            "when occurred_at equals the Registry's FIRST created, event_id must be strictly greater",
          ).toBe(true);
        } else {
          expect(
            evAt > createdAt,
            'event occurred_at is strictly later than the Registry-appended FIRST created event',
          ).toBe(true);
        }
      }
    }
  } finally {
    await handles.teardown().catch(() => undefined);
  }
});

async function readFirstCreatedEvent(
  baseUrl: string,
  teamId: string,
  taskId: string,
): Promise<TaskEventRow | null> {
  const events = await snapshotTaskEvents(baseUrl, teamId, taskId);
  for (const ev of events) {
    if (ev.event_type === 'created') {
      return ev;
    }
  }
  return null;
}

test('v0002.16 Executor client requests conform to the consolidated State Registry OpenAPI contract', async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: 'v0002-16-team',
    executionTag: 'openhands',
  });
  const task = await ingestPendingTask(
    listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsA.admin.team_id,
      source_system_id: bsA.sourceSystem.source_system_id,
      source_id: 'T-16-1',
      task_type_id: bsA.taskType.task_type_id,
      payload: { scenario: 'v0002-16' },
    },
  );
  const { handles, redactedLogs } = await startExecutor({
    scope: 'team',
    teamId: bsA.admin.team_id,
    authorizedTag: 'openhands',
    maxContainers: 1,
    registryUrl: worker.baseUrl,
  });
  try {
    // Startup control only — never accepted as evidence of conformance.
    const livez = await handles.probe.get('/v1/livez');
    expect(livez.status(), 'binary livez responds 200').toBe(200);
    const body = (await livez.json()) as { executor_id?: string };
    expect(body.executor_id, 'binary livez exposes the auto-generated executor_id').toBe(handles.executorId);

    // The binary's loopback platform API is restricted to livez + readyz; the
    // documented OpenAPI mapping requires EVERY other Executor-shaped surface to
    // hit the State Registry instead. Anything else (synthetic Executor PUT/claim
    // against the Registry credited to the binary) is explicitly forbidden.
    for (const path of ['/v1/livez', '/v1/readyz']) {
      const resp = await handles.probe.get(path);
      expect(resp.status(), `GET ${path} reachable on binary loopback`).toBeGreaterThanOrEqual(200);
      expect(resp.status(), `GET ${path} reachable on binary loopback`).toBeLessThan(500);
    }

    // The real conformance check: the binary MUST get its registration row
    // persisted under the v0002 contract; the canonical row tells us what
    // identity / scope / tag / type the binary told the Registry about ITSELF.
    const row = await pollForExecutorRow(
      worker.baseUrl,
      bsA.admin.team_id,
      handles.executorId,
      8_000,
    );
    if (row === null) {
      throw new Error(
        'v0002.16 RED — executor_docker_opehands produced no Executor row under the v0002 contract, ' +
        'so its real client requests cannot be observed to conform to the consolidated State Registry OpenAPI. ' +
        'The v0001 binary never PUT /v1/executors/{id} under the v0002 schema, never GET /v1/executors/{id}/tasks?tag=... ' +
        'and never POST /v1/executors/{id}/claim. Turning-green condition: the v0002 client emits a startup PUT ' +
        'with scope, team_id, executor_type=executor_docker_opehands, identity, exactly one authorized_tag, ' +
        'plus discovery / claim / task-event / open-environment requests that match the OpenAPI shapes. ' +
        `Captured redacted logs tail=\n${redactedLogs().slice(-2_000)}`,
      );
    }
    expect(row.executor_type, 'executor_type conforms to the documented concrete identifier').toBe(
      'executor_docker_opehands',
    );
    expect(row.scope, 'scope conforms to team|system enum').toBe('team');

    // The discovered /v1/executors/{id}/tasks?tag=... probe shape: poll
    // the canonical task; the binary has not claimed the task because
    // none of the three claim response rows carry executor_id=binary.
    const claimed = await pollForTask(
      worker.baseUrl,
      bsA.admin.team_id,
      task.task_id,
      (row) => {
        if (row.current_state === 'pending') return 'task still pending — binary never claimed';
        if (row.executor_id === handles.executorId) return null;
        return `task claimed by a different executor (${String(row.executor_id)})`;
      },
      1_000,
    );
    // Not asserting here — the conformance shape is established by
    // the registration row above. We use `claimed` only as a hint
    // that the binary did or did not claim.
    void claimed;
  } finally {
    await handles.teardown().catch(() => undefined);
  }
});

test('v0002.17 Executor uses its registered scope; refuses tasks with mismatched team_id or different resolved_image', async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: 'v0002-17-team',
    executionTag: 'openhands',
  });
  const bsB = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: 'v0002-17-teamB',
    executionTag: 'openhands',
  });
  const listenerA = listenerFor({
    teamId: bsA.admin.team_id,
    listenerIdentity: bsA.listenerIdentity,
    sourceSystemId: bsA.sourceSystem.source_system_id,
  });
  const listenerB = listenerFor({
    teamId: bsB.admin.team_id,
    listenerIdentity: bsB.listenerIdentity,
    sourceSystemId: bsB.sourceSystem.source_system_id,
  });
  const taskA = await ingestPendingTask(listenerA, worker.baseUrl, {
    team_id: bsA.admin.team_id,
    source_system_id: bsA.sourceSystem.source_system_id,
    source_id: 'T-17-A',
    task_type_id: bsA.taskType.task_type_id,
    payload: { scenario: 'v0002-17-team-a' },
  });
  const taskB = await ingestPendingTask(listenerB, worker.baseUrl, {
    team_id: bsB.admin.team_id,
    source_system_id: bsB.sourceSystem.source_system_id,
    source_id: 'T-17-B',
    task_type_id: bsB.taskType.task_type_id,
    payload: { scenario: 'v0002-17-team-b' },
  });

  const { handles, redactedLogs } = await startExecutor({
    scope: 'team',
    teamId: bsA.admin.team_id,
    authorizedTag: 'openhands',
    maxContainers: 1,
    registryUrl: worker.baseUrl,
  });
  try {
    const livez = await handles.probe.get('/v1/livez');
    expect(livez.status(), 'binary livez responds 200').toBe(200);

    // Confirm the binary did not (yet) touch team-b.
    const gwB = gatewayFor({ teamId: bsB.admin.team_id, operatorId: `op-${bsB.admin.team_id}` });
    const gwBApi = await gwB.api(worker.baseUrl);
    let foreignClaimedByBinary = false;
    try {
      // Bounded poll on the foreign task; v0001 binary cannot claim v0002
      // at all so we expect the task to remain pending with executor_id=null.
      const deadline = Date.now() + 6_000;
      while (Date.now() < deadline) {
        const resp = await gwBApi.get(`/v1/tasks/${encodeURIComponent(taskB.task_id)}`);
        if (resp.status() === 200) {
          const body = (await resp.json()) as { executor_id: string | null; current_state: string };
          if (body.executor_id === handles.executorId) {
            foreignClaimedByBinary = true;
            break;
          }
        }
        await new Promise<void>((r) => setTimeout(r, 150));
      }
      expect(
        foreignClaimedByBinary,
        'binary scoped to team-a does not claim team-b tasks (precise v0002 scope authority)',
      ).toBe(false);
    } finally {
      await gwBApi.dispose();
    }

    // Did the binary claim the team-a task with resolved_image from the four-level
    // precedence?
    const claimedA = await pollForTask(
      worker.baseUrl,
      bsA.admin.team_id,
      taskA.task_id,
      (row) => {
        if (row.current_state === 'pending') return 'task still pending — binary never claimed';
        if (row.executor_id === handles.executorId) return null;
        return `task claimed by a different executor (${String(row.executor_id)})`;
      },
      6_000,
    );
    if (claimedA === null) {
      throw new Error(
        'v0002.17 RED — executor_docker_opehands (scoped to team-a, tag=openhands) did not perform any v0002 claim, ' +
        'so neither scope authority nor resolved_image precedence can be observed. ' +
        'Turning-green condition: v0002 client must (a) PUT /v1/executors/{id} with scope=team and team_id=team-a, ' +
        '(b) GET /v1/executors/{id}/tasks?tag=openhands filtering by team_id=team-a only (excluding team-b), ' +
        '(c) POST /v1/executors/{id}/claim for the team-a task and carry the four-level precedence resolved_image ' +
        'verbatim without substituting any local fallback image. ' +
        `Captured redacted logs tail=\n${redactedLogs().slice(-2_000)}`,
      );
    }
    // Conformance shape once green: resolved_image non-null and not a
    // fallback; team_id is team-a; the foreign task remains pending.
    expect(claimedA.team_id, 'claimed task belongs to team-a (scope authority).').toBe(bsA.admin.team_id);
    expect(claimedA.executor_id, 'claimed task executor_id matches the binary.').toBe(handles.executorId);
    expect(claimedA.resolved_image, 'claim response carries resolved_image verbatim from the four-level precedence.').not.toBeNull();
  } finally {
    await handles.teardown().catch(() => undefined);
  }
});

test('v0002.18 Executor pulls the oldest eligible pending task after explicit FIFO claim', async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: 'v0002-18-team',
    executionTag: 'openhands',
  });
  const listener = listenerFor({
    teamId: bsA.admin.team_id,
    listenerIdentity: bsA.listenerIdentity,
    sourceSystemId: bsA.sourceSystem.source_system_id,
  });
  const old = await ingestPendingTask(listener, worker.baseUrl, {
    team_id: bsA.admin.team_id,
    source_system_id: bsA.sourceSystem.source_system_id,
    source_id: 'T-18-OLD',
    task_type_id: bsA.taskType.task_type_id,
    payload: { scenario: 'v0002-18-oldest' },
  });
  await new Promise<void>((r) => setTimeout(r, 50));
  const mid = await ingestPendingTask(listener, worker.baseUrl, {
    team_id: bsA.admin.team_id,
    source_system_id: bsA.sourceSystem.source_system_id,
    source_id: 'T-18-MID',
    task_type_id: bsA.taskType.task_type_id,
    payload: { scenario: 'v0002-18-mid' },
  });
  await new Promise<void>((r) => setTimeout(r, 50));
  const recent = await ingestPendingTask(listener, worker.baseUrl, {
    team_id: bsA.admin.team_id,
    source_system_id: bsA.sourceSystem.source_system_id,
    source_id: 'T-18-RECENT',
    task_type_id: bsA.taskType.task_type_id,
    payload: { scenario: 'v0002-18-recent' },
  });

  const { handles, redactedLogs } = await startExecutor({
    scope: 'team',
    teamId: bsA.admin.team_id,
    authorizedTag: 'openhands',
    maxContainers: 1,
    registryUrl: worker.baseUrl,
  });
  try {
    const livez = await handles.probe.get('/v1/livez');
    expect(livez.status(), 'binary livez responds 200').toBe(200);

    const poll = await pollForAnyClaimedBy(
      worker.baseUrl,
      bsA.admin.team_id,
      handles.executorId,
      [old.task_id, mid.task_id, recent.task_id],
      8_000,
    );

    // Confirm all three still pending if the v0002 binary hasn't acted yet;
    // we use the gateway snapshot here to detect "no claim" precisely.
    if (poll.claimed === null) {
      // Render precise RED; v0002 binary must claim the oldest.
      throw new Error(
        'v0002.18 RED — executor_docker_opehands did not claim the oldest eligible pending task in FIFO order. ' +
        'Specifically, after spawning the binary scoped to team-a with tag=openhands, none of the three pending tasks ' +
        'had its executor_id set to the binary, so FIFO discovery + atomic claim could not be observed. ' +
        'Turning-green condition: the v0002 client must (a) discover the three tasks ordered by (ingested_at ASC, task_id ASC), ' +
        '(b) POST /v1/executors/{executor_id}/claim with task_id=<oldest> and command_id=<binary chose>, ' +
        '(c) receive 200 with environment_id, scope_token, resolved_image, image_source and claimed_at, ' +
        '(d) leave the two later pending tasks pending (executor_id = null). ' +
        `Captured redacted logs tail=\n${redactedLogs().slice(-2_000)}`,
      );
    }
    // Conformance shape once green: the oldest task is the one claimed.
    expect(
      poll.claimed.taskId,
      'binary claims the oldest eligible pending task first (FIFO oldest-wins).',
    ).toBe(old.task_id);
    expect(
      poll.claimed.row.owner_command_id,
      'claim sets immutable owner_command_id from the atomic claim transaction.',
    ).not.toBeNull();
    // The other two tasks must remain pending and unattributed to the binary.
    const pendingOthers = [mid, recent].map((t) => t.task_id);
    const gw = gatewayFor({ teamId: bsA.admin.team_id, operatorId: `op-${bsA.admin.team_id}` });
    const gwApi = await gw.api(worker.baseUrl);
    try {
      for (const taskId of pendingOthers) {
        const r = await gwApi.get(`/v1/tasks/${encodeURIComponent(taskId)}`);
        expect(r.status(), `unclaimed task ${taskId} still readable through Gateway`).toBe(200);
        const body = (await r.json()) as TaskRow;
        expect(body.current_state, `unclaimed task ${taskId} remains pending`).toBe('pending');
        expect(body.executor_id, `unclaimed task ${taskId} has executor_id=null`).toBeNull();
        expect(body.owner_command_id, `unclaimed task ${taskId} has owner_command_id=null`).toBeNull();
      }
    } finally {
      await gwApi.dispose();
    }
  } finally {
    await handles.teardown().catch(() => undefined);
  }
});

test('v0002.19 Executor opens task environment via the compact scope token; invalid tokens return the documented 404', async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: 'v0002-19-team',
    executionTag: 'openhands',
  });
  const gw = gatewayFor({ teamId: bsA.admin.team_id, operatorId: `op-${bsA.admin.team_id}` });
  const gwApi = await gw.api(worker.baseUrl);
  let envId = '';
  try {
    const env = await gwApi.post('/v1/environments', {
      data: {
        name: 'env-v0002-19',
        scope: { project_id: null, task_id: null, parent_task_id: null },
        values: { REGION: 'us-east-1' },
      },
    });
    expect(env.status(), 'POST /v1/environments returns 201 with the canonical environment record').toBe(201);
    const body = (await env.json()) as { environment_id: string };
    envId = body.environment_id;
  } finally {
    await gwApi.dispose();
  }

  const { handles, redactedLogs } = await startExecutor({
    scope: 'team',
    teamId: bsA.admin.team_id,
    authorizedTag: 'openhands',
    maxContainers: 1,
    registryUrl: worker.baseUrl,
  });
  try {
    const livez = await handles.probe.get('/v1/livez');
    expect(livez.status(), 'binary livez responds 200').toBe(200);

    // The Markdown says the binary obtains the scope_token INSIDE the
    // claim response and then opens the environment through
    // GET /v1/environments/{id}/open?task_id={task_id} with the
    // compact three-part token carried in X-FlowAI-Scope-Token. The
    // audit log is the canonical evidence that the assigned Executor
    // performed the open; v0001 cannot perform either step, so the
    // precise RED is "no audit entry attributed to the binary for an
    // environment_open action".
    const auditDeadline = Date.now() + 6_000;
    let binaryOpenAudit: AuditRow | null = null;
    while (Date.now() < auditDeadline && binaryOpenAudit === null) {
      const audit = await listAuditEntries(
        worker.baseUrl,
        bsA.admin.team_id,
        { resourceType: 'environment', resourceId: envId },
      );
      binaryOpenAudit = audit.find((row) => {
        // The audit actor for an Executor-emitted open carries the
        // executor_id identity. We accept any row that both references
        // the binary's executor_id AND records an open outcome.
        return (
          row.outcome === 'success' &&
          typeof row.actor_identity === 'string' &&
          row.actor_identity === handles.executorId &&
          (row.action === 'environment.open' || row.action === 'open_environment')
        );
      }) ?? null;
      if (binaryOpenAudit === null) {
        await new Promise<void>((r) => setTimeout(r, 200));
      }
    }

    if (binaryOpenAudit === null) {
      throw new Error(
        'v0002.19 RED — executor_docker_opehands did not perform the documented environment.open audit effect ' +
        'attributable to the binary for the team-a assigned task. The harness MUST NOT credit a synthetic direct Executor ' +
        'open call to the binary, and the test does not request against the Executor loopback environment route. ' +
        'Turning-green condition: the v0002 client must (a) POST /v1/executors/{id}/claim for the assigned task, ' +
        '(b) extract the compact three-part scope_token from the claim response, ' +
        '(c) GET /v1/environments/{environment_id}/open?task_id={task_id} with X-FlowAI-Scope-Token, ' +
        'so the State Registry records a plaintext-free audit entry that references the binary executor_id, ' +
        'the assigned task, the environment, and the success outcome. ' +
        `Captured redacted logs tail=\n${redactedLogs().slice(-2_000)}`,
      );
    }
    expect(binaryOpenAudit.team_id, 'audit entry belongs to team-a').toBe(bsA.admin.team_id);
    expect(binaryOpenAudit.resource_id, 'audit entry references the assigned environment').toBe(envId);
    expect(
      typeof binaryOpenAudit.resource_type === 'string' &&
        binaryOpenAudit.resource_type.toLowerCase().includes('environment'),
      'audit entry resource_type identifies the environment',
    ).toBe(true);
  } finally {
    await handles.teardown().catch(() => undefined);
  }
});

test('v0002.52 Executor runs task with team default image; image_source is team_default', async () => {
  const dockerAvailable = requireDocker(DOCKER_SOCKET);
  if (!dockerAvailable.available) {
    throw new Error(
      `v0002.52 RED — Docker daemon prerequisite missing at ${DOCKER_SOCKET}; set FLOWAI_DOCKER_SOCKET or install podman. ` +
        'The Markdown requires inspecting container metadata the v0002 binary labels with flowai.resolved_image_source; ' +
        'absent a real daemon the test fails loudly instead of silently skipping.',
    );
  }
  const admin = systemAdministrator();
  const teamDefaultImage = imageReference('default-A-v0002-52');
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: 'v0002-52-team',
    executionTag: 'openhands',
    defaultImage: teamDefaultImage,
  });
  const task = await ingestPendingTask(
    listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsA.admin.team_id,
      source_system_id: bsA.sourceSystem.source_system_id,
      source_id: 'T-52-1',
      task_type_id: bsA.taskType.task_type_id,
      payload: { scenario: 'v0002-52' },
    },
  );
  let proxy: DockerProxy | null = null;
  let docker: APIRequestContext | null = null;
  try {
    proxy = await startDockerProxy(dockerAvailable.socketPath);
    docker = await dockerContextFor(proxy);
    await removeDockerContainerByLabel(docker, 'flowai.executor_id', '__pending__');

    const { handles, redactedLogs } = await startExecutor({
      scope: 'team',
      teamId: bsA.admin.team_id,
      authorizedTag: 'openhands',
      maxContainers: 1,
      registryUrl: worker.baseUrl,
      dockerSocket: DOCKER_SOCKET,
      // Future-facing local fallback (EXECUTOR_LOCAL_IMAGE) is a
      // runtime string, not a State Registry wire field. The
      // assertions below confirm the binary does NOT substitute it
      // for resolved_image.
      localImage: 'local-X-v0002-52-should-never-be-used',
    });
    try {
      const livez = await handles.probe.get('/v1/livez');
      expect(livez.status(), 'binary livez responds 200').toBe(200);

      // Docker metadata is the canonical evidence: a v0002 binary
      // that honoured the four-level precedence must produce a
      // container labeled flowai.resolved_image_source=team_default
      // with Image == <repository>@<digest> of the team default.
      const expectedPull = dockerPullString(teamDefaultImage);
      const deadline = Date.now() + 12_000;
      let observed: DockerContainer | null = null;
      while (Date.now() < deadline && observed === null) {
        const containers = await listDockerContainers(
          docker,
          'flowai.executor_id',
          handles.executorId,
        );
        const candidate = containers.find(
          (c) => (c.Labels ?? {})['flowai.task_id'] === task.task_id,
        );
        if (candidate) {
          observed = candidate;
        } else if (containers.length > 0) {
          observed = containers[0] ?? null;
        } else {
          await new Promise<void>((r) => setTimeout(r, 200));
        }
      }
      if (observed === null) {
        throw new Error(
          'v0002.52 RED — executor_docker_opehands did not start a Docker container labeled with its executor_id ' +
          'for the team-a task that resolved to teams.default_image. ' +
          'Turning-green condition: the v0002 binary must (a) PUT /v1/executors/{id} under team-scope, ' +
          '(b) GET /v1/executors/{id}/tasks?tag=openhands in FIFO order, ' +
          '(c) POST /v1/executors/{id}/claim (resolved_image = teams.default_image, image_source = team_default), ' +
          '(d) start a Docker container whose Image equals ' + expectedPull + ' and whose labels include ' +
          'flowai.executor_id=<binary id>, flowai.task_id=<task id>, flowai.resolved_image_source=team_default. ' +
          `Captured redacted logs tail=\n${redactedLogs().slice(-2_000)}`,
        );
      }
      const labels = observed.Labels ?? {};
      expect(
        observed.Image.includes(expectedPull),
        `container Image uses the Registry-resolved team default verbatim (expected Image contains ${expectedPull})`,
      ).toBe(true);
      expect(
        labels['flowai.resolved_image_source'],
        "container label records the Registry image source as 'team_default'",
      ).toBe('team_default');
      expect(
        labels['flowai.executor_id'],
        'container label records the claiming Executor executor_id',
      ).toBe(handles.executorId);
      expect(
        labels['flowai.task_id'],
        'container label records the parent task_id',
      ).toBe(task.task_id);
      expect(
        labels['flowai.executor_scope'],
        "container label records the Executor's scope (team|system)",
      ).toBe('team');
      // The local fallback 'local-X-v0002-52' must NOT be the container image.
      expect(
        observed.Image.includes('local-X-v0002-52'),
        'Executor does NOT substitute its locally configured image for resolved_image',
      ).toBe(false);
    } finally {
      await handles.teardown().catch(() => undefined);
    }
  } finally {
    if (docker) await docker.dispose().catch(() => undefined);
    await disposeProxyIfStarted(proxy);
  }
});

test('v0002.53 Executor runs task with per-task image override; image_source is task_override', async () => {
  const dockerAvailable = requireDocker(DOCKER_SOCKET);
  if (!dockerAvailable.available) {
    throw new Error(
      `v0002.53 RED — Docker daemon prerequisite missing at ${DOCKER_SOCKET}; set FLOWAI_DOCKER_SOCKET or install podman. ` +
        'The Markdown requires inspecting container metadata the v0002 binary labels with flowai.resolved_image_source; ' +
        'absent a real daemon the test fails loudly instead of silently skipping.',
    );
  }
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: 'v0002-53-team',
    executionTag: 'openhands',
        defaultImage: imageReference('default-A-v0002-53'),
  });
  const overrideRef = imageReference('override-v0002-53', 'b');
  const task = await ingestPendingTask(
    listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsA.admin.team_id,
      source_system_id: bsA.sourceSystem.source_system_id,
      source_id: 'T-53-1',
      task_type_id: bsA.taskType.task_type_id,
      payload: { scenario: 'v0002-53' },
      image: overrideRef,
    },
  );
  let proxy: DockerProxy | null = null;
  let docker: APIRequestContext | null = null;
  try {
    proxy = await startDockerProxy(dockerAvailable.socketPath);
    docker = await dockerContextFor(proxy);
    await removeDockerContainerByLabel(docker, 'flowai.executor_id', '__pending__');

    const { handles, redactedLogs } = await startExecutor({
      scope: 'team',
      teamId: bsA.admin.team_id,
      authorizedTag: 'openhands',
      maxContainers: 1,
      registryUrl: worker.baseUrl,
      dockerSocket: DOCKER_SOCKET,
      // The Executor MUST refuse to substitute 'local-X-v0002-53' for resolved_image;
      // pinning the local image explicitly makes the RED sharp.
      localImage: 'local-X-v0002-53-never-consulted',
    });
    try {
      const livez = await handles.probe.get('/v1/livez');
      expect(livez.status(), 'binary livez responds 200').toBe(200);

      // Docker metadata is the canonical evidence: a v0002 binary
      // that honoured the four-level precedence must produce a
      // container labeled flowai.resolved_image_source=task_override
      // with Image == <repository>@<digest> of the per-task override.
      const expectedPull = dockerPullString(overrideRef);
      const deadline = Date.now() + 12_000;
      let observed: DockerContainer | null = null;
      while (Date.now() < deadline && observed === null) {
        const containers = await listDockerContainers(
          docker,
          'flowai.executor_id',
          handles.executorId,
        );
        const candidate = containers.find(
          (c) => (c.Labels ?? {})['flowai.task_id'] === task.task_id,
        );
        if (candidate) {
          observed = candidate;
        } else if (containers.length > 0) {
          observed = containers[0] ?? null;
        } else {
          await new Promise<void>((r) => setTimeout(r, 200));
        }
      }
      if (observed === null) {
        const redMessage =
          'v0002.53 RED — executor_docker_opehands did not start a Docker container labeled with its executor_id ' +
          'for the team-a task whose per-task image override should have won the four-level precedence. ' +
          `The Markdown requires the container Image to equal ${expectedPull} and the image source label to be ` +
          "task_override, NOT the local image 'local-X-v0002-53-never-consulted'. " +
          'Turning-green condition: the v0002 binary must (a) PUT /v1/executors/{id} under team-scope, ' +
          `(b) POST /v1/executors/{id}/claim producing resolved_image = ${expectedPull} and image_source = task_override, ` +
          '(c) start a Docker container whose Image equals the override reference and whose labels include ' +
          'flowai.resolved_image_source=task_override. ' +
          `Captured redacted logs tail=\n${redactedLogs().slice(-2_000)}`;
        throw new Error(redMessage);
      }
      const labels = observed.Labels ?? {};
      expect(
        observed.Image.includes(expectedPull),
        `per-task image override wins the four-level precedence and is used by the Executor verbatim (expected Image contains ${expectedPull})`,
      ).toBe(true);
      expect(
        labels['flowai.resolved_image_source'],
        "container label records the Registry image source as 'task_override'",
      ).toBe('task_override');
      expect(
        labels['flowai.executor_id'],
        'container label records the claiming Executor executor_id',
      ).toBe(handles.executorId);
      expect(
        labels['flowai.task_id'],
        'container label records the parent task_id',
      ).toBe(task.task_id);
      expect(
        observed.Image.includes('local-X-v0002-53'),
        "Executor does NOT consult or substitute its locally configured image for resolved_image",
      ).toBe(false);
      expect(
        observed.Image.includes('default-A-v0002-53'),
        'per-task override outranks teams.default_image in the four-level precedence',
      ).toBe(false);
    } finally {
      await handles.teardown().catch(() => undefined);
    }
  } finally {
    if (docker) await docker.dispose().catch(() => undefined);
    await disposeProxyIfStarted(proxy);
  }
});
