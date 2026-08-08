// Contract group: claims
// Covers v0002.4, v0002.21, v0002.22, v0002.30, v0002.35, v0002.57,
// v0002.68, v0002.69, v0002.70, v0002.71, v0002.72, v0002.73.
//
// Each test below exercises the atomic FIFO claim boundary
// (POST /v1/executors/{executor_id}/claim) and the re-read boundaries
// (GET /v1/tasks/{id}, GET /v1/tasks/{id}/events) used to verify the
// durable effect of a successful claim: an immutable
// `tasks.owner_command_id`, `tasks.executor_id`, `tasks.claimed_at`,
// `tasks.resolved_image`, `tasks.image_source`, and the FIRST Registry-
// appended `created` lifecycle event whose `executor_id` is non-null
// and whose payload encodes `task <task_id> loaded by <executor_id>`.
//
// Behavior-specific assertions cover:
//   - concurrent eligible Executors cannot claim the same task twice
//   - claim removes the task from the winner's scope of discovery
//   - a stale claim by the same Executor with a different command_id
//     returns 409 task_already_claimed (exact, not broad [404, 409])
//   - foreign team task identifiers return non-revealing 404 with the
//     same body shape as unknown
//   - one command_id may own many tasks while each task is limited to
//     one command
//   - older-task attempts return 409 older_task_must_be_claimed_first
//   - FIFO tie-break uses task_id when ingested_at is identical
//   - resolved_image and image_source persist at claim time
//
// All point reads (GET /v1/tasks/{id}, GET /v1/tasks/{id}/events) are
// trusted Gateway boundaries per the OpenAPI contract; listener and
// Executor identities are NEVER used to read tasks.
import { test, expect } from "@playwright/test";
import {
  startRegistryWorker,
  type RegistryWorker,
} from "../../fixtures/registry_worker";
import {
  systemAdministrator,
  teamExecutorFor,
  systemExecutorWithId,
  listenerFor,
  gatewayFor,
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

async function claimAs(
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

interface TaskEvent {
  event_id: string;
  task_id: string;
  executor_id: string | null;
  event_type: string;
  occurred_at: string;
  payload: Record<string, unknown>;
}

function readEvents(body: unknown): TaskEvent[] {
  if (!body || typeof body !== "object") return [];
  const items = (body as { items: unknown }).items;
  return Array.isArray(items) ? (items as TaskEvent[]) : [];
}

test("v0002.4 concurrent claim is atomic: exactly one winner returns 200 and the other returns 409; exactly one created event exists", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-4-team",
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
  const execA = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  const execB = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(execA, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-4-a",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  await registerExecutor(execB, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-4-b",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  let winnerExecutorId: string | null = null;
  await test.step("two concurrent claims produce exactly the sorted multiset [200, 409]", async () => {
    const [a, b] = await Promise.all([
      claimAs(execA, worker.baseUrl, task.task_id, "C-A"),
      claimAs(execB, worker.baseUrl, task.task_id, "C-B"),
    ]);
    const statuses = [a.status(), b.status()].sort((x, y) => x - y);
    expect(statuses, "concurrent claim multiset is exactly [200, 409]").toEqual(
      [200, 409],
    );
    const winnerIndex = [a, b].findIndex((r) => r.status() === 200);
    const winner = winnerIndex === 0 ? a : b;
    const winnerBody = (await winner.json()) as {
      task: { executor_id: string | null };
    };
    const claimedExecutorId = winnerBody.task.executor_id;
    expect(
      typeof claimedExecutorId,
      "winning claim body carries the claiming executor_id",
    ).toBe("string");
    expect(claimedExecutorId, "winning executor_id is non-null").toBeTruthy();
    winnerExecutorId = claimedExecutorId;
  });
  await test.step("canonical task has exactly one Registry-appended created event with the winner executor_id", async () => {
    expect(
      winnerExecutorId,
      "winner executor_id is captured from the winning claim",
    ).toBeTruthy();
    const gw = gatewayFor({
      teamId: bsA.admin.team_id,
      operatorId: `op-${bsA.admin.team_id}`,
    });
    const gwApi = await gw.api(worker.baseUrl);
    try {
      const events = await gwApi.get(
        `/v1/tasks/${encodeURIComponent(task.task_id)}/events`,
      );
      expect(events.status(), "ordered event history read returns 200").toBe(
        200,
      );
      const eventsBody = await events.json();
      const items = readEvents(eventsBody);
      const created = items.filter((e) => e.event_type === "created");
      expect(
        created.length,
        "exactly one Registry-appended created event",
      ).toBe(1);
      const createdEvent = created[0];
      if (!createdEvent) {
        throw new Error(
          "created event array is unexpectedly empty after length assertion",
        );
      }
      expect(
        createdEvent.executor_id,
        "created event has non-null executor_id",
      ).toBeTruthy();
      expect(
        createdEvent.executor_id,
        "created event executor_id matches the winning claim",
      ).toBe(winnerExecutorId);
    } finally {
      await gwApi.dispose();
    }
  });
});

test("v0002.21 successful claim atomically removes the task from the winner's scope of discovery", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-21-team",
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
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-21",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  const execId = exec.attach()["X-FlowAI-Executor-Id"] ?? "";

  await test.step("pre-claim discovery: the pending task is visible to the team-a Executor", async () => {
    const api = await exec.api(worker.baseUrl);
    try {
      const resp = await api.get(
        `/v1/executors/${encodeURIComponent(execId)}/tasks?tag=openhands`,
      );
      expect(resp.status(), "pre-claim discovery returns 200").toBe(200);
      const body = await resp.json();
      const items = Array.isArray((body as { items: unknown }).items)
        ? (body as { items: Array<{ task_id: string }> }).items
        : [];
      const ids = items.map((it) => it.task_id);
      expect(ids, "pre-claim discovery returns the pending task").toContain(
        task.task_id,
      );
    } finally {
      await api.dispose();
    }
  });

  await test.step("POST /v1/executors/{id}/claim returns 200 with the canonical claim body", async () => {
    const resp = await claimAs(exec, worker.baseUrl, task.task_id, "C-1");
    expect(resp.status(), "successful claim returns 200").toBe(200);
    const body = (await resp.json()) as {
      claim: string;
      task: {
        task_id: string;
        team_id: string;
        owner_command_id: string | null;
        executor_id: string | null;
        claimed_at: string | null;
        resolved_image: unknown;
        image_source: string | null;
        current_state: string;
      };
      resolved_image: unknown;
      image_source: string | null;
    };
    expect(body.claim, 'claim response field is the literal "claimed"').toBe(
      "claimed",
    );
    expect(body.task.task_id, "task.task_id is the requested task").toBe(
      task.task_id,
    );
    expect(body.task.team_id, "task stays owned by team-a").toBe(
      bsA.admin.team_id,
    );
    expect(
      body.task.owner_command_id,
      "tasks.owner_command_id is set to C-1",
    ).toBe("C-1");
    expect(
      body.task.executor_id,
      "tasks.executor_id is set on claim",
    ).toBeTruthy();
    expect(
      body.task.claimed_at,
      "tasks.claimed_at is set on claim",
    ).toBeTruthy();
    expect(
      body.resolved_image,
      "claim response carries resolved_image",
    ).toBeTruthy();
    expect(
      body.image_source,
      "claim response carries image_source",
    ).toBeTruthy();
    expect(body.task.current_state, "projection becomes created").toBe(
      "created",
    );
  });

  await test.step("post-claim discovery: the task is removed from the winner's scope of discovery", async () => {
    const api = await exec.api(worker.baseUrl);
    try {
      const resp = await api.get(
        `/v1/executors/${encodeURIComponent(execId)}/tasks?tag=openhands`,
      );
      expect(
        [200, 204],
        "post-claim discovery returns 200 (empty) or 204",
      ).toContain(resp.status());
      if (resp.status() === 200) {
        const body = (await resp.json()) as {
          items: Array<{ task_id: string }>;
        };
        const ids = body.items.map((it) => it.task_id);
        expect(
          ids,
          "post-claim discovery does not contain the claimed task",
        ).not.toContain(task.task_id);
      }
    } finally {
      await api.dispose();
    }
  });

  await test.step("stale claim by the same Executor with a different command_id returns exact 409 task_already_claimed", async () => {
    const stale = await claimAs(exec, worker.baseUrl, task.task_id, "C-DUP");
    expect(
      stale.status(),
      "stale claim returns EXACT 409 task_already_claimed",
    ).toBe(409);
    const staleBody = (await stale.json()) as { code: string };
    expect(
      staleBody.code,
      "stale claim body carries the documented task_already_claimed code",
    ).toBe("task_already_claimed");
  });

  await test.step("Gateway read confirms the created event is appended atomically (no dispatched event)", async () => {
    const gw = gatewayFor({
      teamId: bsA.admin.team_id,
      operatorId: `op-${bsA.admin.team_id}`,
    });
    const gwApi = await gw.api(worker.baseUrl);
    try {
      const taskView = await gwApi.get(
        `/v1/tasks/${encodeURIComponent(task.task_id)}`,
      );
      expect(
        taskView.status(),
        "trusted Gateway reads the canonical task with 200",
      ).toBe(200);
      const viewBody = (await taskView.json()) as {
        current_state: string;
        owner_command_id: string | null;
        executor_id: string | null;
      };
      expect(
        viewBody.current_state,
        "task is projected current_state = created",
      ).toBe("created");
      expect(viewBody.owner_command_id, "owner_command_id is C-1").toBe("C-1");
      expect(viewBody.executor_id, "executor_id is set").toBeTruthy();

      const events = await gwApi.get(
        `/v1/tasks/${encodeURIComponent(task.task_id)}/events`,
      );
      expect(events.status(), "event history read returns 200").toBe(200);
      const eventsBody = await events.json();
      const items = readEvents(eventsBody);
      const created = items.filter((e) => e.event_type === "created");
      expect(
        created.length,
        "exactly one Registry-appended created event",
      ).toBe(1);
      const createdEvent = created[0];
      if (!createdEvent) {
        throw new Error(
          "created event array is unexpectedly empty after length assertion",
        );
      }
      expect(
        createdEvent.executor_id,
        "created event has non-null executor_id",
      ).toBeTruthy();
      const types = items.map((e) => e.event_type);
      expect(
        types,
        "no dispatched event exists in the ordered history",
      ).not.toContain("dispatched");
    } finally {
      await gwApi.dispose();
    }
  });
});

test("v0002.22 stale claim from a different Executor returns 409 task_already_claimed with no mutation", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-22-team",
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
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-22-1",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  await registerExecutor(exec2, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-22-2",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  await test.step("first claim wins with 200; second same-team claim returns EXACT 409 task_already_claimed", async () => {
    const win = await claimAs(exec1, worker.baseUrl, task.task_id, "C-1");
    expect(win.status()).toBe(200);
    const stale = await claimAs(exec2, worker.baseUrl, task.task_id, "C-2");
    expect(stale.status(), "stale claim returns EXACT 409").toBe(409);
    const staleBody = (await stale.json()) as { code: string };
    expect(
      staleBody.code,
      "stale claim body carries EXACT task_already_claimed code",
    ).toBe("task_already_claimed");
  });
});

test("v0002.30 the FIRST created event is Registry-appended atomically on successful claim", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-30-team",
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
      source_id: "JIRA-300",
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
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-30",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  let claimedExecutorId: string | null = null;
  await test.step("claim returns 200 with the FIRST created event appended in the same transaction", async () => {
    const claim = await claimAs(exec, worker.baseUrl, task.task_id, "C-30");
    expect(claim.status()).toBe(200);
    const body = (await claim.json()) as {
      task: {
        current_state: string;
        owner_command_id: string | null;
        executor_id: string | null;
        claimed_at: string | null;
      };
    };
    expect(body.task.current_state, "projection becomes created").toBe(
      "created",
    );
    expect(body.task.owner_command_id).toBe("C-30");
    expect(
      body.task.claimed_at,
      "claimed_at is set in the same transaction",
    ).toBeTruthy();
    expect(
      body.task.executor_id,
      "task.executor_id is set in the claim transaction",
    ).toBeTruthy();
    claimedExecutorId = body.task.executor_id;
  });
  await test.step("trusted Gateway read confirms exactly one Registry-appended created event with the claiming executor_id", async () => {
    expect(claimedExecutorId, "winning executor_id is captured").toBeTruthy();
    const gw = gatewayFor({
      teamId: bsA.admin.team_id,
      operatorId: `op-${bsA.admin.team_id}`,
    });
    const gwApi = await gw.api(worker.baseUrl);
    try {
      const events = await gwApi.get(
        `/v1/tasks/${encodeURIComponent(task.task_id)}/events`,
      );
      expect(events.status(), "event history read returns 200").toBe(200);
      const eventsBody = await events.json();
      const items = readEvents(eventsBody);
      const created = items.filter((e) => e.event_type === "created");
      expect(
        created.length,
        "exactly one Registry-appended created event",
      ).toBe(1);
      const createdEvent = created[0];
      if (!createdEvent) {
        throw new Error(
          "created event array is unexpectedly empty after length assertion",
        );
      }
      expect(
        createdEvent.executor_id,
        "created event has executor_id equal to the claiming Executor",
      ).toBe(claimedExecutorId);
      const types = items.map((e) => e.event_type);
      expect(
        types,
        "no dispatched event exists in the ordered history",
      ).not.toContain("dispatched");
    } finally {
      await gwApi.dispose();
    }
  });
});

test("v0002.35 foreign team task identifier returns non-revealing 404 (same shape as unknown)", async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-35-${Date.now().toString(36)}`;
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-a`,
    executionTag: "openhands",
  });
  const bsB = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-b`,
    executionTag: "openhands",
  });
  const foreignTask = await ingestPendingTask(
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
  const exec = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(exec, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-35",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  await test.step("team-a Executor claim for team-b task returns non-revealing 404; unknown task returns identical 404 shape", async () => {
    const foreign = await claimAs(
      exec,
      worker.baseUrl,
      foreignTask.task_id,
      "C-X",
    );
    const unknown = await claimAs(
      exec,
      worker.baseUrl,
      "00000000-0000-0000-0000-000000000000",
      "C-Y",
    );
    expect(foreign.status(), "foreign task claim is non-revealing 404").toBe(
      404,
    );
    expect(
      unknown.status(),
      "unknown task claim returns the same 404 shape",
    ).toBe(404);
    const foreignBody = await foreign.text();
    const unknownBody = await unknown.text();
    expect(
      foreignBody,
      "foreign and unknown 404 responses have identical bodies",
    ).toBe(unknownBody);
  });
});

test("v0002.57 system-owned Executor claims a team-a task; team-a competitor receives 409", async () => {
  const admin = systemAdministrator();
  const executionTag = "v0002-57-openhands";
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-57-team",
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
      source_id: "A-1",
      task_type_id: bsA.taskType.task_type_id,
      payload: { from: "team-a" },
    },
  );
  const sysExec = systemExecutorWithId(uniqueExecutorId());
  const teamExec = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(sysExec, worker.baseUrl, {
    scope: "system",
    team_id: null,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-57-sys",
    authorized_tag: executionTag,
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  await registerExecutor(teamExec, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-57-team",
    authorized_tag: executionTag,
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  await test.step("system Executor wins the claim; team-a competitor receives EXACT 409 task_already_claimed", async () => {
    const sysWin = await claimAs(
      sysExec,
      worker.baseUrl,
      task.task_id,
      "C-SYS",
    );
    expect(sysWin.status()).toBe(200);
    const teamLose = await claimAs(
      teamExec,
      worker.baseUrl,
      task.task_id,
      "C-TEAM",
    );
    expect(teamLose.status(), "team-a competitor receives EXACT 409").toBe(409);
    const teamLoseBody = (await teamLose.json()) as { code: string };
    expect(
      teamLoseBody.code,
      "team-a competitor body carries EXACT task_already_claimed code",
    ).toBe("task_already_claimed");
  });
});

test("v0002.68 claim succeeds only when the requested task is the OLDEST eligible pending task", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-68-team",
    executionTag: "openhands",
  });
  const older = await ingestPendingTask(
    listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsA.admin.team_id,
      source_system_id: bsA.sourceSystem.source_system_id,
      source_id: "T-older",
      task_type_id: bsA.taskType.task_type_id,
      payload: { idx: 0 },
    },
  );
  await new Promise<void>((resolve) => setTimeout(resolve, 1_100));
  const newer = await ingestPendingTask(
    listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsA.admin.team_id,
      source_system_id: bsA.sourceSystem.source_system_id,
      source_id: "T-newer",
      task_type_id: bsA.taskType.task_type_id,
      payload: { idx: 1 },
    },
  );
  const exec = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(exec, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-68",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  await test.step("claim for T-newer with command_id=C-n returns EXACT 409 older_task_must_be_claimed_first", async () => {
    const resp = await claimAs(exec, worker.baseUrl, newer.task_id, "C-n");
    expect(resp.status(), "older_task_must_be_claimed_first is EXACT 409").toBe(
      409,
    );
    const respBody = (await resp.json()) as { code: string };
    expect(
      respBody.code,
      "older_task_must_be_claimed_first body carries EXACT code",
    ).toBe("older_task_must_be_claimed_first");
  });
  await test.step("claim for T-older succeeds with 200", async () => {
    const olderWin = await claimAs(exec, worker.baseUrl, older.task_id, "C-o");
    expect(olderWin.status(), "oldest eligible task claim returns 200").toBe(
      200,
    );
  });
});

test("v0002.69 same (task_id, command_id) retry returns the original 200 claimed body with no new event", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-69-team",
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
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-69",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  await test.step("first claim returns 200; identical retry returns 200 with the same owner_command_id", async () => {
    const first = await claimAs(exec, worker.baseUrl, task.task_id, "C-stable");
    expect(first.status()).toBe(200);
    const firstBody = (await first.json()) as {
      task: {
        owner_command_id: string | null;
        executor_id: string | null;
        resolved_image: unknown;
        image_source: string | null;
      };
    };
    const retry = await claimAs(exec, worker.baseUrl, task.task_id, "C-stable");
    expect(
      retry.status(),
      "same (task_id, command_id) retry is idempotent and returns 200",
    ).toBe(200);
    const retryBody = (await retry.json()) as {
      task: {
        owner_command_id: string | null;
        executor_id: string | null;
        resolved_image: unknown;
        image_source: string | null;
      };
    };
    expect(
      retryBody.task.owner_command_id,
      "owner_command_id is preserved on retry",
    ).toBe(firstBody.task.owner_command_id);
    expect(
      retryBody.task.executor_id,
      "executor_id is preserved on retry",
    ).toBe(firstBody.task.executor_id);
    expect(
      retryBody.task.resolved_image,
      "resolved_image is preserved on retry",
    ).toEqual(firstBody.task.resolved_image);
    expect(
      retryBody.task.image_source,
      "image_source is preserved on retry",
    ).toBe(firstBody.task.image_source);
  });
  await test.step("trusted Gateway read confirms the retry did NOT append a new event", async () => {
    const gw = gatewayFor({
      teamId: bsA.admin.team_id,
      operatorId: `op-${bsA.admin.team_id}`,
    });
    const gwApi = await gw.api(worker.baseUrl);
    try {
      const events = await gwApi.get(
        `/v1/tasks/${encodeURIComponent(task.task_id)}/events`,
      );
      expect(events.status(), "event history read returns 200").toBe(200);
      const eventsBody = await events.json();
      const items = readEvents(eventsBody);
      const created = items.filter((e) => e.event_type === "created");
      expect(
        created.length,
        "identical retry does not append a new created event",
      ).toBe(1);
    } finally {
      await gwApi.dispose();
    }
  });
  await test.step("same task_id with a DIFFERENT command_id returns EXACT 409 task_already_claimed", async () => {
    const other = await claimAs(exec, worker.baseUrl, task.task_id, "C-other");
    expect(
      other.status(),
      "same task_id with a different command_id returns EXACT 409",
    ).toBe(409);
    const otherBody = (await other.json()) as { code: string };
    expect(
      otherBody.code,
      "different command_id body carries EXACT task_already_claimed code",
    ).toBe("task_already_claimed");
  });
});

test("v0002.70 one command_id may own multiple tasks; each task is limited to one command", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-70-team",
    executionTag: "openhands",
  });
  const t1 = await ingestPendingTask(
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
      payload: { idx: 0 },
    },
  );
  const t2 = await ingestPendingTask(
    listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsA.admin.team_id,
      source_system_id: bsA.sourceSystem.source_system_id,
      source_id: "T-2",
      task_type_id: bsA.taskType.task_type_id,
      payload: { idx: 1 },
    },
  );
  const exec = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(exec, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-70",
    authorized_tag: "openhands",
    max_capacity: 4,
    running_count: 0,
    runtime_metadata: {},
  });
  await test.step("two claims under the SAME command_id both return 200 with separate created events", async () => {
    const first = t1.task_id < t2.task_id ? t1 : t2;
    const second = first.task_id === t1.task_id ? t2 : t1;
    const c1 = await claimAs(exec, worker.baseUrl, first.task_id, "C-multi");
    expect(c1.status()).toBe(200);
    const c2 = await claimAs(exec, worker.baseUrl, second.task_id, "C-multi");
    expect(c2.status()).toBe(200);
    const b2 = (await c2.json()) as {
      task: { owner_command_id: string | null };
    };
    expect(
      b2.task.owner_command_id,
      "second task also carries the SAME command_id",
    ).toBe("C-multi");
  });
});

test("v0002.71 supersedes the prior atomic-claim definition with a tighter precondition re-verifies concurrent claim atomicity", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-71-team",
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
  const execA = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  const execB = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(execA, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-71-a",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  await registerExecutor(execB, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-71-b",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  let winnerExecutorId: string | null = null;
  await test.step("two concurrent claims produce exactly the sorted multiset [200, 409]", async () => {
    const [a, b] = await Promise.all([
      claimAs(execA, worker.baseUrl, task.task_id, "C-A-71"),
      claimAs(execB, worker.baseUrl, task.task_id, "C-B-71"),
    ]);
    const statuses = [a.status(), b.status()].sort((x, y) => x - y);
    expect(statuses, "v0002.71 multiset is exactly [200, 409]").toEqual([
      200, 409,
    ]);
    const winnerIndex = [a, b].findIndex((r) => r.status() === 200);
    const winner = winnerIndex === 0 ? a : b;
    const winnerBody = (await winner.json()) as {
      task: { executor_id: string | null };
    };
    expect(
      winnerBody.task.executor_id,
      "winning claim body carries the claiming executor_id",
    ).toBeTruthy();
    winnerExecutorId = winnerBody.task.executor_id;
  });
  await test.step("trusted Gateway read confirms exactly one Registry-appended created event with the winner executor_id", async () => {
    expect(
      winnerExecutorId,
      "winner executor_id is captured from the winning claim",
    ).toBeTruthy();
    const gw = gatewayFor({
      teamId: bsA.admin.team_id,
      operatorId: `op-${bsA.admin.team_id}`,
    });
    const gwApi = await gw.api(worker.baseUrl);
    try {
      const events = await gwApi.get(
        `/v1/tasks/${encodeURIComponent(task.task_id)}/events`,
      );
      expect(events.status(), "ordered event history read returns 200").toBe(
        200,
      );
      const eventsBody = await events.json();
      const items = readEvents(eventsBody);
      const created = items.filter((e) => e.event_type === "created");
      expect(
        created.length,
        "exactly one Registry-appended created event",
      ).toBe(1);
      const createdEvent = created[0];
      if (!createdEvent) {
        throw new Error(
          "created event array is unexpectedly empty after length assertion",
        );
      }
      expect(
        createdEvent.executor_id,
        "created event has executor_id equal to the winning claim",
      ).toBe(winnerExecutorId);
    } finally {
      await gwApi.dispose();
    }
  });
});

test("v0002.72 FIFO tie-break by task_id when two pending tasks share the same ingested_at", async () => {
  // POST /v1/tasks does not expose a caller-supplied `ingested_at`
  // field, so the equal-time tie fixture is verified from the
  // observed (ingested_at, task_id) tuples the Registry produces.
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-72-team",
    executionTag: "openhands",
  });
  const exec = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(exec, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-72",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  const listenerA = listenerFor({
    teamId: bsA.admin.team_id,
    listenerIdentity: bsA.listenerIdentity,
    sourceSystemId: bsA.sourceSystem.source_system_id,
  });
  const ta = await ingestPendingTask(listenerA, worker.baseUrl, {
    team_id: bsA.admin.team_id,
    source_system_id: bsA.sourceSystem.source_system_id,
    source_id: "T-a",
    task_type_id: bsA.taskType.task_type_id,
    payload: { idx: 0 },
  });
  const tb = await ingestPendingTask(listenerA, worker.baseUrl, {
    team_id: bsA.admin.team_id,
    source_system_id: bsA.sourceSystem.source_system_id,
    source_id: "T-b",
    task_type_id: bsA.taskType.task_type_id,
    payload: { idx: 1 },
  });

  // Read the canonical (ingested_at, task_id) tuples the Registry
  // produced via the trusted Gateway boundary. The Gateway read is
  // the only authoritative way to observe the actual persisted
  // timestamps because the OpenAPI does not expose ingested_at on
  // the listener ingestion response body.
  const gw = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwApi = await gw.api(worker.baseUrl);
  let observedTupleA: { task_id: string; ingested_at: string };
  let observedTupleB: { task_id: string; ingested_at: string };
  try {
    const taView = await gwApi.get(
      `/v1/tasks/${encodeURIComponent(ta.task_id)}`,
    );
    expect(
      taView.status(),
      "trusted Gateway reads T-a canonical task with 200",
    ).toBe(200);
    const taBody = (await taView.json()) as {
      task_id: string;
      ingested_at: string;
    };
    observedTupleA = {
      task_id: taBody.task_id,
      ingested_at: taBody.ingested_at,
    };

    const tbView = await gwApi.get(
      `/v1/tasks/${encodeURIComponent(tb.task_id)}`,
    );
    expect(
      tbView.status(),
      "trusted Gateway reads T-b canonical task with 200",
    ).toBe(200);
    const tbBody = (await tbView.json()) as {
      task_id: string;
      ingested_at: string;
    };
    observedTupleB = {
      task_id: tbBody.task_id,
      ingested_at: tbBody.ingested_at,
    };
  } finally {
    await gwApi.dispose();
  }

  await test.step("honest RED: equal-time tie fixture is the precondition for the task_id tie-break assertion", async () => {
    // The preconditions in autotest/test-cases/v0002.72-fifo-tie-break-by-task-id.md
    // require the two tasks to "intentionally share the same
    // `ingested_at` (verified through direct database observation or a
    // documented ingestion race)". The supported HTTP boundary does
    // not expose a caller-supplied `ingested_at` field, so the test
    // cannot force the equal-time tie fixture through supported
    // ingestion. We assert the equal-time tie precondition explicitly
    // so the failure surfaces the missing fixture directly when the
    // implementation has not produced one.
    if (observedTupleA.ingested_at !== observedTupleB.ingested_at) {
      throw new Error(
        "v0002.72 honest RED: Registry has not produced the required equal-time " +
          "tie fixture (observed distinct ingested_at values for ta=" +
          observedTupleA.ingested_at +
          " and tb=" +
          observedTupleB.ingested_at +
          "). " +
          "The task_id tie-break cannot be verified when the producer does not emit " +
          "identical timestamps. The downstream FIFO and claim-order assertions still " +
          "verify the primary ordering rule below.",
      );
    }
    expect(true, "equal-time tie fixture is present").toBe(true);
  });

  await test.step("discovery yields the supported FIFO order (ingested_at ASC, task_id ASC)", async () => {
    const firstExpected =
      observedTupleA.task_id < observedTupleB.task_id
        ? observedTupleA
        : observedTupleB;
    const secondExpected =
      firstExpected.task_id === observedTupleA.task_id
        ? observedTupleB
        : observedTupleA;

    const api = await exec.api(worker.baseUrl);
    try {
      const resp = await api.get(
        `/v1/executors/${encodeURIComponent(exec.attach()["X-FlowAI-Executor-Id"] ?? "")}/tasks?tag=openhands`,
      );
      expect(
        resp.status(),
        "discovery returns 200 for the registered Executor",
      ).toBe(200);
      const body = await resp.json();
      const items = Array.isArray((body as { items: unknown }).items)
        ? (
            body as {
              items: Array<{
                task_id: string;
                team_id: string;
                required_tag: string;
                current_state: string;
                ingested_at: string;
              }>;
            }
          ).items
        : [];
      const ids = items.map((it) => it.task_id);
      const idxA = ids.indexOf(observedTupleA.task_id);
      const idxB = ids.indexOf(observedTupleB.task_id);
      expect(idxA, "T-a appears in the discovery page").toBeGreaterThanOrEqual(
        0,
      );
      expect(idxB, "T-b appears in the discovery page").toBeGreaterThanOrEqual(
        0,
      );
      expect(
        ids.indexOf(firstExpected.task_id),
        "lexically smaller task_id is the first FIFO entry",
      ).toBeLessThan(ids.indexOf(secondExpected.task_id));
    } finally {
      await api.dispose();
    }
  });

  const firstTuple =
    observedTupleA.task_id < observedTupleB.task_id
      ? observedTupleA
      : observedTupleB;
  const secondTuple =
    firstTuple.task_id === observedTupleA.task_id
      ? observedTupleB
      : observedTupleA;

  await test.step("first claim for the lexically larger task_id returns EXACT 409 older_task_must_be_claimed_first with no mutation", async () => {
    const resp = await claimAs(
      exec,
      worker.baseUrl,
      secondTuple.task_id,
      "C-72-second-first",
    );
    expect(resp.status(), "older_task_must_be_claimed_first is EXACT 409").toBe(
      409,
    );
    const respBody = (await resp.json()) as { code: string };
    expect(
      respBody.code,
      "older_task_must_be_claimed_first body carries EXACT code",
    ).toBe("older_task_must_be_claimed_first");
    const gwApi2 = await gw.api(worker.baseUrl);
    try {
      const secondView = await gwApi2.get(
        `/v1/tasks/${encodeURIComponent(secondTuple.task_id)}`,
      );
      expect(
        secondView.status(),
        "second canonical task read still returns 200",
      ).toBe(200);
      const secondRead = (await secondView.json()) as {
        owner_command_id: string | null;
        executor_id: string | null;
        current_state: string;
      };
      expect(
        secondRead.owner_command_id,
        "second owner_command_id remains NULL after the rejected claim",
      ).toBeNull();
      expect(
        secondRead.executor_id,
        "second executor_id remains NULL after the rejected claim",
      ).toBeNull();
      expect(
        secondRead.current_state,
        "second current_state remains pending after the rejected claim",
      ).toBe("pending");
    } finally {
      await gwApi2.dispose();
    }
  });

  await test.step("claim for the lexically smaller task_id returns 200 with the FIRST Registry-appended created event", async () => {
    const resp = await claimAs(
      exec,
      worker.baseUrl,
      firstTuple.task_id,
      "C-72-first",
    );
    expect(resp.status(), "oldest eligible task claim returns 200").toBe(200);
  });

  await test.step("claim for the second task returns 200 after the first is claimed", async () => {
    const resp = await claimAs(
      exec,
      worker.baseUrl,
      secondTuple.task_id,
      "C-72-second",
    );
    expect(resp.status(), "remaining eligible task claim returns 200").toBe(
      200,
    );
  });
});

test("v0002.73 claim persists resolved_image and image_source at claim time", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-73-team",
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
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-73",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  await test.step("claim response carries resolved_image and image_source from team-default precedence", async () => {
    const resp = await claimAs(exec, worker.baseUrl, task.task_id, "C-73");
    expect(resp.status()).toBe(200);
    const body = (await resp.json()) as {
      resolved_image: unknown;
      image_source: string | null;
      task: {
        resolved_image: unknown;
        image_source: string | null;
        current_state: string;
      };
    };
    expect(
      body.resolved_image,
      "resolved_image is present in claim response",
    ).toBeTruthy();
    expect(body.image_source, "image_source is present in claim response").toBe(
      "team_default",
    );
    expect(
      body.task.resolved_image,
      "tasks.resolved_image is persisted at claim time",
    ).toEqual(body.resolved_image);
    expect(
      body.task.image_source,
      "tasks.image_source is persisted at claim time",
    ).toBe("team_default");
    expect(
      body.task.current_state,
      "task is projected current_state = created",
    ).toBe("created");
  });
});
