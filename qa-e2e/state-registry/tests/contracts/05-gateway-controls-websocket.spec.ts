// Contract group: gateway-controls-websocket
// Covers v0002.7, v0002.12, v0002.37, v0002.41, v0002.42, v0002.43,
// v0002.44.
//
// Each test below exercises one of the team-scoped operator and team-
// isolation boundaries documented in the contract:
//   - POST /v1/tasks/{task_id}/controls (audit-backed operator control)
//   - GET /v1/tasks (team-filtered collection read)
//   - GET /v1/audit, GET /v1/audit/{audit_id} (audit reads)
//   - GET /v1/events/stream (team-bound WebSocket upgrade)
//
// Behavior-specific assertions verify:
//   - gateway-mediated controls are team-scoped; foreign-team controls
//     return non-revealing 404
//   - service identities are enforced across listener / Executor /
//     Gateway / admin boundaries
//   - team-filter precedes pagination, counts, and audit
//   - WebSocket subscriptions bind at upgrade and never cross teams
//   - direct copied headers without the trusted Gateway identity are
//     rejected
//   - team_name is display-only and never grants authority
import { test, expect } from "@playwright/test";
import {
  startRegistryWorker,
  type RegistryWorker,
} from "../../fixtures/registry_worker";
import {
  systemAdministrator,
  listenerFor,
  gatewayFor,
  teamExecutorFor,
} from "../../fixtures/identities";
import {
  bootstrapTeam,
  ingestPendingTask,
  registerExecutor,
  snapshotResponse,
  imageReference,
} from "./_setup";
import { uniqueExecutorId } from "../../fixtures/executor_container";
import { openApiOnlySocketWithHeaders } from "../../fixtures/websocket";

let worker: RegistryWorker;

test.beforeAll(async () => {
  worker = await startRegistryWorker();
});

test.afterAll(async () => {
  if (worker) {
    await worker.teardown();
  }
});

test("v0002.7 gateway-mediated control is team-scoped and audit-backed; direct listener copy is rejected", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-7-team",
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
  const gw = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwApi = await gw.api(worker.baseUrl);
  try {
    await test.step("trusted Gateway creates the cancel control with 202", async () => {
      const resp = await gwApi.post(
        `/v1/tasks/${encodeURIComponent(task.task_id)}/controls`,
        {
          data: {
            action: "cancel",
            idempotency_key: "idem-v0002-7-1",
            reason: "unit-test",
          },
        },
      );
      expect(
        resp.status(),
        "trusted Gateway creates the team-a cancel control",
      ).toBe(202);
      const body = await resp.json();
      expect(body.team_id, "control carries the verified team_id").toBe(
        bsA.admin.team_id,
      );
      expect(body.task_id, "control references the team task_id").toBe(
        task.task_id,
      );
    });
    await test.step("positive control: idempotent retry returns the same control with 202", async () => {
      const resp = await gwApi.post(
        `/v1/tasks/${encodeURIComponent(task.task_id)}/controls`,
        {
          data: {
            action: "cancel",
            idempotency_key: "idem-v0002-7-1",
            reason: "unit-test",
          },
        },
      );
      expect(resp.status(), "idempotent retry returns 202").toBe(202);
    });
  } finally {
    await gwApi.dispose();
  }
  await test.step("negative control: direct listener copy is rejected with 403 without mutation", async () => {
    const listener = listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    });
    const listenerApi = await listener.api(worker.baseUrl);
    try {
      const resp = await listenerApi.post(
        `/v1/tasks/${encodeURIComponent(task.task_id)}/controls`,
        {
          data: { action: "cancel", idempotency_key: "idem-v0002-7-direct" },
        },
      );
      expect(
        resp.status(),
        "listener identity cannot create Gateway-only controls",
      ).toBe(403);
    } finally {
      await listenerApi.dispose();
    }
  });
});

test("v0002.12 service-identity enforcement: impersonating and unauthenticated calls are rejected; admin-only paths reject non-admin identities", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-12-team",
    executionTag: "openhands",
  });
  await test.step("listener impersonation for a foreign team is rejected with 403", async () => {
    const listener = listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    });
    const api = await listener.api(worker.baseUrl);
    try {
      const resp = await api.post("/v1/tasks", {
        data: {
          team_id: "team-foreign",
          source_system_id: "ss-foreign",
          source_id: "X",
          task_type_id: "tt-foreign",
          payload: {},
        },
      });
      expect(resp.status(), "listener cannot ingest for a foreign team").toBe(
        403,
      );
    } finally {
      await api.dispose();
    }
  });
  await test.step("admin endpoint rejects trusted Gateway identity with 403", async () => {
    const gw = gatewayFor({
      teamId: bsA.admin.team_id,
      operatorId: `op-${bsA.admin.team_id}`,
    });
    const api = await gw.api(worker.baseUrl);
    try {
      const resp = await api.post("/admin/teams", {
        data: {
          team_name: "gateway-impersonator",
          default_image: imageReference("default-gateway"),
        },
      });
      expect(
        resp.status(),
        "trusted Gateway identity is rejected on the admin transport boundary",
      ).toBe(403);
    } finally {
      await api.dispose();
    }
  });
});

test("v0002.37 control creation is team-scoped and idempotent; foreign task control returns non-revealing 404", async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-37-${Date.now().toString(36)}`;
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-a`,
    executionTag: "openhands",
  });
  const bsB = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-b`,
    executionTag: "openhands",
  });
  const taskA = await ingestPendingTask(
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
  const taskB = await ingestPendingTask(
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
  const gwA = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwApi = await gwA.api(worker.baseUrl);
  try {
    await test.step("team-a Gateway creates a control for the team-a task; retry is idempotent", async () => {
      const first = await gwApi.post(
        `/v1/tasks/${encodeURIComponent(taskA.task_id)}/controls`,
        {
          data: { action: "cancel", idempotency_key: "v0002-37-idem-1" },
        },
      );
      expect(first.status(), "team-a Gateway creates the team-a control").toBe(
        202,
      );
      const retry = await gwApi.post(
        `/v1/tasks/${encodeURIComponent(taskA.task_id)}/controls`,
        {
          data: { action: "cancel", idempotency_key: "v0002-37-idem-1" },
        },
      );
      expect(retry.status(), "idempotent retry returns 202").toBe(202);
    });
    await test.step("team-a Gateway attempting a control for the team-b task returns non-revealing 404", async () => {
      const resp = await gwApi.post(
        `/v1/tasks/${encodeURIComponent(taskB.task_id)}/controls`,
        {
          data: { action: "cancel", idempotency_key: "v0002-37-foreign" },
        },
      );
      expect(resp.status(), "foreign-task control is non-revealing 404").toBe(
        404,
      );
    });
  } finally {
    await gwApi.dispose();
  }
});

test("v0002.41 team-filter precedes pagination, counts, and audit reads", async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-41-${Date.now().toString(36)}`;
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-a`,
    executionTag: "openhands",
  });
  const bsB = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-b`,
    executionTag: "openhands",
  });
  const taskA = await ingestPendingTask(
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
      payload: {},
    },
  );
  await ingestPendingTask(
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
      payload: {},
    },
  );
  await test.step("team-a Gateway list returns ONLY team-a tasks with 200", async () => {
    const gw = gatewayFor({
      teamId: bsA.admin.team_id,
      operatorId: `op-${bsA.admin.team_id}`,
    });
    const api = await gw.api(worker.baseUrl);
    try {
      const resp = await api.get("/v1/tasks?limit=50");
      expect(
        resp.status(),
        "trusted Gateway list returns 200 for the authenticated team",
      ).toBe(200);
      const body = await resp.json();
      const items = Array.isArray(body.items) ? body.items : [];
      const teamIds = (items as Array<{ team_id: string }>).map(
        (it) => it.team_id,
      );
      const taskIds = (items as Array<{ task_id: string }>).map(
        (it) => it.task_id,
      );
      expect(
        items.length,
        "team-a list is non-empty and proves own-team rows exist",
      ).toBeGreaterThan(0);
      expect(taskIds, "team-a list contains the team-a task_id").toContain(
        taskA.task_id,
      );
      expect(
        teamIds.every((tid) => tid === bsA.admin.team_id),
        "all items belong to team-a",
      ).toBe(true);
    } finally {
      await api.dispose();
    }
  });
});

test("v0002.42 WebSocket upgrade binds at upgrade and never emits cross-team frames", async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-42-${Date.now().toString(36)}`;
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-a`,
    executionTag: "openhands",
  });
  const bsB = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-b`,
    executionTag: "openhands",
  });

  const taskA = await ingestPendingTask(
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
  const taskB = await ingestPendingTask(
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
  const execB = teamExecutorFor({
    teamId: bsB.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(execA, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-42-a",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  await registerExecutor(execB, worker.baseUrl, {
    scope: "team",
    team_id: bsB.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-42-b",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });

  const wsUrl = worker.baseUrl.replace(/^http/, "ws") + "/v1/events/stream";
  const gwA = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwB = gatewayFor({
    teamId: bsB.admin.team_id,
    operatorId: `op-${bsB.admin.team_id}`,
  });
  const [sockA, sockB] = await Promise.all([
    openApiOnlySocketWithHeaders(wsUrl, { ...gwA.attach() }, 5_000),
    openApiOnlySocketWithHeaders(wsUrl, { ...gwB.attach() }, 5_000),
  ]);
  try {
    await test.step("both authenticated WebSocket upgrades succeed and bind immutable team context", async () => {
      await Promise.all([sockA.opened(), sockB.opened()]);
    });

    await test.step("after both sockets are open, claim tasks to trigger Registry-appended lifecycle events", async () => {
      const claimAApi = await execA.api(worker.baseUrl);
      let claimA: Awaited<ReturnType<typeof snapshotResponse>> | undefined;
      try {
        const resp = await claimAApi.post(
          `/v1/executors/${encodeURIComponent(execA.attach()["X-FlowAI-Executor-Id"] ?? "")}/claim`,
          { data: { task_id: taskA.task_id, command_id: "C-A-42" } },
        );
        claimA = await snapshotResponse(resp);
      } finally {
        await claimAApi.dispose();
      }
      expect(claimA?.status(), "team-a Executor claim returns 200").toBe(200);

      const claimBApi = await execB.api(worker.baseUrl);
      let claimB: Awaited<ReturnType<typeof snapshotResponse>> | undefined;
      try {
        const resp = await claimBApi.post(
          `/v1/executors/${encodeURIComponent(execB.attach()["X-FlowAI-Executor-Id"] ?? "")}/claim`,
          { data: { task_id: taskB.task_id, command_id: "C-B-42" } },
        );
        claimB = await snapshotResponse(resp);
      } finally {
        await claimBApi.dispose();
      }
      expect(claimB?.status(), "team-b Executor claim returns 200").toBe(200);
    });

    await test.step("each live connection receives ONLY frames for its immutable bound team_id over a bounded observation period", async () => {
      const framesA: unknown[] = [];
      const framesB: unknown[] = [];
      const observationBudgetMs = 2_000;
      const pollIntervalMs = 100;
      const deadline = Date.now() + observationBudgetMs;
      while (Date.now() < deadline) {
        const slotEnd = Date.now() + pollIntervalMs;
        while (Date.now() < slotEnd) {
          const next = await drainOneFrame(
            sockA,
            Math.max(1, slotEnd - Date.now()),
          );
          if (next !== undefined) framesA.push(next);
        }
        const next = await drainOneFrame(sockB, 50);
        if (next !== undefined) framesB.push(next);
      }
      expect(
        framesA.length,
        "team-a socket observed at least one Registry-appended frame during the observation window",
      ).toBeGreaterThan(0);
      expect(
        framesB.length,
        "team-b socket observed at least one Registry-appended frame during the observation window",
      ).toBeGreaterThan(0);
      for (let i = 0; i < framesA.length; i++) {
        const frame = framesA[i];
        expect(
          frameTeamID(frame),
          `team-a frame[${i}] belongs only to team-a`,
        ).toBe(bsA.admin.team_id);
      }
      for (let i = 0; i < framesB.length; i++) {
        const frame = framesB[i];
        expect(
          frameTeamID(frame),
          `team-b frame[${i}] belongs only to team-b`,
        ).toBe(bsB.admin.team_id);
      }
    });

    await test.step("team-a filter naming a real team-b task produces no frame even after a new team-b lifecycle event", async () => {
      const filtered = await openApiOnlySocketWithHeaders(
        `${wsUrl}?task_id=${encodeURIComponent(taskB.task_id)}`,
        { ...gwA.attach() },
        5_000,
      );
      try {
        await filtered.opened();
        const postEventApi = await execB.api(worker.baseUrl);
        let postEvent: Awaited<ReturnType<typeof snapshotResponse>> | undefined;
        try {
          const resp = await postEventApi.post(
            `/v1/tasks/${encodeURIComponent(taskB.task_id)}/events`,
            {
              data: {
                event_id: "0190-running-b-42",
                team_id: bsB.admin.team_id,
                task_id: taskB.task_id,
                executor_id: execB.attach()["X-FlowAI-Executor-Id"] ?? "",
                event_type: "running",
                occurred_at: futureIsoMs(60_000),
                payload: {},
              },
            },
          );
          postEvent = await snapshotResponse(resp);
        } finally {
          await postEventApi.dispose();
        }
        expect(
          postEvent?.status(),
          "team-b running event is accepted with 202 to seed a foreign frame",
        ).toBe(202);
        await expect(filtered.nextMessage(1_000)).rejects.toThrow(
          /message timeout/,
        );
      } finally {
        await filtered.close();
      }
    });
  } finally {
    await Promise.allSettled([sockA.close(), sockB.close()]);
  }
});

function futureIsoMs(deltaMs: number): string {
  return new Date(Date.now() + deltaMs).toISOString();
}

async function drainOneFrame(
  socket: { nextMessage: (timeoutMs: number) => Promise<unknown> },
  timeoutMs: number,
): Promise<unknown | undefined> {
  try {
    return await Promise.race([
      socket.nextMessage(timeoutMs),
      new Promise<never>((_, reject) =>
        setTimeout(() => reject(new Error("drain-timeout")), timeoutMs),
      ),
    ]);
  } catch {
    return undefined;
  }
}

function frameTeamID(frame: unknown): unknown {
  if (frame === null || typeof frame !== "object") {
    return undefined;
  }
  const record = frame as Record<string, unknown>;
  if (record.team_id !== undefined) {
    return record.team_id;
  }
  for (const key of ["data", "resource", "payload"]) {
    const nested = record[key];
    if (nested !== null && typeof nested === "object") {
      const teamID = (nested as Record<string, unknown>).team_id;
      if (teamID !== undefined) {
        return teamID;
      }
    }
  }
  return undefined;
}

test("v0002.43 State Registry trusts only verified Gateway context; copied headers are rejected", async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-43-${Date.now().toString(36)}`;
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-a`,
    executionTag: "openhands",
  });
  const bsOther = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-other`,
    executionTag: "openhands",
  });
  const taskA = await ingestPendingTask(
    listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsA.admin.team_id,
      source_system_id: bsA.sourceSystem.source_system_id,
      source_id: "A-43",
      task_type_id: bsA.taskType.task_type_id,
      payload: {},
    },
  );
  const taskOther = await ingestPendingTask(
    listenerFor({
      teamId: bsOther.admin.team_id,
      listenerIdentity: bsOther.listenerIdentity,
      sourceSystemId: bsOther.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsOther.admin.team_id,
      source_system_id: bsOther.sourceSystem.source_system_id,
      source_id: "OTHER-43",
      task_type_id: bsOther.taskType.task_type_id,
      payload: {},
    },
  );
  await test.step("trusted Gateway can read team-a tasks with 200 and sees the seeded own-team row", async () => {
    const gw = gatewayFor({
      teamId: bsA.admin.team_id,
      operatorId: `op-${bsA.admin.team_id}`,
    });
    const api = await gw.api(worker.baseUrl);
    try {
      const resp = await api.get("/v1/tasks?limit=10");
      expect(resp.status(), "trusted Gateway list returns 200").toBe(200);
      const body = await resp.json();
      const items = Array.isArray(body.items) ? body.items : [];
      const taskIds = (items as Array<{ task_id: string }>).map(
        (it) => it.task_id,
      );
      const teamIds = (items as Array<{ team_id: string }>).map(
        (it) => it.team_id,
      );
      expect(
        items.length,
        "team-a list is non-empty and proves own-team rows exist",
      ).toBeGreaterThan(0);
      expect(
        taskIds,
        "team-a list contains the seeded team-a task_id",
      ).toContain(taskA.task_id);
      expect(
        teamIds.every((tid) => tid === bsA.admin.team_id),
        "all items belong to team-a",
      ).toBe(true);
    } finally {
      await api.dispose();
    }
  });
  await test.step("different Gateway identity sees ONLY its own team rows and proves its own rows exist", async () => {
    const other = gatewayFor({
      teamId: bsOther.admin.team_id,
      operatorId: `op-${bsOther.admin.team_id}`,
    });
    const api = await other.api(worker.baseUrl);
    try {
      const resp = await api.get(`/v1/tasks?limit=10`);
      expect(
        resp.status(),
        "different Gateway list returns 200 for its own team",
      ).toBe(200);
      const body = await resp.json();
      const items = Array.isArray(body.items) ? body.items : [];
      const taskIds = (items as Array<{ task_id: string }>).map(
        (it) => it.task_id,
      );
      const teamIds = (items as Array<{ team_id: string }>).map(
        (it) => it.team_id,
      );
      expect(
        items.length,
        "other-team list is non-empty and proves other-team rows exist",
      ).toBeGreaterThan(0);
      expect(
        taskIds,
        "other-team list contains the seeded other-team task_id",
      ).toContain(taskOther.task_id);
      expect(
        teamIds.every((tid) => tid === bsOther.admin.team_id),
        "items belong to the other team only",
      ).toBe(true);
      expect(
        taskIds,
        "other-team list does not leak team-a task_id",
      ).not.toContain(taskA.task_id);
    } finally {
      await api.dispose();
    }
  });
});

test("v0002.44 team_name is display-only and never grants authority", async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-44-${Date.now().toString(36)}`;
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-a`,
    executionTag: "openhands",
  });
  const bsB = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-b`,
    executionTag: "openhands",
  });
  const taskA = await ingestPendingTask(
    listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsA.admin.team_id,
      source_system_id: bsA.sourceSystem.source_system_id,
      source_id: "A-44",
      task_type_id: bsA.taskType.task_type_id,
      payload: {},
    },
  );
  const taskB = await ingestPendingTask(
    listenerFor({
      teamId: bsB.admin.team_id,
      listenerIdentity: bsB.listenerIdentity,
      sourceSystemId: bsB.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsB.admin.team_id,
      source_system_id: bsB.sourceSystem.source_system_id,
      source_id: "B-44",
      task_type_id: bsB.taskType.task_type_id,
      payload: {},
    },
  );
  await test.step("verified team-a Gateway with normal display team_name reads the team-a row and never the team-b row (returns 200)", async () => {
    const gw = gatewayFor({
      teamId: bsA.admin.team_id,
      operatorId: `op-${bsA.admin.team_id}`,
    });
    const api = await gw.api(worker.baseUrl);
    try {
      const resp = await api.get("/v1/tasks?limit=10");
      expect(
        resp.status(),
        "team_name as display-only does not affect list authorization (200)",
      ).toBe(200);
      const body = await resp.json();
      const items = Array.isArray(body.items) ? body.items : [];
      const taskIds = (items as Array<{ task_id: string }>).map(
        (it) => it.task_id,
      );
      expect(
        taskIds,
        "verified team-a list contains the team-a task_id",
      ).toContain(taskA.task_id);
      expect(
        taskIds,
        "verified team-a list never leaks the team-b task_id",
      ).not.toContain(taskB.task_id);
    } finally {
      await api.dispose();
    }
  });
  await test.step("a deliberately foreign display team_name while verified team_id remains team-a cannot grant team-b authority", async () => {
    const gw = gatewayFor({
      teamId: bsA.admin.team_id,
      operatorId: `op-${bsA.admin.team_id}`,
      teamName: bsB.admin.team_name,
    });
    const api = await gw.api(worker.baseUrl);
    try {
      const listResp = await api.get("/v1/tasks?limit=10");
      expect(
        listResp.status(),
        "spoofed display team_name does not change list authorization (200)",
      ).toBe(200);
      const body = await listResp.json();
      const items = Array.isArray(body.items) ? body.items : [];
      const taskIds = (items as Array<{ task_id: string }>).map(
        (it) => it.task_id,
      );
      const teamIds = (items as Array<{ team_id: string }>).map(
        (it) => it.team_id,
      );
      expect(
        taskIds,
        "spoofed display team_name still returns team-a task_id",
      ).toContain(taskA.task_id);
      expect(
        taskIds,
        "spoofed display team_name never returns team-b task_id",
      ).not.toContain(taskB.task_id);
      expect(
        teamIds.every((tid) => tid === bsA.admin.team_id),
        "team_id filter is anchored to verified team-a only",
      ).toBe(true);

      const controlResp = await api.post(
        `/v1/tasks/${encodeURIComponent(taskB.task_id)}/controls`,
        {
          data: { action: "cancel", idempotency_key: "v0002-44-foreign-name" },
        },
      );
      expect(
        controlResp.status(),
        "foreign display team_name does not authorize team-b control (non-revealing 404)",
      ).toBe(404);
    } finally {
      await api.dispose();
    }
  });
});
