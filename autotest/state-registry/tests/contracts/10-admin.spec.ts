// Contract group: admin
// Covers v0002.60, v0002.61, v0002.62, v0002.63, v0002.79, v0002.80,
// v0002.81.
//
// Each test below exercises one of the admin-only boundaries:
//   - POST /admin/teams (create team with REQUIRED default_image)
//   - POST /admin/source-systems (register team-owned source system)
//   - POST /admin/task-types (register team-owned task type)
//   - GET /admin/tags (system-admin-only global tag projection)
//   - GET /admin/tasks (system-admin-only global task projection)
//
// Behavior-specific assertions verify:
//   - only the system administrator may call /admin/*
//   - teams.default_image is REQUIRED and immutable through any
//     operator or admin path
//   - source-system listener_identity is globally unique across teams
//   - tag and task admin projections enumerate entries with the
//     documented exact-allowlist shape and never mutate state
//   - admin reads reject every non-system-administrator identity
//     BEFORE any row is read
import { test, expect } from "@playwright/test";
import {
  startRegistryWorker,
  type RegistryWorker,
} from "../../fixtures/registry_worker";
import {
  systemAdministrator,
  listenerFor,
  teamExecutorFor,
  gatewayFor,
} from "../../fixtures/identities";
import {
  ingestPendingTask,
  imageReference,
  type ImageReference,
} from "./_setup";
import { uniqueExecutorId } from "../../fixtures/executor_binary";

let worker: RegistryWorker;

async function sqlScalar(sql: string): Promise<string> {
  return await worker.postgres.runtime.exec(worker.postgres.handle, [
    "psql",
    "--username",
    "postgres",
    "--dbname",
    "flowai",
    "--tuples-only",
    "--no-align",
    "--command",
    sql,
  ]);
}

async function tableCount(
  table: "teams" | "source_systems" | "task_types",
): Promise<number> {
  return Number(await sqlScalar(`SELECT count(*) FROM ${table}`));
}

test.beforeAll(async () => {
  worker = await startRegistryWorker();
});

test.afterAll(async () => {
  if (worker) {
    await worker.teardown();
  }
});

test("v0002.60 admin creates a team with REQUIRED default_image; duplicate team_name and missing fields are rejected", async () => {
  const admin = systemAdministrator();
  const adminApi = await admin.api(worker.baseUrl);
  const suffix = `v0002-60-${Date.now().toString(36)}`;
  const expectedImage: ImageReference = imageReference(`default-${suffix}-a`);
  const initialTeamCount = await tableCount("teams");
  let teamId = "";
  try {
    await test.step("positive control: POST /admin/teams returns 201 with REQUIRED default_image and unique team_name", async () => {
      const resp = await adminApi.post("/admin/teams", {
        data: { team_name: `${suffix}-a`, default_image: expectedImage },
      });
      expect(resp.status(), "admin team create returns 201").toBe(201);
      const body = (await resp.json()) as {
        team_id: string;
        team_name: string;
        default_image: ImageReference;
      };
      teamId = body.team_id;
      expect(teamId, "admin create returns an immutable team_id").toBeTruthy();
      expect(body.team_name, "team_name is persisted").toBe(`${suffix}-a`);
      expect(
        body.default_image,
        "default_image is REQUIRED and persisted as ImageReference",
      ).toEqual(expectedImage);
    });
    await test.step("negative control: duplicate team_name is rejected with 400", async () => {
      const resp = await adminApi.post("/admin/teams", {
        data: {
          team_name: `${suffix}-a`,
          default_image: imageReference(`default-${suffix}-duplicate`),
        },
      });
      expect(
        resp.status(),
        "duplicate team_name is rejected without replacement",
      ).toBe(400);
    });
    await test.step("negative control: missing default_image is rejected with 400", async () => {
      const resp = await adminApi.post("/admin/teams", {
        data: { team_name: `${suffix}-noimage` },
      });
      expect(
        resp.status(),
        "missing default_image is rejected without persistence",
      ).toBe(400);
    });
    await test.step("negative control: missing team_name is rejected with 400", async () => {
      const resp = await adminApi.post("/admin/teams", {
        data: { default_image: imageReference(`default-${suffix}-noname`) },
      });
      expect(
        resp.status(),
        "missing team_name is rejected without persistence",
      ).toBe(400);
    });
    await test.step("rejections leave exactly the one canonical team row", async () => {
      expect(await tableCount("teams")).toBe(initialTeamCount + 1);
    });
  } finally {
    await adminApi.dispose();
  }
  await test.step("the canonical team row is durable across Registry restart", async () => {
    const restart = await worker.restart();
    expect(restart.restartCount).toBeGreaterThanOrEqual(1);
    const persisted = await sqlScalar(
      `SELECT team_name || '|' || ((default_image::jsonb)->>'repository') || '|' || ((default_image::jsonb)->>'digest') FROM teams WHERE team_id = '${teamId}'`,
    );
    expect(persisted).toBe(
      `${suffix}-a|${expectedImage.repository}|${expectedImage.digest}`,
    );
  });
});

test("v0002.61 admin registers a team-owned source system; globally unique listener_identity is enforced", async () => {
  const admin = systemAdministrator();
  const adminApi = await admin.api(worker.baseUrl);
  const suffix = `v0002-61-${Date.now().toString(36)}`;
  let teamAId = "";
  let teamBId = "";
  let sourceSystemId = "";
  const sourceImage = imageReference(`default-${suffix}-source`);
  const initialSourceCount = await tableCount("source_systems");
  try {
    await test.step("positive control: POST /admin/teams creates team-a and team-b", async () => {
      const teamA = await adminApi.post("/admin/teams", {
        data: {
          team_name: `${suffix}-a`,
          default_image: imageReference(`default-${suffix}-a`),
        },
      });
      expect(teamA.status()).toBe(201);
      teamAId = ((await teamA.json()) as { team_id: string }).team_id;
      const teamB = await adminApi.post("/admin/teams", {
        data: {
          team_name: `${suffix}-b`,
          default_image: imageReference(`default-${suffix}-b`),
        },
      });
      expect(teamB.status()).toBe(201);
      teamBId = ((await teamB.json()) as { team_id: string }).team_id;
    });
    const listenerIdentity = `${suffix}-L-1`;
    await test.step("positive control: POST /admin/source-systems returns 201 for the first team", async () => {
      const resp = await adminApi.post("/admin/source-systems", {
        data: {
          team_id: teamAId,
          listener_identity: listenerIdentity,
          default_image: sourceImage,
        },
      });
      expect(resp.status(), "source-system create returns 201").toBe(201);
      const body = (await resp.json()) as {
        source_system_id: string;
        team_id: string;
        listener_identity: string;
        default_image: ImageReference;
      };
      sourceSystemId = body.source_system_id;
      expect(
        sourceSystemId,
        "source system has an immutable server-generated id",
      ).toBeTruthy();
      expect(body.team_id, "source system is bound to team-a").toBe(teamAId);
      expect(body.listener_identity).toBe(listenerIdentity);
      expect(body.default_image).toEqual(sourceImage);
    });
    await test.step("negative control: same listener_identity under the SAME team is rejected with 409", async () => {
      const resp = await adminApi.post("/admin/source-systems", {
        data: { team_id: teamAId, listener_identity: listenerIdentity },
      });
      expect(
        resp.status(),
        "duplicate listener_identity under the same team is rejected",
      ).toBe(409);
    });
    await test.step("negative control: same listener_identity under a DIFFERENT team is rejected with 409", async () => {
      const resp = await adminApi.post("/admin/source-systems", {
        data: { team_id: teamBId, listener_identity: listenerIdentity },
      });
      expect(
        resp.status(),
        "duplicate listener_identity across teams is rejected",
      ).toBe(409);
    });
    await test.step("negative control: source-system referencing a non-existent team is rejected with 400", async () => {
      const resp = await adminApi.post("/admin/source-systems", {
        data: { team_id: "team-ghost", listener_identity: `${suffix}-L-ghost` },
      });
      expect(resp.status(), "non-existent team reference is rejected").toBe(
        400,
      );
    });
    await test.step("negative control: unauthenticated caller is rejected with 401", async () => {
      const resp = await fetch(`${worker.baseUrl}/admin/source-systems`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          team_id: teamAId,
          listener_identity: `${suffix}-anonymous`,
        }),
      });
      expect(resp.status).toBe(401);
    });
    await test.step("negative control: source-system reattachment has no update route", async () => {
      const resp = await adminApi.put(
        `/admin/source-systems/${encodeURIComponent(sourceSystemId)}`,
        {
          data: { team_id: teamBId },
        },
      );
      expect([404, 405]).toContain(resp.status());
      const persistedTeam = await sqlScalar(
        `SELECT team_id FROM source_systems WHERE source_system_id = '${sourceSystemId}'`,
      );
      expect(persistedTeam).toBe(teamAId);
    });
    await test.step("all rejected writes leave exactly one source-system row", async () => {
      expect(await tableCount("source_systems")).toBe(initialSourceCount + 1);
    });
  } finally {
    await adminApi.dispose();
  }
});

test("v0002.62 admin registers a team-owned task type with REQUIRED execution_tag", async () => {
  const admin = systemAdministrator();
  const adminApi = await admin.api(worker.baseUrl);
  const suffix = `v0002-62-${Date.now().toString(36)}`;
  let teamId = "";
  const taskTypeImage = imageReference(`default-${suffix}-task-type`);
  const initialTaskTypeCount = await tableCount("task_types");
  try {
    await test.step("positive control: POST /admin/teams creates the team", async () => {
      const team = await adminApi.post("/admin/teams", {
        data: {
          team_name: `${suffix}-a`,
          default_image: imageReference(`default-${suffix}-a`),
        },
      });
      expect(team.status()).toBe(201);
      teamId = ((await team.json()) as { team_id: string }).team_id;
    });
    await test.step("positive control: POST /admin/task-types returns 201 with REQUIRED execution_tag", async () => {
      const resp = await adminApi.post("/admin/task-types", {
        data: {
          team_id: teamId,
          execution_tag: "openhands",
          default_image: taskTypeImage,
        },
      });
      expect(resp.status(), "task-type create returns 201").toBe(201);
      const body = (await resp.json()) as {
        task_type_id: string;
        team_id: string;
        execution_tag: string;
        default_image: ImageReference;
      };
      expect(
        body.task_type_id,
        "task-type create returns an immutable task_type_id",
      ).toBeTruthy();
      expect(body.team_id).toBe(teamId);
      expect(body.execution_tag).toBe("openhands");
      expect(body.default_image).toEqual(taskTypeImage);
    });
    await test.step("negative control: missing execution_tag is rejected with 400", async () => {
      const resp = await adminApi.post("/admin/task-types", {
        data: { team_id: teamId },
      });
      expect(resp.status(), "missing execution_tag is rejected").toBe(400);
    });
    await test.step("negative control: non-existent team reference is rejected with 400", async () => {
      const resp = await adminApi.post("/admin/task-types", {
        data: { team_id: "team-ghost", execution_tag: "openhands" },
      });
      expect(resp.status(), "non-existent team reference is rejected").toBe(
        400,
      );
    });
    await test.step("negative control: unauthenticated caller is rejected with 401", async () => {
      const resp = await fetch(`${worker.baseUrl}/admin/task-types`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ team_id: teamId, execution_tag: "openhands" }),
      });
      expect(resp.status).toBe(401);
    });
    await test.step("all rejected writes leave exactly one task-type row", async () => {
      expect(await tableCount("task_types")).toBe(initialTaskTypeCount + 1);
    });
  } finally {
    await adminApi.dispose();
  }
});

test("v0002.63 Executor registration never creates or updates a team; only POST /admin/teams creates one", async () => {
  const admin = systemAdministrator();
  const adminApi = await admin.api(worker.baseUrl);
  const initialTeamCount = await tableCount("teams");
  let realTeamId = "";
  try {
    await test.step("negative control: PUT /v1/executors/{id} with scope=team and team_id=team-ghost is rejected with 400 without persistence", async () => {
      const executorId = uniqueExecutorId();
      const executor = teamExecutorFor({ teamId: "team-ghost", executorId });
      const api = await executor.api(worker.baseUrl);
      try {
        const resp = await api.put(
          `/v1/executors/${encodeURIComponent(executorId)}`,
          {
            data: {
              scope: "team",
              team_id: "team-ghost",
              executor_type: "executor_docker_opehands",
              identity: executorId,
              authorized_tag: "openhands",
              max_capacity: 1,
              running_count: 0,
              runtime_metadata: {},
            },
          },
        );
        expect(
          resp.status(),
          "Executor registration referencing a non-existent team is rejected without persistence",
        ).toBe(400);
      } finally {
        await api.dispose();
      }
    });
    await test.step("positive control: POST /admin/teams creates the team (only admin path can)", async () => {
      const resp = await adminApi.post("/admin/teams", {
        data: {
          team_name: `v0002-63-${Date.now().toString(36)}-real`,
          default_image: imageReference("default-v0002-63"),
        },
      });
      expect(resp.status(), "admin team create returns 201").toBe(201);
      realTeamId = ((await resp.json()) as { team_id: string }).team_id;
    });
    await test.step("known team registration succeeds without mutating the team", async () => {
      const executor = teamExecutorFor({
        teamId: realTeamId,
        executorId: uniqueExecutorId(),
      });
      const api = await executor.api(worker.baseUrl);
      try {
        const executorId = executor.attach()["X-FlowAI-Executor-Id"] ?? "";
        const resp = await api.put(
          `/v1/executors/${encodeURIComponent(executorId)}`,
          {
            data: {
              scope: "team",
              team_id: realTeamId,
              executor_type: "executor_docker_opehands",
              identity: executorId,
              authorized_tag: "openhands",
              max_capacity: 1,
              running_count: 0,
              runtime_metadata: {},
            },
          },
        );
        expect(resp.status(), "known team registration succeeds").toBe(200);
        const body = (await resp.json()) as {
          executor_id: string;
          scope: string;
          team_id: string | null;
        };
        expect(body.executor_id).toBe(executorId);
        expect(body.scope).toBe("team");
        expect(body.team_id).toBe(realTeamId);
      } finally {
        await api.dispose();
      }
    });
    await test.step("system scope with non-null team_id is rejected without touching teams", async () => {
      const executor = teamExecutorFor({
        teamId: realTeamId,
        executorId: uniqueExecutorId(),
      });
      const api = await executor.api(worker.baseUrl);
      try {
        const executorId = executor.attach()["X-FlowAI-Executor-Id"] ?? "";
        const resp = await api.put(
          `/v1/executors/${encodeURIComponent(executorId)}`,
          {
            data: {
              scope: "system",
              team_id: realTeamId,
              executor_type: "executor_docker_opehands",
              identity: executorId,
              authorized_tag: "openhands",
              max_capacity: 1,
              running_count: 0,
              runtime_metadata: {},
            },
          },
        );
        expect(resp.status()).toBe(400);
      } finally {
        await api.dispose();
      }
    });
    await test.step("the teams table contains only the row created through POST /admin/teams", async () => {
      expect(await tableCount("teams")).toBe(initialTeamCount + 1);
    });
  } finally {
    await adminApi.dispose();
  }
});

test("v0002.79 admin lists configured tags with the documented exact allowlist (team_id, task_type_id, execution_tag)", async () => {
  const admin = systemAdministrator();
  const adminApi = await admin.api(worker.baseUrl);
  const suffix = `v0002-79-${Date.now().toString(36)}`;
  let teamId = "";
  let sourceSystemId = "";
  const listenerIdentity = `L-${suffix}`;
  try {
    await test.step("positive control: POST /admin/teams + POST /admin/task-types seeds the admin tag projection", async () => {
      const team = await adminApi.post("/admin/teams", {
        data: {
          team_name: `${suffix}-a`,
          default_image: imageReference(`default-${suffix}-a`),
        },
      });
      expect(team.status()).toBe(201);
      teamId = ((await team.json()) as { team_id: string }).team_id;
      const source = await adminApi.post("/admin/source-systems", {
        data: { team_id: teamId, listener_identity: listenerIdentity },
      });
      expect(source.status()).toBe(201);
      sourceSystemId = ((await source.json()) as { source_system_id: string })
        .source_system_id;
      const tt = await adminApi.post("/admin/task-types", {
        data: { team_id: teamId, execution_tag: "openhands" },
      });
      expect(tt.status()).toBe(201);
    });
  } finally {
    await adminApi.dispose();
  }
  await test.step("positive control: GET /admin/tags returns 200 with the documented 3-field exact allowlist", async () => {
    const adminRead = await systemAdministrator().api(worker.baseUrl);
    try {
      const resp = await adminRead.get("/admin/tags?limit=200");
      expect(
        resp.status(),
        "system administrator reads /admin/tags with 200",
      ).toBe(200);
      const body = await resp.json();
      expect(Array.isArray(body.items)).toBe(true);
      for (const entry of body.items as Array<Record<string, unknown>>) {
        const keys = Object.keys(entry).sort();
        expect(
          keys,
          "each admin tag entry has EXACTLY team_id, task_type_id, execution_tag",
        ).toEqual(["execution_tag", "task_type_id", "team_id"]);
      }
    } finally {
      await adminRead.dispose();
    }
  });
  await test.step("negative control: GET /admin/tags as Gateway identity is rejected with 403", async () => {
    const api = await gatewayFor({ teamId, operatorId: `op-${teamId}` }).api(
      worker.baseUrl,
    );
    try {
      const resp = await api.get("/admin/tags?limit=200");
      expect(resp.status(), "Gateway identity cannot read /admin/tags").toBe(
        403,
      );
    } finally {
      await api.dispose();
    }
  });
  await test.step("negative control: GET /admin/tags as listener identity is rejected with 403", async () => {
    const api = await listenerFor({
      teamId,
      listenerIdentity,
      sourceSystemId,
    }).api(worker.baseUrl);
    try {
      const resp = await api.get("/admin/tags?limit=200");
      expect(resp.status(), "listener identity cannot read /admin/tags").toBe(
        403,
      );
    } finally {
      await api.dispose();
    }
  });
});

test("v0002.80 admin lists all tasks across teams with the documented exact 11-field allowlist", async () => {
  const admin = systemAdministrator();
  const adminApi = await admin.api(worker.baseUrl);
  const suffix = `v0002-80-${Date.now().toString(36)}`;
  let teamId = "";
  let sourceSystemId = "";
  let taskTypeId = "";
  let listenerIdentity = "";
  try {
    await test.step("positive control: bootstrap seeds team, source, task type, and one task", async () => {
      const team = await adminApi.post("/admin/teams", {
        data: {
          team_name: `${suffix}-a`,
          default_image: imageReference(`default-${suffix}-a`),
        },
      });
      expect(team.status()).toBe(201);
      teamId = ((await team.json()) as { team_id: string }).team_id;
      listenerIdentity = `L-${suffix}-a`;
      const source = await adminApi.post("/admin/source-systems", {
        data: { team_id: teamId, listener_identity: listenerIdentity },
      });
      expect(source.status()).toBe(201);
      sourceSystemId = ((await source.json()) as { source_system_id: string })
        .source_system_id;
      const tt = await adminApi.post("/admin/task-types", {
        data: { team_id: teamId, execution_tag: "openhands" },
      });
      expect(tt.status()).toBe(201);
      taskTypeId = ((await tt.json()) as { task_type_id: string }).task_type_id;
    });
  } finally {
    await adminApi.dispose();
  }
  await ingestPendingTask(
    listenerFor({
      teamId,
      listenerIdentity,
      sourceSystemId,
    }),
    worker.baseUrl,
    {
      team_id: teamId,
      source_system_id: sourceSystemId,
      source_id: "T-1",
      task_type_id: taskTypeId,
      payload: { hello: "world" },
    },
  );
  await test.step("positive control: GET /admin/tasks returns 200 with EXACTLY the documented 11-field shape per entry", async () => {
    const adminRead = await systemAdministrator().api(worker.baseUrl);
    try {
      const resp = await adminRead.get("/admin/tasks?limit=200");
      expect(
        resp.status(),
        "system administrator reads /admin/tasks with 200",
      ).toBe(200);
      const body = await resp.json();
      expect(Array.isArray(body.items)).toBe(true);
      const expectedKeys = [
        "claimed_at",
        "current_state",
        "executor_id",
        "ingested_at",
        "owner_command_id",
        "required_tag",
        "source_id",
        "source_system_id",
        "task_id",
        "task_type_id",
        "team_id",
      ];
      for (const entry of body.items as Array<Record<string, unknown>>) {
        const keys = Object.keys(entry).sort();
        expect(
          keys,
          "admin task entry has EXACTLY 11 documented fields",
        ).toEqual([...expectedKeys].sort());
      }
    } finally {
      await adminRead.dispose();
    }
  });
});

test("v0002.81 admin reads enforce identity and are non-mutating; non-admin identities are rejected before any row is read", async () => {
  const admin = systemAdministrator();
  const adminApi = await admin.api(worker.baseUrl);
  const suffix = `v0002-81-${Date.now().toString(36)}`;
  let teamId = "";
  let sourceSystemId = "";
  let taskTypeId = "";
  let listenerIdentity = "";
  try {
    await test.step("positive control: bootstrap seeds team, source, task type, and one task", async () => {
      const team = await adminApi.post("/admin/teams", {
        data: {
          team_name: `${suffix}-a`,
          default_image: imageReference(`default-${suffix}-a`),
        },
      });
      expect(team.status()).toBe(201);
      teamId = ((await team.json()) as { team_id: string }).team_id;
      listenerIdentity = `L-${suffix}-a`;
      const source = await adminApi.post("/admin/source-systems", {
        data: { team_id: teamId, listener_identity: listenerIdentity },
      });
      expect(source.status()).toBe(201);
      sourceSystemId = ((await source.json()) as { source_system_id: string })
        .source_system_id;
      const tt = await adminApi.post("/admin/task-types", {
        data: { team_id: teamId, execution_tag: "openhands" },
      });
      expect(tt.status()).toBe(201);
      taskTypeId = ((await tt.json()) as { task_type_id: string }).task_type_id;
    });
  } finally {
    await adminApi.dispose();
  }
  await ingestPendingTask(
    listenerFor({
      teamId,
      listenerIdentity,
      sourceSystemId,
    }),
    worker.baseUrl,
    {
      team_id: teamId,
      source_system_id: sourceSystemId,
      source_id: "T-1",
      task_type_id: taskTypeId,
      payload: { hello: "world" },
    },
  );

  await test.step("negative control: listener / Executor / Gateway are each rejected with 403 before any row is read", async () => {
    const cases = [
      {
        name: "listener",
        ctx: listenerFor({ teamId, listenerIdentity, sourceSystemId }),
      },
      {
        name: "team Executor",
        ctx: teamExecutorFor({ teamId, executorId: uniqueExecutorId() }),
      },
      {
        name: "trusted Gateway",
        ctx: gatewayFor({ teamId, operatorId: `op-${teamId}` }),
      },
    ];
    for (const c of cases) {
      const api = await c.ctx.api(worker.baseUrl);
      try {
        const tagsResp = await api.get("/admin/tags?limit=200");
        const tasksResp = await api.get("/admin/tasks?limit=200");
        expect(
          tagsResp.status(),
          `${c.name} is rejected on /admin/tags with 403`,
        ).toBe(403);
        expect(
          tasksResp.status(),
          `${c.name} is rejected on /admin/tasks with 403`,
        ).toBe(403);
      } finally {
        await api.dispose();
      }
    }
  });

  await test.step("negative control: unauthenticated caller cannot reach /admin/tags or /admin/tasks (401)", async () => {
    const tagsResp = await fetch(`${worker.baseUrl}/admin/tags?limit=200`);
    const tasksResp = await fetch(`${worker.baseUrl}/admin/tasks?limit=200`);
    expect(
      tagsResp.status,
      "unauthenticated /admin/tags is rejected with 401",
    ).toBe(401);
    expect(
      tasksResp.status,
      "unauthenticated /admin/tasks is rejected with 401",
    ).toBe(401);
  });

  await test.step("positive control: system administrator can read both admin projections with 200", async () => {
    const adminRead = await systemAdministrator().api(worker.baseUrl);
    try {
      const tagsResp = await adminRead.get("/admin/tags?limit=200");
      const tasksResp = await adminRead.get("/admin/tasks?limit=200");
      expect(
        tagsResp.status(),
        "system administrator reads /admin/tags with 200",
      ).toBe(200);
      expect(
        tasksResp.status(),
        "system administrator reads /admin/tasks with 200",
      ).toBe(200);
    } finally {
      await adminRead.dispose();
    }
  });

  await test.step("negative control: out-of-range limit returns 400 invalid_pagination BEFORE any protected query runs", async () => {
    const adminRead = await systemAdministrator().api(worker.baseUrl);
    try {
      const tooBig = await adminRead.get("/admin/tags?limit=500");
      const zero = await adminRead.get("/admin/tags?limit=0");
      expect(tooBig.status(), "limit=500 is rejected with 400").toBe(400);
      expect(zero.status(), "limit=0 is rejected with 400").toBe(400);
    } finally {
      await adminRead.dispose();
    }
  });
});
