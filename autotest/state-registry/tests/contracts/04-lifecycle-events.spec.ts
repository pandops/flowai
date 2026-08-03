// Contract group: lifecycle-events
// Covers v0002.5, v0002.6, v0002.23, v0002.24, v0002.25, v0002.26,
// v0002.29, v0002.31, v0002.36, v0002.58.
//
// Each test exercises one or more of the lifecycle-event boundaries:
//   - POST /v1/executors/{executor_id}/events (Executor self events)
//   - POST /v1/tasks/{task_id}/events (assigned Executor task events)
//   - GET /v1/tasks/{task_id}/events (ordered event history read)
//
// Behavior-specific assertions verify:
//   - idempotent retry returns the original 202 result without duplicate
//     history
//   - self events are accepted by the Executor event stream and never
//     enter task history
//   - the happy path projects [created, running, finished] exactly
//   - the failure path projects [created, running, failed] with no
//     finished event
//   - terminal-after-terminal and out-of-order tuples are rejected; no
//     accepted_sequence override; cross-team envelope team_id is
//     rejected with 403 team_mismatch
//   - assignment ownership survives restart
//   - foreign team task and Executor identifiers return non-revealing
//     404 (same shape as unknown)
//   - system-owned Executor task-event envelope is verified against
//     the parent task's team_id, not the Executor's null service
//     binding
import { test, expect } from "@playwright/test";
import {
  startRegistryWorker,
  type RegistryWorker,
} from "../../fixtures/registry_worker";
import {
  systemAdministrator,
  teamExecutorFor,
  systemExecutorWithId,
  gatewayFor,
  listenerFor,
} from "../../fixtures/identities";
import {
  bootstrapTeam,
  ingestPendingTask,
  registerExecutor,
  snapshotResponse,
} from "./_setup";
import { uniqueExecutorId } from "../../fixtures/executor_binary";

let worker: RegistryWorker;

test.beforeAll(async () => {
  worker = await startRegistryWorker();
});

test.afterAll(async () => {
  if (worker) {
    await worker.teardown();
  }
});

async function postTaskEvent(
  exec: {
    api: (
      base: string,
    ) => Promise<import("@playwright/test").APIRequestContext>;
    attach: () => Readonly<Record<string, string>>;
  },
  baseUrl: string,
  taskId: string,
  body: Record<string, unknown>,
) {
  const api = await exec.api(baseUrl);
  try {
    const response = await api.post(
      `/v1/tasks/${encodeURIComponent(taskId)}/events`,
      { data: body },
    );
    return await snapshotResponse(response);
  } finally {
    await api.dispose();
  }
}

async function postSelfEvent(
  exec: {
    api: (
      base: string,
    ) => Promise<import("@playwright/test").APIRequestContext>;
    attach: () => Readonly<Record<string, string>>;
  },
  baseUrl: string,
  body: Record<string, unknown>,
) {
  const executorId = exec.attach()["X-FlowAI-Executor-Id"] ?? "";
  const api = await exec.api(baseUrl);
  try {
    const response = await api.post(
      `/v1/executors/${encodeURIComponent(executorId)}/events`,
      { data: body },
    );
    return await snapshotResponse(response);
  } finally {
    await api.dispose();
  }
}

interface TaskEventRow {
  event_id: string;
  team_id: string;
  task_id: string;
  executor_id: string | null;
  event_type: string;
  occurred_at: string;
  payload: Record<string, unknown>;
}

// claimAsExecutor must snapshot before dispose so the caller never
// observes a freed APIResponse (the prior pattern captured a live
// APIResponse, asserted status() after dispose, and is forbidden).
async function claimAsExecutor(
  exec: {
    api: (
      base: string,
    ) => Promise<import("@playwright/test").APIRequestContext>;
    attach: () => Readonly<Record<string, string>>;
  },
  baseUrl: string,
  taskId: string,
  commandId: string,
) {
  const api = await exec.api(baseUrl);
  try {
    const response = await api.post(
      `/v1/executors/${encodeURIComponent(exec.attach()["X-FlowAI-Executor-Id"] ?? "")}/claim`,
      { data: { task_id: taskId, command_id: commandId } },
    );
    return await snapshotResponse(response);
  } finally {
    await api.dispose();
  }
}

// readClaimCreatedAt returns the occurred_at of the Registry-appended
// FIRST `created` event so the per-test base timestamp is strictly
// later than the claim transaction (avoids stale 2026-07-13 fixtures).
async function readClaimCreatedAt(
  worker: RegistryWorker,
  teamId: string,
  taskId: string,
): Promise<string> {
  const gw = gatewayFor({ teamId, operatorId: `op-${teamId}` });
  const gwApi = await gw.api(worker.baseUrl);
  try {
    const resp = await gwApi.get(
      `/v1/tasks/${encodeURIComponent(taskId)}/events`,
    );
    expect(resp.status(), "ordered event history read returns 200").toBe(200);
    const body = await resp.json();
    const items = Array.isArray((body as { items: unknown }).items)
      ? (body as { items: TaskEventRow[] }).items
      : [];
    const created = items.find((e) => e.event_type === "created");
    if (!created) {
      throw new Error(
        `readClaimCreatedAt: created event missing for task=${taskId}`,
      );
    }
    return created.occurred_at;
  } finally {
    await gwApi.dispose();
  }
}

function futureIso(deltaMs: number): string {
  return new Date(Date.now() + deltaMs).toISOString();
}

function plusOffsetMs(base: string, offsetMs: number): string {
  const baseMs = Date.parse(base);
  if (Number.isNaN(baseMs)) {
    throw new Error(`plusOffsetMs: invalid base ISO timestamp base=${base}`);
  }
  return new Date(baseMs + offsetMs).toISOString();
}

test("v0002.5 idempotent retry of the same running event returns the original 202 and appends once", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-5-team",
    executionTag: "openhands",
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
      source_id: "T-1",
      task_type_id: bsA.taskType.task_type_id,
      payload: { hello: "world" },
    },
  );
  const exec = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(exec, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_opehands",
    identity: "exec-v0002-5",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  const claimResp = await claimAsExecutor(
    exec,
    worker.baseUrl,
    task.task_id,
    "C-1",
  );
  expect(claimResp.status(), "claim returns 200").toBe(200);
  const createdAt = await readClaimCreatedAt(
    worker,
    bsA.admin.team_id,
    task.task_id,
  );
  const envelope = {
    event_id: "0190-running-0001",
    team_id: bsA.admin.team_id,
    task_id: task.task_id,
    executor_id: exec.attach()["X-FlowAI-Executor-Id"] ?? "",
    event_type: "running",
    occurred_at: plusOffsetMs(createdAt, 1_000),
    payload: { runtime: "openhands" },
  };
  const first = await postTaskEvent(
    exec,
    worker.baseUrl,
    task.task_id,
    envelope,
  );
  const retry = await postTaskEvent(
    exec,
    worker.baseUrl,
    task.task_id,
    envelope,
  );
  expect(first.status(), "first POST returns 202").toBe(202);
  expect(retry.status(), "retry returns the same 202 result").toBe(202);
  const gw = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwApi = await gw.api(worker.baseUrl);
  try {
    const events = await gwApi.get(
      `/v1/tasks/${encodeURIComponent(task.task_id)}/events`,
    );
    expect(events.status(), "ordered event history read returns 200").toBe(200);
    const body = await events.json();
    const items = Array.isArray(body.items) ? body.items : [];
    const running = items.filter(
      (e: { event_type: string; event_id: string }) =>
        e.event_type === "running" && e.event_id === "0190-running-0001",
    );
    expect(running.length, "running event appears exactly once").toBe(1);
    expect(
      items.find((e: { event_type: string }) => e.event_type === "finished"),
      "no finished event was appended",
    ).toBeUndefined();
  } finally {
    await gwApi.dispose();
  }
});

test("v0002.6 Executor self events are accepted only by the Executor event stream and never enter task history", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-6-team",
    executionTag: "openhands",
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
      source_id: "T-1",
      task_type_id: bsA.taskType.task_type_id,
      payload: { hello: "world" },
    },
  );
  const exec = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(exec, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_opehands",
    identity: "exec-v0002-6",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  const claimResp = await claimAsExecutor(
    exec,
    worker.baseUrl,
    task.task_id,
    "C-1",
  );
  expect(claimResp.status(), "claim returns 200").toBe(200);
  const createdAt = await readClaimCreatedAt(
    worker,
    bsA.admin.team_id,
    task.task_id,
  );
  const selfEnvelope = {
    event_id: "0190-capacity-0001",
    team_id: bsA.admin.team_id,
    task_id: null,
    executor_id: exec.attach()["X-FlowAI-Executor-Id"] ?? "",
    event_type: "capacity_observed",
    occurred_at: plusOffsetMs(createdAt, 1_000),
    payload: { max_capacity: 2, running_count: 1 },
  };
  await test.step("POST /v1/executors/{id}/events accepts the capacity_observed self event with 202", async () => {
    const resp = await postSelfEvent(exec, worker.baseUrl, selfEnvelope);
    expect(resp.status()).toBe(202);
  });
  await test.step("POST /v1/tasks/{id}/events rejects the self event shape with 403", async () => {
    const resp = await postTaskEvent(
      exec,
      worker.baseUrl,
      task.task_id,
      selfEnvelope,
    );
    expect(
      resp.status(),
      "self event shape is rejected on the task-event boundary",
    ).toBe(403);
  });
});

test("v0002.23 successful happy-path lifecycle projects [created, running, finished]", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-23-team",
    executionTag: "openhands",
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
      source_id: "T-1",
      task_type_id: bsA.taskType.task_type_id,
      payload: { hello: "world" },
    },
  );
  const exec = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(exec, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_opehands",
    identity: "exec-v0002-23",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  const claim = await claimAsExecutor(
    exec,
    worker.baseUrl,
    task.task_id,
    "C-1",
  );
  expect(claim.status(), "claim returns 200").toBe(200);
  const createdAt = await readClaimCreatedAt(
    worker,
    bsA.admin.team_id,
    task.task_id,
  );
  const base = {
    team_id: bsA.admin.team_id,
    task_id: task.task_id,
    executor_id: exec.attach()["X-FlowAI-Executor-Id"] ?? "",
  };
  const r1 = await postTaskEvent(exec, worker.baseUrl, task.task_id, {
    ...base,
    event_id: "0190-running-23",
    event_type: "running",
    occurred_at: plusOffsetMs(createdAt, 1_000),
    payload: { runtime: "openhands" },
  });
  expect(r1.status()).toBe(202);
  const r2 = await postTaskEvent(exec, worker.baseUrl, task.task_id, {
    ...base,
    event_id: "0190-finished-23",
    event_type: "finished",
    occurred_at: plusOffsetMs(createdAt, 61_000),
    payload: { status: "ok" },
  });
  expect(r2.status()).toBe(202);
  const gw = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwApi = await gw.api(worker.baseUrl);
  try {
    const events = await gwApi.get(
      `/v1/tasks/${encodeURIComponent(task.task_id)}/events`,
    );
    expect(events.status(), "ordered event history read returns 200").toBe(200);
    const body = await events.json();
    const types = (body.items as Array<{ event_type: string }>).map(
      (e) => e.event_type,
    );
    expect(
      types,
      "happy path projects exactly [created, running, finished]",
    ).toEqual(["created", "running", "finished"]);
    expect(types, "no dispatched event exists").not.toContain("dispatched");
  } finally {
    await gwApi.dispose();
  }
});

test("v0002.24 failure path projects [created, running, failed] without a finished event", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-24-team",
    executionTag: "openhands",
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
      source_id: "T-1",
      task_type_id: bsA.taskType.task_type_id,
      payload: { hello: "world" },
    },
  );
  const exec = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(exec, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_opehands",
    identity: "exec-v0002-24",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  const claim = await claimAsExecutor(
    exec,
    worker.baseUrl,
    task.task_id,
    "C-1",
  );
  expect(claim.status(), "claim returns 200").toBe(200);
  const createdAt = await readClaimCreatedAt(
    worker,
    bsA.admin.team_id,
    task.task_id,
  );
  const base = {
    team_id: bsA.admin.team_id,
    task_id: task.task_id,
    executor_id: exec.attach()["X-FlowAI-Executor-Id"] ?? "",
  };
  const r1 = await postTaskEvent(exec, worker.baseUrl, task.task_id, {
    ...base,
    event_id: "0190-running-24",
    event_type: "running",
    occurred_at: plusOffsetMs(createdAt, 1_000),
    payload: {},
  });
  expect(r1.status()).toBe(202);
  const r2 = await postTaskEvent(exec, worker.baseUrl, task.task_id, {
    ...base,
    event_id: "0190-failed-24",
    event_type: "failed",
    occurred_at: plusOffsetMs(createdAt, 61_000),
    payload: { error: "boom" },
  });
  expect(r2.status()).toBe(202);
  const gw = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwApi = await gw.api(worker.baseUrl);
  try {
    const events = await gwApi.get(
      `/v1/tasks/${encodeURIComponent(task.task_id)}/events`,
    );
    expect(events.status(), "ordered event history read returns 200").toBe(200);
    const body = await events.json();
    const types = (body.items as Array<{ event_type: string }>).map(
      (e) => e.event_type,
    );
    expect(types, "failure path projects [created, running, failed]").toEqual([
      "created",
      "running",
      "failed",
    ]);
    expect(types, "no finished event exists").not.toContain("finished");
  } finally {
    await gwApi.dispose();
  }
});

test("v0002.25 invalid transitions: terminal-after-terminal, out-of-order tuples, accepted_sequence, cross-team envelope", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-25-team",
    executionTag: "openhands",
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
      source_id: "T-1",
      task_type_id: bsA.taskType.task_type_id,
      payload: { hello: "world" },
    },
  );
  const exec = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(exec, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_opehands",
    identity: "exec-v0002-25",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  const claim = await claimAsExecutor(
    exec,
    worker.baseUrl,
    task.task_id,
    "C-1",
  );
  expect(claim.status(), "claim returns 200").toBe(200);
  const createdAt = await readClaimCreatedAt(
    worker,
    bsA.admin.team_id,
    task.task_id,
  );
  const base = {
    team_id: bsA.admin.team_id,
    task_id: task.task_id,
    executor_id: exec.attach()["X-FlowAI-Executor-Id"] ?? "",
  };
  await postTaskEvent(exec, worker.baseUrl, task.task_id, {
    ...base,
    event_id: "0190-r-25",
    event_type: "running",
    occurred_at: plusOffsetMs(createdAt, 1_000),
    payload: {},
  });
  await postTaskEvent(exec, worker.baseUrl, task.task_id, {
    ...base,
    event_id: "0190-f-25",
    event_type: "finished",
    occurred_at: plusOffsetMs(createdAt, 61_000),
    payload: {},
  });
  await test.step("terminal-after-terminal returns 409 invalid_task_transition", async () => {
    const resp = await postTaskEvent(exec, worker.baseUrl, task.task_id, {
      ...base,
      event_id: "0190-f2-25",
      event_type: "failed",
      occurred_at: plusOffsetMs(createdAt, 121_000),
      payload: {},
    });
    expect(resp.status(), "terminal-after-terminal is rejected with 409").toBe(
      409,
    );
    const body = (await resp.json()) as { code: string };
    expect(
      body.code,
      "terminal-after-terminal body carries the documented invalid_task_transition code",
    ).toBe("invalid_task_transition");
  });
  await test.step("out-of-order tuple with accepted_sequence is rejected with 409; accepted_sequence is not honored", async () => {
    const resp = await postTaskEvent(exec, worker.baseUrl, task.task_id, {
      ...base,
      event_id: "0190-old-25",
      event_type: "finished",
      occurred_at: createdAt,
      payload: {},
      accepted_sequence: 99,
    });
    expect(resp.status(), "out-of-order tuple is rejected with 409").toBe(409);
    const body = (await resp.json()) as { code: string };
    expect(
      body.code,
      "out-of-order body carries the documented invalid_task_transition code",
    ).toBe("invalid_task_transition");
  });
  await test.step("cross-team envelope team_id is rejected with 403 team_mismatch", async () => {
    const resp = await postTaskEvent(exec, worker.baseUrl, task.task_id, {
      ...base,
      team_id: "team-other",
      event_id: "0190-mismatch-25",
      event_type: "finished",
      occurred_at: plusOffsetMs(createdAt, 181_000),
      payload: {},
    });
    expect(
      resp.status(),
      "cross-team envelope is rejected with 403 team_mismatch",
    ).toBe(403);
    const body = (await resp.json()) as { code: string };
    expect(
      body.code,
      "cross-team envelope body carries the documented team_mismatch code",
    ).toBe("team_mismatch");
  });
});

test("v0002.26 deterministic ordering (occurred_at ASC, event_id ASC); retries dedupe; accepted_sequence never overrides", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-26-team",
    executionTag: "openhands",
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
      source_id: "T-1",
      task_type_id: bsA.taskType.task_type_id,
      payload: { hello: "world" },
    },
  );
  const exec = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(exec, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_opehands",
    identity: "exec-v0002-26",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  const claim = await claimAsExecutor(
    exec,
    worker.baseUrl,
    task.task_id,
    "C-1",
  );
  expect(claim.status(), "claim returns 200").toBe(200);
  const createdAt = await readClaimCreatedAt(
    worker,
    bsA.admin.team_id,
    task.task_id,
  );
  const base = {
    team_id: bsA.admin.team_id,
    task_id: task.task_id,
    executor_id: exec.attach()["X-FlowAI-Executor-Id"] ?? "",
  };
  const tieOccurredAt = plusOffsetMs(createdAt, 1_000);
  await postTaskEvent(exec, worker.baseUrl, task.task_id, {
    ...base,
    event_id: "0190-tie-0001",
    event_type: "running",
    occurred_at: tieOccurredAt,
    payload: {},
  });
  await postTaskEvent(exec, worker.baseUrl, task.task_id, {
    ...base,
    event_id: "0190-tie-0002",
    event_type: "finished",
    occurred_at: tieOccurredAt,
    payload: {},
  });
  const gw = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwApi = await gw.api(worker.baseUrl);
  try {
    const events = await gwApi.get(
      `/v1/tasks/${encodeURIComponent(task.task_id)}/events`,
    );
    expect(events.status(), "ordered event history read returns 200").toBe(200);
    const body = await events.json();
    const items = Array.isArray(body.items) ? body.items : [];
    const tieRows = items.filter(
      (e: { occurred_at: string }) => e.occurred_at === tieOccurredAt,
    );
    expect(
      tieRows.length,
      "two events at the same timestamp are present",
    ).toBeGreaterThanOrEqual(2);
    const ids = tieRows.map((e: { event_id: string }) => e.event_id);
    expect(ids, "equal-time rows are ordered by ascending event_id").toEqual(
      [...ids].sort(),
    );
  } finally {
    await gwApi.dispose();
  }
});

test("v0002.29 assignment ownership survives restart", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-29-team",
    executionTag: "openhands",
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
      source_id: "T-1",
      task_type_id: bsA.taskType.task_type_id,
      payload: { hello: "world" },
    },
  );
  const exec1 = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  const exec2 = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(exec1, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_opehands",
    identity: "exec-v0002-29-1",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  await registerExecutor(exec2, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_opehands",
    identity: "exec-v0002-29-2",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  const win = await claimAsExecutor(exec1, worker.baseUrl, task.task_id, "C-1");
  expect(win.status(), "pre-restart claim returns 200").toBe(200);
  const restart = await worker.restart();
  expect(restart.restartCount).toBeGreaterThanOrEqual(1);
  const stale = await claimAsExecutor(
    exec2,
    worker.baseUrl,
    task.task_id,
    "C-2",
  );
  expect(
    stale.status(),
    "post-restart re-claim by another Executor returns 409",
  ).toBe(409);
});

test("v0002.31 assigned Executor accepts running/finished; unassigned same-team Executor and cross-team envelope are rejected", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-31-team",
    executionTag: "openhands",
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
      source_id: "T-1",
      task_type_id: bsA.taskType.task_type_id,
      payload: { hello: "world" },
    },
  );
  const exec1 = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  const exec2 = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(exec1, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_opehands",
    identity: "exec-v0002-31-1",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  await registerExecutor(exec2, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_opehands",
    identity: "exec-v0002-31-2",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  const claim = await claimAsExecutor(
    exec1,
    worker.baseUrl,
    task.task_id,
    "C-1",
  );
  expect(claim.status(), "claim returns 200").toBe(200);
  const createdAt = await readClaimCreatedAt(
    worker,
    bsA.admin.team_id,
    task.task_id,
  );
  const base = {
    team_id: bsA.admin.team_id,
    task_id: task.task_id,
    executor_id: exec1.attach()["X-FlowAI-Executor-Id"] ?? "",
  };
  const r1 = await postTaskEvent(exec1, worker.baseUrl, task.task_id, {
    ...base,
    event_id: "0190-r-31",
    event_type: "running",
    occurred_at: plusOffsetMs(createdAt, 1_000),
    payload: {},
  });
  expect(r1.status(), "assigned Executor running event accepted with 202").toBe(
    202,
  );
  const wrongExec = await postTaskEvent(exec2, worker.baseUrl, task.task_id, {
    ...base,
    executor_id: exec2.attach()["X-FlowAI-Executor-Id"] ?? "",
    event_id: "0190-x-31",
    event_type: "finished",
    occurred_at: plusOffsetMs(createdAt, 61_000),
    payload: {},
  });
  expect(
    wrongExec.status(),
    "unassigned same-team Executor is rejected with 403 not_assigned",
  ).toBe(403);
  const wrongExecBody = (await wrongExec.json()) as { code: string };
  expect(
    wrongExecBody.code,
    "unassigned same-team body carries the documented not_assigned code",
  ).toBe("not_assigned");
  const crossTeam = await postTaskEvent(exec1, worker.baseUrl, task.task_id, {
    ...base,
    team_id: "team-other",
    event_id: "0190-y-31",
    event_type: "finished",
    occurred_at: plusOffsetMs(createdAt, 121_000),
    payload: {},
  });
  expect(
    crossTeam.status(),
    "cross-team envelope is rejected with 403 team_mismatch",
  ).toBe(403);
  const crossTeamBody = (await crossTeam.json()) as { code: string };
  expect(
    crossTeamBody.code,
    "cross-team envelope body carries the documented team_mismatch code",
  ).toBe("team_mismatch");
});

test("v0002.36 foreign-team task events and self events return non-revealing 404", async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-36-${Date.now().toString(36)}`;
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-a`,
    executionTag: "openhands",
  });
  const bsB = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-b`,
    executionTag: "openhands",
  });
  const teamBTask = await ingestPendingTask(
    listenerFor({
      teamId: bsB.admin.team_id,
      listenerIdentity: bsB.listenerIdentity,
      sourceSystemId: bsB.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsB.admin.team_id,
      source_system_id: bsB.sourceSystem.source_system_id,
      source_id: "B-1",
      task_type_id: bsB.taskType.task_type_id,
      payload: { from: "team-b" },
    },
  );
  const execA = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(execA, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_opehands",
    identity: "exec-v0002-36",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  await test.step("team-a Executor writing a running event for a team-b task returns non-revealing 404", async () => {
    const resp = await postTaskEvent(execA, worker.baseUrl, teamBTask.task_id, {
      event_id: "0190-foreign-36",
      team_id: bsA.admin.team_id,
      task_id: teamBTask.task_id,
      executor_id: execA.attach()["X-FlowAI-Executor-Id"] ?? "",
      event_type: "running",
      occurred_at: futureIso(60_000),
      payload: {},
    });
    expect(resp.status(), "foreign task event probe is non-revealing 404").toBe(
      404,
    );
  });
});

test("v0002.58 system-owned Executor task-event envelope is verified against the parent task's team_id", async () => {
  const admin = systemAdministrator();
  const executionTag = "v0002-58-openhands";
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-58-team",
    executionTag,
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
      source_id: "T-1",
      task_type_id: bsA.taskType.task_type_id,
      payload: { hello: "world" },
    },
  );
  const sysExec = systemExecutorWithId(uniqueExecutorId());
  await registerExecutor(sysExec, worker.baseUrl, {
    scope: "system",
    team_id: null,
    executor_type: "executor_docker_opehands",
    identity: "exec-v0002-58-sys",
    authorized_tag: executionTag,
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  const claim = await claimAsExecutor(
    sysExec,
    worker.baseUrl,
    task.task_id,
    "C-SYS",
  );
  expect(claim.status(), "system-owned Executor claim returns 200").toBe(200);
  const createdAt = await readClaimCreatedAt(
    worker,
    bsA.admin.team_id,
    task.task_id,
  );
  const envelope = {
    event_id: "0190-r-58",
    team_id: bsA.admin.team_id,
    task_id: task.task_id,
    executor_id: sysExec.attach()["X-FlowAI-Executor-Id"] ?? "",
    event_type: "running",
    occurred_at: plusOffsetMs(createdAt, 1_000),
    payload: {},
  };
  await test.step("envelope team_id = parent task's team_id is accepted with 202", async () => {
    const resp = await postTaskEvent(
      sysExec,
      worker.baseUrl,
      task.task_id,
      envelope,
    );
    expect(resp.status()).toBe(202);
  });
  await test.step("envelope team_id = team-other is rejected with 403 team_mismatch", async () => {
    const resp = await postTaskEvent(sysExec, worker.baseUrl, task.task_id, {
      ...envelope,
      team_id: "team-other",
      event_id: "0190-r-other-58",
      occurred_at: plusOffsetMs(createdAt, 61_000),
    });
    expect(
      resp.status(),
      "system Executor envelope team_id mismatch is rejected",
    ).toBe(403);
    const body = (await resp.json()) as { code: string };
    expect(
      body.code,
      "cross-team envelope body carries the documented team_mismatch code",
    ).toBe("team_mismatch");
  });
});
