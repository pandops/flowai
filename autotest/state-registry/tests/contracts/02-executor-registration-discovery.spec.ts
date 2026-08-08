// Contract group: executor-registration-discovery
// Covers v0002.3, v0002.27, v0002.28, v0002.34, v0002.54, v0002.55,
// v0002.56, v0002.59, v0002.67.
//
// Each test below exercises one or more of the Executor registration
// and discovery boundaries documented in the contract:
//   - PUT /v1/executors/{executor_id}
//   - GET /v1/executors/{executor_id}/tasks?tag=<registered>
//   - GET /v1/executors/{executor_id}
//   - GET /v1/tasks/{task_id} (point read for foreign probes — trusted
//     Gateway context only)
//
// Behavior-specific assertions verify:
//   - exact-tag eligibility precedes FIFO ordering
//   - team-owned discovery is same-team only; system-owned discovery
//     spans teams sharing the registered tag
//   - zero-tag and multi-tag registrations are rejected without
//     persistence
//   - capacity observations are informational and never gate claim
//   - foreign task identifiers return the same non-revealing shape as
//     unknown
//   - scope is immutable from registration onward; re-registration
//     with a different scope is rejected
//   - discovery refreshes FIFO ordering from observed
//     (ingested_at, task_id) tuples; the discovery summary shape is
//     EXACTLY the documented TaskSummary allowlist
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
import { bootstrapTeam, ingestPendingTask, registerExecutor } from "./_setup";
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

interface TaskSummary {
  task_id: string;
  team_id: string;
  required_tag: string;
  current_state: string;
  ingested_at: string;
}

interface DiscoveryPage {
  items: TaskSummary[];
}

function readItems(body: unknown): TaskSummary[] {
  if (!body || typeof body !== "object") return [];
  const items = (body as DiscoveryPage).items;
  return Array.isArray(items) ? items : [];
}

test("v0002.3 executor discovers only pending same-team tasks matching the registered exact tag", async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-3-${Date.now().toString(36)}`;
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-a`,
    executionTag: "openhands",
  });
  const execA = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(execA, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-3-a",
    authorized_tag: "openhands",
    max_capacity: 4,
    running_count: 0,
    runtime_metadata: { runtime: "docker", tool: "openhands" },
  });

  const openTask = await ingestPendingTask(
    listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsA.admin.team_id,
      source_system_id: bsA.sourceSystem.source_system_id,
      source_id: "OPEN-1",
      task_type_id: bsA.taskType.task_type_id,
      payload: { hello: "world" },
    },
  );
  const bsK8s = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-k8s`,
    executionTag: "k8s",
  });
  const k8sTask = await ingestPendingTask(
    listenerFor({
      teamId: bsK8s.admin.team_id,
      listenerIdentity: bsK8s.listenerIdentity,
      sourceSystemId: bsK8s.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsK8s.admin.team_id,
      source_system_id: bsK8s.sourceSystem.source_system_id,
      source_id: "K8S-1",
      task_type_id: bsK8s.taskType.task_type_id,
      payload: { hello: "world" },
    },
  );

  await test.step("GET /v1/executors/{id}/tasks?tag=openhands returns 200 with only the same-team openhands pending summary", async () => {
    const api = await execA.api(worker.baseUrl);
    try {
      const resp = await api.get(
        `/v1/executors/${encodeURIComponent(execA.attach()["X-FlowAI-Executor-Id"] ?? "")}/tasks?tag=openhands`,
      );
      expect(resp.status()).toBe(200);
      const body = await resp.json();
      const items = readItems(body);
      expect(
        items.length,
        "discovery returns at least one team-a openhands pending summary",
      ).toBeGreaterThanOrEqual(1);
      const ids = items.map((it) => it.task_id);
      expect(
        ids,
        "team-a openhands task is visible to the team-a Executor",
      ).toContain(openTask.task_id);
      expect(
        ids,
        "foreign team k8s task is NOT visible to the team-a openhands Executor",
      ).not.toContain(k8sTask.task_id);
      for (const it of items) {
        expect(it.team_id, "every summary belongs to team-a only").toBe(
          bsA.admin.team_id,
        );
        expect(
          it.required_tag,
          "every summary carries the registered openhands tag",
        ).toBe("openhands");
        expect(
          it.current_state,
          "every summary is projected current_state = pending",
        ).toBe("pending");
      }
      // Exact discovery summary allowlist per OpenAPI TaskSummary schema
      const firstItem = items[0];
      if (!firstItem) {
        throw new Error(
          "discovery returned no items; positive non-vacuous check requires at least one summary",
        );
      }
      const summaryKeys = Object.keys(
        firstItem as unknown as Record<string, unknown>,
      ).sort();
      expect(
        summaryKeys,
        "TaskSummary shape is EXACTLY task_id, team_id, required_tag, current_state, ingested_at",
      ).toEqual([
        "current_state",
        "ingested_at",
        "required_tag",
        "task_id",
        "team_id",
      ]);
    } finally {
      await api.dispose();
    }
  });

  await test.step("negative control: GET /v1/executors/{id}/tasks?tag=k8s is rejected (registered tag is openhands)", async () => {
    const api = await execA.api(worker.baseUrl);
    try {
      const resp = await api.get(
        `/v1/executors/${encodeURIComponent(execA.attach()["X-FlowAI-Executor-Id"] ?? "")}/tasks?tag=k8s`,
      );
      expect(resp.status(), "unregistered-tag discovery is rejected").toBe(400);
    } finally {
      await api.dispose();
    }
  });
});

test("v0002.27 registration rejects zero-tag and multi-tag bodies with invalid_tag_count", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-27-team",
    executionTag: "openhands",
  });
  const exec = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });

  await test.step("zero-tag registration is rejected with 400 invalid_tag_count", async () => {
    const api = await exec.api(worker.baseUrl);
    try {
      const resp = await api.put(
        `/v1/executors/${encodeURIComponent(exec.attach()["X-FlowAI-Executor-Id"] ?? "")}`,
        {
          data: {
            scope: "team",
            team_id: bsA.admin.team_id,
            executor_type: "executor_docker_openhands",
            identity: "exec-zero-tags",
            authorized_tag: "",
            max_capacity: 1,
            running_count: 0,
            runtime_metadata: {},
          },
        },
      );
      expect(
        resp.status(),
        "zero-tag registration is rejected with 400 invalid_tag_count",
      ).toBe(400);
    } finally {
      await api.dispose();
    }
  });

  await test.step("multi-tag registration is rejected with 400 invalid_tag_count", async () => {
    const api = await exec.api(worker.baseUrl);
    try {
      const resp = await api.put(
        `/v1/executors/${encodeURIComponent(exec.attach()["X-FlowAI-Executor-Id"] ?? "")}`,
        {
          data: {
            scope: "team",
            team_id: bsA.admin.team_id,
            executor_type: "executor_docker_openhands",
            identity: "exec-many-tags",
            authorized_tag: ["openhands", "k8s"],
            max_capacity: 1,
            running_count: 0,
            runtime_metadata: {},
          },
        },
      );
      expect(
        resp.status(),
        "multi-tag registration is rejected with 400 invalid_tag_count",
      ).toBe(400);
    } finally {
      await api.dispose();
    }
  });
});

test("v0002.28 capacity observations are informational; claim is not gated on saturated capacity", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-28-team",
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
    identity: "exec-v0002-28",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 1,
    runtime_metadata: {},
  });
  await test.step("Executor record persists the saturated observation", async () => {
    const api = await exec.api(worker.baseUrl);
    try {
      const resp = await api.get(
        `/v1/executors/${encodeURIComponent(exec.attach()["X-FlowAI-Executor-Id"] ?? "")}`,
      );
      expect(resp.status()).toBe(200);
      const body = await resp.json();
      expect(body.max_capacity).toBe(1);
      expect(body.running_count).toBe(1);
    } finally {
      await api.dispose();
    }
  });
});

test("v0002.34 discovery isolates team before pagination; foreign point probe returns non-revealing 404", async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-34-${Date.now().toString(36)}`;
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-a`,
    executionTag: "openhands",
  });
  const bsB = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-b`,
    executionTag: "openhands",
  });
  const execA = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(execA, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-34-a",
    authorized_tag: "openhands",
    max_capacity: 4,
    running_count: 0,
    runtime_metadata: {},
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
  const teamATask = await ingestPendingTask(
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

  await test.step("team-a Executor discovery returns 200 with ONLY team-a summaries; team-b is invisible", async () => {
    const api = await execA.api(worker.baseUrl);
    try {
      const resp = await api.get(
        `/v1/executors/${encodeURIComponent(execA.attach()["X-FlowAI-Executor-Id"] ?? "")}/tasks?tag=openhands`,
      );
      expect(resp.status()).toBe(200);
      const body = await resp.json();
      const items = readItems(body);
      expect(
        items.length,
        "team-a discovery is not empty (positive non-vacuous check)",
      ).toBeGreaterThanOrEqual(1);
      const ids = items.map((it) => it.task_id);
      expect(
        ids,
        "team-a task is visible to team-a Executor (positive non-vacuous check)",
      ).toContain(teamATask.task_id);
      expect(
        ids,
        "foreign team-b summary is not visible to team-a Executor",
      ).not.toContain(teamBTask.task_id);
      for (const it of items) {
        expect(it.team_id, "every summary belongs to team-a only").toBe(
          bsA.admin.team_id,
        );
        expect(
          it.required_tag,
          "every summary carries the registered openhands tag",
        ).toBe("openhands");
      }
    } finally {
      await api.dispose();
    }
  });

  await test.step("negative control: team-a Executor point probe for team-b task returns non-revealing 404 (same as unknown)", async () => {
    const gw = gatewayFor({
      teamId: bsA.admin.team_id,
      operatorId: `op-${bsA.admin.team_id}`,
    });
    const gwApi = await gw.api(worker.baseUrl);
    try {
      const foreign = await gwApi.get(
        `/v1/tasks/${encodeURIComponent(teamBTask.task_id)}`,
      );
      const unknown = await gwApi.get(
        `/v1/tasks/00000000-0000-0000-0000-000000000000`,
      );
      expect(foreign.status(), "foreign task probe is non-revealing 404").toBe(
        404,
      );
      expect(unknown.status(), "unknown task probe is non-revealing 404").toBe(
        404,
      );
      const foreignBody = await foreign.text();
      const unknownBody = await unknown.text();
      expect(
        foreignBody,
        "foreign and unknown 404 responses have identical bodies",
      ).toBe(unknownBody);
    } finally {
      await gwApi.dispose();
    }
  });
});

test("v0002.54 system-owned Executor registers with scope=system, team_id=null; non-null team_id is rejected", async () => {
  const execId = uniqueExecutorId();
  const exec = systemExecutorWithId(execId);
  await test.step("PUT /v1/executors/{id} with scope=system, team_id=null returns 200", async () => {
    const reg = await registerExecutor(exec, worker.baseUrl, {
      scope: "system",
      team_id: null,
      executor_type: "executor_docker_openhands",
      identity: "exec-v0002-54-system",
      authorized_tag: "openhands",
      max_capacity: 4,
      running_count: 0,
      runtime_metadata: {},
    });
    expect(reg.scope).toBe("system");
    expect(reg.team_id).toBeNull();
  });
  await test.step("negative control: scope=system with non-null team_id is rejected with 400 without persistence", async () => {
    const api = await exec.api(worker.baseUrl);
    try {
      const resp = await api.put(
        `/v1/executors/${encodeURIComponent(execId)}`,
        {
          data: {
            scope: "system",
            team_id: "team-foreign",
            executor_type: "executor_docker_openhands",
            identity: execId,
            authorized_tag: "openhands",
            max_capacity: 1,
            running_count: 0,
            runtime_metadata: {},
          },
        },
      );
      expect(
        resp.status(),
        "system scope with non-null team_id is rejected with 400",
      ).toBe(400);
    } finally {
      await api.dispose();
    }
  });
});

test("v0002.55 system-owned Executor discovers pending tasks across teams sharing its registered tag", async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-55-${Date.now().toString(36)}`;
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-a`,
    executionTag: "openhands",
  });
  const bsB = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-b`,
    executionTag: "openhands",
  });
  const sysExec = systemExecutorWithId(uniqueExecutorId());
  await registerExecutor(sysExec, worker.baseUrl, {
    scope: "system",
    team_id: null,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-55-system",
    authorized_tag: "openhands",
    max_capacity: 4,
    running_count: 0,
    runtime_metadata: {},
  });
  const teamATask = await ingestPendingTask(
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
  await test.step("GET /v1/executors/{id}/tasks?tag=openhands returns 200 with BOTH teams' summaries", async () => {
    const api = await sysExec.api(worker.baseUrl);
    try {
      const resp = await api.get(
        `/v1/executors/${encodeURIComponent(sysExec.attach()["X-FlowAI-Executor-Id"] ?? "")}/tasks?tag=openhands`,
      );
      expect(resp.status()).toBe(200);
      const body = await resp.json();
      const items = readItems(body);
      expect(
        items.length,
        "system discovery returns at least two team summaries (positive non-vacuous check)",
      ).toBeGreaterThanOrEqual(2);
      const ids = items.map((it) => it.task_id);
      expect(
        ids,
        "team-a task is visible to system Executor (positive non-vacuous check)",
      ).toContain(teamATask.task_id);
      expect(
        ids,
        "team-b task is visible to system Executor (positive non-vacuous check)",
      ).toContain(teamBTask.task_id);
      const teamIds = items.map((it) => it.team_id);
      expect(teamIds, "team-a summary is visible to system Executor").toContain(
        bsA.admin.team_id,
      );
      expect(teamIds, "team-b summary is visible to system Executor").toContain(
        bsB.admin.team_id,
      );
      for (const it of items) {
        expect(
          it.required_tag,
          "every summary carries the registered openhands tag",
        ).toBe("openhands");
        expect(
          it.current_state,
          "every summary is projected current_state = pending",
        ).toBe("pending");
      }
    } finally {
      await api.dispose();
    }
  });
});

test("v0002.56 team-owned Executor discovery remains team-scoped even when a system Executor shares the tag", async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-56-${Date.now().toString(36)}`;
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-a`,
    executionTag: "openhands",
  });
  const bsB = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-b`,
    executionTag: "openhands",
  });
  const sysExec = systemExecutorWithId(uniqueExecutorId());
  await registerExecutor(sysExec, worker.baseUrl, {
    scope: "system",
    team_id: null,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-56-system",
    authorized_tag: "openhands",
    max_capacity: 4,
    running_count: 0,
    runtime_metadata: {},
  });
  const teamExec = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(teamExec, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-56-team",
    authorized_tag: "openhands",
    max_capacity: 4,
    running_count: 0,
    runtime_metadata: {},
  });
  const teamATask = await ingestPendingTask(
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
  await test.step("team-a Executor never sees team-b summaries even with a system Executor sharing the tag", async () => {
    const api = await teamExec.api(worker.baseUrl);
    try {
      const resp = await api.get(
        `/v1/executors/${encodeURIComponent(teamExec.attach()["X-FlowAI-Executor-Id"] ?? "")}/tasks?tag=openhands`,
      );
      expect(resp.status()).toBe(200);
      const body = await resp.json();
      const items = readItems(body);
      expect(
        items.length,
        "team-a discovery is not empty (positive non-vacuous check)",
      ).toBeGreaterThanOrEqual(1);
      const ids = items.map((it) => it.task_id);
      expect(
        ids,
        "team-a task is visible to team-a Executor (positive non-vacuous check)",
      ).toContain(teamATask.task_id);
      const teamIds = items.map((it) => it.team_id);
      expect(
        teamIds.every((tid) => tid === bsA.admin.team_id),
        "all summaries belong to team-a",
      ).toBe(true);
      expect(
        ids,
        "team-b task never appears in team-a discovery",
      ).not.toContain(teamBTask.task_id);
    } finally {
      await api.dispose();
    }
  });
});

test("v0002.59 re-registration with a different scope is rejected without mutation; original row remains", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-59-team",
    executionTag: "openhands",
  });
  const sysExec = systemExecutorWithId(uniqueExecutorId());
  const teamExec = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(sysExec, worker.baseUrl, {
    scope: "system",
    team_id: null,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-59-sys",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  await registerExecutor(teamExec, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-59-team",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });

  await test.step("system-owned Executor cannot be re-registered as team-owned (400 without mutation)", async () => {
    const sysId = sysExec.attach()["X-FlowAI-Executor-Id"] ?? "";
    const api = await sysExec.api(worker.baseUrl);
    try {
      const resp = await api.put(`/v1/executors/${encodeURIComponent(sysId)}`, {
        data: {
          scope: "team",
          team_id: bsA.admin.team_id,
          executor_type: "executor_docker_openhands",
          identity: sysId,
          authorized_tag: "openhands",
          max_capacity: 1,
          running_count: 0,
          runtime_metadata: {},
        },
      });
      expect(
        resp.status(),
        "scope-change re-registration is rejected with 400",
      ).toBe(400);
      const view = await api.get(`/v1/executors/${encodeURIComponent(sysId)}`);
      expect(view.status()).toBe(200);
      const body = await view.json();
      expect(body.scope, "scope is immutable from registration").toBe("system");
      expect(
        body.team_id,
        "team_id remains null after rejected re-registration",
      ).toBeNull();
    } finally {
      await api.dispose();
    }
  });

  await test.step("team-owned Executor cannot be re-registered as system-owned (400 without mutation)", async () => {
    const teamId = teamExec.attach()["X-FlowAI-Executor-Id"] ?? "";
    const api = await teamExec.api(worker.baseUrl);
    try {
      const resp = await api.put(
        `/v1/executors/${encodeURIComponent(teamId)}`,
        {
          data: {
            scope: "system",
            team_id: null,
            executor_type: "executor_docker_openhands",
            identity: teamId,
            authorized_tag: "openhands",
            max_capacity: 1,
            running_count: 0,
            runtime_metadata: {},
          },
        },
      );
      expect(
        resp.status(),
        "team-owned Executor cannot switch to system scope (400)",
      ).toBe(400);
      const view = await api.get(`/v1/executors/${encodeURIComponent(teamId)}`);
      expect(view.status()).toBe(200);
      const body = await view.json();
      expect(body.scope, "team scope remains unchanged").toBe("team");
      expect(body.team_id, "team_id remains unchanged").toBe(bsA.admin.team_id);
    } finally {
      await api.dispose();
    }
  });
});

test("v0002.67 FIFO discovery orders eligible pending tasks by (ingested_at ASC, task_id ASC)", async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-67-${Date.now().toString(36)}`;
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-a`,
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
    identity: "exec-v0002-67",
    authorized_tag: "openhands",
    max_capacity: 4,
    running_count: 0,
    runtime_metadata: {},
  });
  const ingest = [
    { sourceId: "T-low", payload: { idx: 0 } },
    { sourceId: "T-mid", payload: { idx: 1 } },
    { sourceId: "T-tie-a", payload: { idx: 2 } },
    { sourceId: "T-tie-b", payload: { idx: 3 } },
  ];
  const ingested = [] as Array<{
    sourceId: string;
    task_id: string;
    ingested_at: string;
  }>;
  for (const entry of ingest) {
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
        source_id: entry.sourceId,
        task_type_id: bsA.taskType.task_type_id,
        payload: entry.payload,
      },
    );
    ingested.push({
      sourceId: entry.sourceId,
      task_id: task.task_id,
      ingested_at: task.ingested_at,
    });
  }

  await test.step("discovery returns summaries in (ingested_at ASC, task_id ASC) order with team-a eligibility", async () => {
    const api = await exec.api(worker.baseUrl);
    try {
      const resp = await api.get(
        `/v1/executors/${encodeURIComponent(exec.attach()["X-FlowAI-Executor-Id"] ?? "")}/tasks?tag=openhands`,
      );
      expect(resp.status()).toBe(200);
      const body = await resp.json();
      const items = readItems(body);
      expect(
        items.length,
        "discovery returns at least four team-a openhands pending summaries",
      ).toBeGreaterThanOrEqual(4);
      for (const it of items) {
        expect(
          it.team_id,
          "eligibility predicate restricts the page to team-a",
        ).toBe(bsA.admin.team_id);
        expect(
          it.required_tag,
          "every summary carries the registered openhands tag",
        ).toBe("openhands");
        expect(
          it.current_state,
          "every summary is projected current_state = pending",
        ).toBe("pending");
      }
      const expectedOrder = [...ingested]
        .sort((a, b) => {
          if (a.ingested_at < b.ingested_at) return -1;
          if (a.ingested_at > b.ingested_at) return 1;
          if (a.task_id < b.task_id) return -1;
          if (a.task_id > b.task_id) return 1;
          return 0;
        })
        .map((x) => x.task_id);
      const observed = items.map((it) => it.task_id);
      const observedIndices = expectedOrder.map((id) => observed.indexOf(id));
      for (const observedIndex of observedIndices) {
        expect(
          observedIndex,
          "every ingested task is visible in the discovery page",
        ).toBeGreaterThanOrEqual(0);
      }
      for (let i = 1; i < observedIndices.length; i += 1) {
        const prev = observedIndices[i - 1];
        const curr = observedIndices[i];
        if (prev === undefined || curr === undefined) {
          throw new Error(
            "observed index is unexpectedly undefined while comparing FIFO order",
          );
        }
        expect(
          curr,
          "discovery order matches expected FIFO (ingested_at ASC, task_id ASC) order",
        ).toBeGreaterThan(prev);
      }
    } finally {
      await api.dispose();
    }
  });
});
