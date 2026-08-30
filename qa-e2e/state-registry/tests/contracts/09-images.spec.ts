// Contract group: images
// Covers v0002.50, v0002.51, v0002.64, v0002.74, v0002.75, v0002.76.
//
// Each test below exercises the four-level image precedence contract:
//   tasks.image -> task_types.default_image ->
//   source_systems.default_image -> teams.default_image.
//
// Behavior-specific assertions verify:
//   - claim response carries resolved_image and image_source from the
//     closest applicable level
//   - teams.default_image is REQUIRED and immutable through any
//     operator or admin path
//   - equal image strings across teams grant no cross-team authority
import { test, expect } from "@playwright/test";
import {
  startRegistryWorker,
  type RegistryWorker,
} from "../../fixtures/registry_worker";
import {
  systemAdministrator,
  teamExecutorFor,
  listenerFor,
} from "../../fixtures/identities";
import {
  ingestPendingTask,
  registerExecutor,
  imageReference,
  dockerPullString,
  type ImageReference,
} from "./_setup";
import { uniqueExecutorId } from "../../fixtures/executor_container";

let worker: RegistryWorker;

test.beforeAll(async () => {
  worker = await startRegistryWorker();
});

test.afterAll(async () => {
  if (worker) {
    await worker.teardown();
  }
});

test("v0002.50 claim response resolves teams.default_image when task_types and source_systems have no default", async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-50-${Date.now().toString(36)}`;
  const adminApi = await admin.api(worker.baseUrl);
  let teamId = "";
  let defaultImage: ImageReference = imageReference(`default-A-${suffix}`);
  let sourceSystemId = "";
  let taskTypeId = "";
  try {
    const team = await adminApi.post("/admin/teams", {
      data: {
        team_name: `${suffix}-a`,
        default_image: dockerPullString(defaultImage),
      },
    });
    expect(team.status()).toBe(201);
    const teamBody = (await team.json()) as {
      team_id: string;
      default_image: string;
    };
    teamId = teamBody.team_id;
    expect(
      teamBody.default_image,
      "admin create returns the persisted ImageReference object",
    ).toEqual(dockerPullString(defaultImage));
    const source = await adminApi.post("/admin/source-systems", {
      data: { team_id: teamId, listener_identity: `L-${suffix}-a` },
    });
    expect(source.status()).toBe(201);
    sourceSystemId = ((await source.json()) as { source_system_id: string })
      .source_system_id;
    const tt = await adminApi.post("/admin/task-types", {
      data: { team_id: teamId, execution_tag: "openhands" },
    });
    expect(tt.status()).toBe(201);
    taskTypeId = ((await tt.json()) as { task_type_id: string }).task_type_id;
  } finally {
    await adminApi.dispose();
  }
  const task = await ingestPendingTask(
    listenerFor({
      teamId,
      listenerIdentity: `L-${suffix}-a`,
      sourceSystemId,
    }),
    worker.baseUrl,
    {
      team_id: teamId,
      source_system_id: sourceSystemId,
      source_id: "T-1",
      task_type_id: taskTypeId,
      payload: {},
    },
  );
  const exec = teamExecutorFor({ teamId, executorId: uniqueExecutorId() });
  await registerExecutor(exec, worker.baseUrl, {
    scope: "team",
    team_id: teamId,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-50",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  await test.step("claim response records resolved_image from teams.default_image with image_source = team_default", async () => {
    const api = await exec.api(worker.baseUrl);
    try {
      const claim = await api.post(
        `/v1/executors/${encodeURIComponent(exec.attach()["X-FlowAI-Executor-Id"] ?? "")}/claim`,
        {
          data: { task_id: task.task_id, command_id: "C-1" },
        },
      );
      expect(
        claim.status(),
        "claim returns 200 with the canonical claim response",
      ).toBe(200);
      const body = (await claim.json()) as {
        resolved_image: ImageReference;
        image_source: string;
        task: { resolved_image: ImageReference; image_source: string };
      };
      expect(
        body.resolved_image,
        "claim resolved_image is the team default object",
      ).toEqual(defaultImage);
      expect(
        body.image_source,
        "image_source is team_default when only the team default applies",
      ).toBe("team_default");
      expect(
        body.task.resolved_image,
        "task envelope resolved_image is the team default object",
      ).toEqual(defaultImage);
      expect(
        body.task.image_source,
        "task envelope image_source is team_default",
      ).toBe("team_default");
    } finally {
      await api.dispose();
    }
  });
});

test("v0002.51 task-level image override wins over team default and is preserved across retries", async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-51-${Date.now().toString(36)}`;
  const adminApi = await admin.api(worker.baseUrl);
  let teamId = "";
  let sourceSystemId = "";
  let taskTypeId = "";
  let listenerIdentity = "";
  try {
    const team = await adminApi.post("/admin/teams", {
      data: {
        team_name: `${suffix}-a`,
        default_image: dockerPullString(imageReference(`default-A-${suffix}`)),
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
  } finally {
    await adminApi.dispose();
  }
  const listener = listenerFor({
    teamId,
    listenerIdentity,
    sourceSystemId,
  });
  const overrideRef = imageReference(`override-B-${suffix}`, "b");
  const firstApi = await listener.api(worker.baseUrl);
  let firstTaskId = "";
  let firstImage: ImageReference = overrideRef;
  try {
    const resp = await firstApi.post("/v1/tasks", {
      data: {
        team_id: teamId,
        source_system_id: sourceSystemId,
        source_id: "T-51",
        task_type_id: taskTypeId,
        payload: { hello: "world" },
        image: overrideRef,
      },
    });
    expect(
      resp.status(),
      "first POST /v1/tasks creates the canonical pending task with 201",
    ).toBe(201);
    const body = (await resp.json()) as {
      task_id: string;
      image: ImageReference;
    };
    firstTaskId = body.task_id;
    firstImage = body.image;
  } finally {
    await firstApi.dispose();
  }
  await test.step("listener retry preserves the original override and does NOT replace it (returns 200)", async () => {
    const retryApi = await listener.api(worker.baseUrl);
    try {
      const retry = await retryApi.post("/v1/tasks", {
        data: {
          team_id: teamId,
          source_system_id: sourceSystemId,
          source_id: "T-51",
          task_type_id: taskTypeId,
          payload: { hello: "world" },
          image: imageReference(`override-C-${suffix}`, "c"),
        },
      });
      expect(
        retry.status(),
        "idempotent retry returns 200 with the existing canonical task",
      ).toBe(200);
      const body = (await retry.json()) as {
        task_id: string;
        image: ImageReference;
      };
      expect(body.task_id, "retry returns the same canonical task").toBe(
        firstTaskId,
      );
      expect(body.image, "image override is immutable across retries").toEqual(
        firstImage,
      );
    } finally {
      await retryApi.dispose();
    }
  });
});

test("v0002.64 teams.default_image is REQUIRED at admin registration and immutable", async () => {
  const admin = systemAdministrator();
  const adminApi = await admin.api(worker.baseUrl);
  try {
    await test.step("POST /admin/teams without default_image is rejected with 400", async () => {
      const resp = await adminApi.post("/admin/teams", {
        data: { team_name: `v0002-64-${Date.now().toString(36)}-noimage` },
      });
      expect(resp.status(), "missing default_image is rejected").toBe(400);
    });
  } finally {
    await adminApi.dispose();
  }
});

test("v0002.74 task_types.default_image wins when tasks.image is null", async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-74-${Date.now().toString(36)}`;
  const adminApi = await admin.api(worker.baseUrl);
  let teamId = "";
  let sourceSystemId = "";
  let taskTypeId = "";
  let listenerIdentity = "";
  let taskTypeDefaultImage: ImageReference = imageReference(
    `default-TT-${suffix}`,
  );
  try {
    const team = await adminApi.post("/admin/teams", {
      data: {
        team_name: `${suffix}-a`,
        default_image: dockerPullString(imageReference(`default-A-${suffix}`)),
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
      data: {
        team_id: teamId,
        execution_tag: "openhands",
        default_image: taskTypeDefaultImage,
      },
    });
    expect(tt.status()).toBe(201);
    taskTypeId = ((await tt.json()) as { task_type_id: string }).task_type_id;
  } finally {
    await adminApi.dispose();
  }
  const task = await ingestPendingTask(
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
      payload: {},
    },
  );
  const exec = teamExecutorFor({ teamId, executorId: uniqueExecutorId() });
  await registerExecutor(exec, worker.baseUrl, {
    scope: "team",
    team_id: teamId,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-74",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  await test.step("claim response carries resolved_image from task_types.default_image with image_source = task_type_default", async () => {
    const api = await exec.api(worker.baseUrl);
    try {
      const claim = await api.post(
        `/v1/executors/${encodeURIComponent(exec.attach()["X-FlowAI-Executor-Id"] ?? "")}/claim`,
        {
          data: { task_id: task.task_id, command_id: "C-1" },
        },
      );
      expect(
        claim.status(),
        "claim returns 200 with the canonical claim response",
      ).toBe(200);
      const body = (await claim.json()) as {
        resolved_image: ImageReference;
        image_source: string;
      };
      expect(
        body.image_source,
        "task_types.default_image wins over team default",
      ).toBe("task_type_default");
      expect(
        body.resolved_image,
        "resolved_image is the task-type default object",
      ).toEqual(taskTypeDefaultImage);
    } finally {
      await api.dispose();
    }
  });
});

test("v0002.75 source_systems.default_image wins when tasks.image and task_types.default_image are null", async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-75-${Date.now().toString(36)}`;
  const adminApi = await admin.api(worker.baseUrl);
  let teamId = "";
  let sourceSystemId = "";
  let taskTypeId = "";
  let listenerIdentity = "";
  let sourceSystemDefaultImage: ImageReference = imageReference(
    `default-SS-${suffix}`,
  );
  try {
    const team = await adminApi.post("/admin/teams", {
      data: {
        team_name: `${suffix}-a`,
        default_image: dockerPullString(imageReference(`default-A-${suffix}`)),
      },
    });
    expect(team.status()).toBe(201);
    teamId = ((await team.json()) as { team_id: string }).team_id;
    listenerIdentity = `L-${suffix}-a`;
    const source = await adminApi.post("/admin/source-systems", {
      data: {
        team_id: teamId,
        listener_identity: listenerIdentity,
        default_image: sourceSystemDefaultImage,
      },
    });
    expect(source.status()).toBe(201);
    sourceSystemId = ((await source.json()) as { source_system_id: string })
      .source_system_id;
    const tt = await adminApi.post("/admin/task-types", {
      data: { team_id: teamId, execution_tag: "openhands" },
    });
    expect(tt.status()).toBe(201);
    taskTypeId = ((await tt.json()) as { task_type_id: string }).task_type_id;
  } finally {
    await adminApi.dispose();
  }
  const task = await ingestPendingTask(
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
      payload: {},
    },
  );
  const exec = teamExecutorFor({ teamId, executorId: uniqueExecutorId() });
  await registerExecutor(exec, worker.baseUrl, {
    scope: "team",
    team_id: teamId,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-75",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  await test.step("claim response carries resolved_image from source_systems.default_image with image_source = source_system_default", async () => {
    const api = await exec.api(worker.baseUrl);
    try {
      const claim = await api.post(
        `/v1/executors/${encodeURIComponent(exec.attach()["X-FlowAI-Executor-Id"] ?? "")}/claim`,
        {
          data: { task_id: task.task_id, command_id: "C-1" },
        },
      );
      expect(
        claim.status(),
        "claim returns 200 with the canonical claim response",
      ).toBe(200);
      const body = (await claim.json()) as {
        resolved_image: ImageReference;
        image_source: string;
      };
      expect(
        body.image_source,
        "source_systems.default_image wins over team default",
      ).toBe("source_system_default");
      expect(
        body.resolved_image,
        "resolved_image is the source-system default object",
      ).toEqual(sourceSystemDefaultImage);
    } finally {
      await api.dispose();
    }
  });
});

test("v0002.76 equal image strings across teams grant no cross-team authority", async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-76-${Date.now().toString(36)}`;
  const sharedImage: ImageReference = imageReference(`shared-${suffix}`);
  const adminApi = await admin.api(worker.baseUrl);
  let teamAId = "";
  let teamBId = "";
  let sourceSystemBId = "";
  let taskTypeBId = "";
  let listenerB = "";
  try {
    const teamA = await adminApi.post("/admin/teams", {
      data: {
        team_name: `${suffix}-a`,
        default_image: dockerPullString(sharedImage),
      },
    });
    expect(teamA.status()).toBe(201);
    teamAId = ((await teamA.json()) as { team_id: string }).team_id;
    const teamB = await adminApi.post("/admin/teams", {
      data: {
        team_name: `${suffix}-b`,
        default_image: dockerPullString(sharedImage),
      },
    });
    expect(teamB.status()).toBe(201);
    teamBId = ((await teamB.json()) as { team_id: string }).team_id;
    listenerB = `L-${suffix}-b`;
    const sourceB = await adminApi.post("/admin/source-systems", {
      data: { team_id: teamBId, listener_identity: listenerB },
    });
    expect(sourceB.status()).toBe(201);
    sourceSystemBId = ((await sourceB.json()) as { source_system_id: string })
      .source_system_id;
    const ttB = await adminApi.post("/admin/task-types", {
      data: { team_id: teamBId, execution_tag: "openhands" },
    });
    expect(ttB.status()).toBe(201);
    taskTypeBId = ((await ttB.json()) as { task_type_id: string }).task_type_id;
  } finally {
    await adminApi.dispose();
  }
  const teamBTask = await ingestPendingTask(
    listenerFor({
      teamId: teamBId,
      listenerIdentity: listenerB,
      sourceSystemId: sourceSystemBId,
    }),
    worker.baseUrl,
    {
      team_id: teamBId,
      source_system_id: sourceSystemBId,
      source_id: "B-1",
      task_type_id: taskTypeBId,
      payload: {},
    },
  );
  const exec = teamExecutorFor({
    teamId: teamAId,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(exec, worker.baseUrl, {
    scope: "team",
    team_id: teamAId,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-76",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  await test.step("team-a Executor attempting to claim a team-b task returns non-revealing 404 even though default_image objects are structurally equal", async () => {
    const api = await exec.api(worker.baseUrl);
    try {
      const resp = await api.post(
        `/v1/executors/${encodeURIComponent(exec.attach()["X-FlowAI-Executor-Id"] ?? "")}/claim`,
        {
          data: { task_id: teamBTask.task_id, command_id: "C-1" },
        },
      );
      expect(
        resp.status(),
        "equal image objects grant no cross-team authority",
      ).toBe(404);
    } finally {
      await api.dispose();
    }
  });
});
