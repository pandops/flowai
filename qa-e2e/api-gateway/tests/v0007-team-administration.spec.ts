import { expect, test } from "@playwright/test";
import { randomUUID } from "node:crypto";
import pg from "pg";
import { startKeycloakContainer } from "../fixtures/keycloak_container.js";
import { startPostgresContainer } from "../fixtures/postgres_container.js";
import {
  runStateRegistryExpectStartupFailure,
  startStateRegistryContainer,
} from "../fixtures/state_registry_container.js";
import { runGatewayExpectStartupFailure } from "../fixtures/gateway_container.js";
import { AdminJWTProviderFixture } from "../fixtures/harness.js";
import { TestControlClient } from "../fixtures/generated/test-control/client.js";

test("v0007.33 validates the complete State Registry administrator JWT matrix", async () => {
  const identity = new AdminJWTProviderFixture();
  await identity.start();
  identity.addKey("admin-b");
  const postgres = await startPostgresContainer();
  const registry = await startStateRegistryContainer(
    postgres.connectionString,
    {
      issuer: identity.issuer,
      audience: "flowai-api-gateway-admin",
      jwksURL: `${identity.issuer}/jwks`,
      jwksTimeout: "200ms",
      tokenMaxAge: "1m",
    },
  );
  const database = new pg.Pool({ connectionString: postgres.connectionString });
  const image = `registry.example/admin/matrix@sha256:${"9".repeat(64)}`;
  const call = (
    token: string,
    operation: "create" | "update" | "archive",
    teamID = "00000000-0000-0000-0000-000000000000",
  ) => {
    const target =
      operation === "create"
        ? `${registry.baseURL}/admin/teams`
        : `${registry.baseURL}/admin/teams/${teamID}${operation === "archive" ? "/archive" : ""}`;
    return fetch(target, {
      method: operation === "update" ? "PATCH" : "POST",
      headers: {
        authorization: `Bearer ${token}`,
        "content-type": "application/json",
      },
      body:
        operation === "archive"
          ? undefined
          : JSON.stringify(
              operation === "create"
                ? {
                    team_name: `Rejected ${randomUUID()}`,
                    default_image: image,
                  }
                : { team_name: `Rejected ${randomUUID()}` },
            ),
    });
  };
  try {
    const valid = identity.token();
    const createdResponse = await call(valid, "create");
    expect(createdResponse.status).toBe(201);
    const created = (await createdResponse.json()) as { team_id: string };
    const now = Math.floor(Date.now() / 1000);
    const invalid = [
      identity.token({ issuer: `${identity.issuer}/wrong` }),
      identity.token({ audience: "wrong-audience" }),
      identity.token({ alg: "RS512" }),
      identity.token({ signWithKid: "admin-b" }),
      identity.token({ kid: "unknown", signWithKid: "admin-a" }),
      identity.token({ iat: null }),
      identity.token({ iat: now - 120, exp: now + 300 }),
      identity.token({ iat: now - 120, exp: now - 120 }),
    ];
    for (const token of invalid)
      for (const operation of ["create", "update", "archive"] as const) {
        const response = await call(token, operation, created.team_id);
        expect(
          response.status,
          `${operation}: ${await response.clone().text()}`,
        ).toBe(401);
        expect(((await response.json()) as { code: string }).code).toBe(
          "invalid_admin_token",
        );
      }
    for (const roles of [[], ["flowai-system-admin-extra"]])
      for (const operation of ["create", "update", "archive"] as const) {
        const response = await call(
          identity.token({ roles }),
          operation,
          created.team_id,
        );
        expect(response.status).toBe(403);
        expect(((await response.json()) as { code: string }).code).toBe(
          "insufficient_admin_role",
        );
      }
    for (const operation of ["create", "update", "archive"] as const) {
      const malformedRole = await call(
        identity.token({ roles: "flowai-system-admin" }),
        operation,
        created.team_id,
      );
      expect(malformedRole.status).toBe(401);
      expect(((await malformedRole.json()) as { code: string }).code).toBe(
        "invalid_admin_token",
      );
    }
    identity.publish("admin-a", "admin-b");
    const rotated = await call(identity.token({ kid: "admin-b" }), "create");
    expect(rotated.status).toBe(201);
    identity.setFailure({ kind: "status", status: 503 });
    expect((await call(valid, "create")).status).toBe(201);
    const unavailable = await call(
      identity.token({ kid: "missing-outage", signWithKid: "admin-a" }),
      "create",
    );
    expect(unavailable.status).toBe(502);
    expect(((await unavailable.json()) as { code: string }).code).toBe(
      "admin_identity_provider_unavailable",
    );
    identity.setFailure({ kind: "timeout", delayMs: 600 });
    const timeout = await call(
      identity.token({ kid: "missing-timeout", signWithKid: "admin-a" }),
      "archive",
      created.team_id,
    );
    expect(timeout.status).toBe(502);
    expect(((await timeout.json()) as { code: string }).code).toBe(
      "admin_identity_provider_unavailable",
    );
    expect(
      (
        await database.query(
          "SELECT team_name, archived_at FROM teams ORDER BY team_name",
        )
      ).rows,
    ).toEqual([
      { team_name: expect.stringMatching(/^Rejected /), archived_at: null },
      { team_name: expect.stringMatching(/^Rejected /), archived_at: null },
      { team_name: expect.stringMatching(/^Rejected /), archived_at: null },
    ]);
    expect(
      (
        await database.query(
          "SELECT action, count(*)::int AS count FROM audit_entries GROUP BY action ORDER BY action",
        )
      ).rows,
    ).toEqual([{ action: "team.create", count: 3 }]);
  } finally {
    await database.end();
    await registry.close();
    await postgres.close();
    await identity.stop();
  }
});

test("v0007.28-v0007.30 v0007.33 v0007.40 v0007.41 v0007.43 administer canonical team lifecycle", async () => {
  const keycloak = await startKeycloakContainer();
  const postgres = await startPostgresContainer();
  const registry = await startStateRegistryContainer(
    postgres.connectionString,
    {
      issuer: keycloak.issuer,
      audience: "flowai-api-gateway-admin",
      jwksURL: keycloak.discovery.jwks_uri,
    },
  );
  const database = new pg.Pool({ connectionString: postgres.connectionString });
  try {
    const adminToken = await issueAdminToken(keycloak.discovery.token_endpoint);
    const headers = {
      authorization: `Bearer ${adminToken}`,
      "content-type": "application/json",
    };
    const digestA = `registry.example/flowai/agent@sha256:${"a".repeat(64)}`;
    const createdResponse = await fetch(`${registry.baseURL}/admin/teams`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        team_name: "Original Team",
        default_image: digestA,
      }),
    });
    expect(createdResponse.status).toBe(201);
    const created = (await createdResponse.json()) as {
      team_id: string;
      team_name: string;
      default_image: string;
      ingested_at: string;
      archived_at: string | null;
    };
    expect(created).toEqual({
      team_id: expect.any(String),
      team_name: "Original Team",
      default_image: digestA,
      ingested_at: expect.any(String),
      archived_at: null,
    });

    expect(
      (await fetch(`${registry.baseURL}/admin/teams/${created.team_id}`))
        .status,
    ).toBe(401);
    expect(
      (
        await fetch(`${registry.baseURL}/admin/teams/${created.team_id}`, {
          headers: { authorization: "Bearer invalid" },
        })
      ).status,
    ).toBe(401);
    const internal = await fetch(
      `${registry.baseURL}/internal/v1/teams/${created.team_id}`,
      {
        headers: {
          authorization:
            "Bearer deliberately-ignored-on-distinct-internal-route",
        },
      },
    );
    expect(internal.status).toBe(200);
    expect(await internal.json()).toEqual(created);

    const digestB = `registry.example/flowai/agent@sha512:${"b".repeat(128)}`;
    const update = await fetch(
      `${registry.baseURL}/admin/teams/${created.team_id}`,
      {
        method: "PATCH",
        headers,
        body: JSON.stringify({
          team_name: "Renamed Team",
          default_image: digestB,
        }),
      },
    );
    expect(update.status).toBe(200);
    expect(await update.json()).toMatchObject({
      team_id: created.team_id,
      team_name: "Renamed Team",
      default_image: digestB,
      ingested_at: created.ingested_at,
      archived_at: null,
    });

    const reused = await fetch(`${registry.baseURL}/admin/teams`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        team_name: "Original Team",
        default_image: digestA,
      }),
    });
    expect(reused.status).toBe(201);
    const conflict = await fetch(`${registry.baseURL}/admin/teams`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        team_name: "Renamed Team",
        default_image: digestA,
      }),
    });
    expect(conflict.status).toBe(409);
    expect(((await conflict.json()) as { code: string }).code).toBe(
      "team_conflict",
    );

    expect(
      (
        await fetch(`${registry.testControlURL}/test-control/v1/barriers`, {
          method: "POST",
          headers: {
            authorization: "Bearer wrong",
            "content-type": "application/json",
          },
          body: JSON.stringify({ name: "team_archive_after_lock" }),
        })
      ).status,
    ).toBe(401);
    const controlHeaders = {
      authorization: `Bearer ${registry.testControlToken}`,
      "content-type": "application/json",
    };
    const armedResponse = await fetch(
      `${registry.testControlURL}/test-control/v1/barriers`,
      {
        method: "POST",
        headers: controlHeaders,
        body: JSON.stringify({ name: "team_archive_after_lock" }),
      },
    );
    expect(armedResponse.status).toBe(201);
    const barrier = (await armedResponse.json()) as {
      barrier_id: string;
      state: string;
    };
    expect(barrier.state).toBe("armed");
    expect(
      (
        await fetch(`${registry.testControlURL}/test-control/v1/barriers`, {
          method: "POST",
          headers: controlHeaders,
          body: JSON.stringify({ name: "team_archive_after_lock" }),
        })
      ).status,
    ).toBe(409);
    const archivePromise = fetch(
      `${registry.baseURL}/admin/teams/${created.team_id}/archive`,
      { method: "POST", headers },
    );
    const reachedResponse = await fetch(
      `${registry.testControlURL}/test-control/v1/barriers/${barrier.barrier_id}?wait_ms=5000`,
      { headers: controlHeaders },
    );
    expect(reachedResponse.status).toBe(200);
    expect(((await reachedResponse.json()) as { state: string }).state).toBe(
      "reached",
    );
    const stillActive = await fetch(
      `${registry.baseURL}/internal/v1/teams/${created.team_id}`,
    );
    expect(
      ((await stillActive.json()) as { archived_at: string | null })
        .archived_at,
    ).toBeNull();
    const released = await fetch(
      `${registry.testControlURL}/test-control/v1/barriers/${barrier.barrier_id}/release`,
      { method: "POST", headers: controlHeaders },
    );
    expect(released.status).toBe(200);
    expect(((await released.json()) as { state: string }).state).toBe(
      "released",
    );
    const archive = await archivePromise;
    expect(archive.status).toBe(201);
    const archived = (await archive.json()) as { archived_at: string };
    const retry = await fetch(
      `${registry.baseURL}/admin/teams/${created.team_id}/archive`,
      { method: "POST", headers },
    );
    expect(retry.status).toBe(200);
    expect(((await retry.json()) as { archived_at: string }).archived_at).toBe(
      archived.archived_at,
    );
    expect(
      (
        await fetch(`${registry.baseURL}/admin/teams/${created.team_id}`, {
          method: "DELETE",
          headers,
        })
      ).status,
    ).toBe(405);
    expect(
      (
        await fetch(
          `${registry.baseURL}/admin/teams/${created.team_id}/unarchive`,
          { method: "POST", headers },
        )
      ).status,
    ).toBe(404);
    expect(
      (
        await fetch(
          `${registry.testControlURL}/test-control/v1/barriers/${barrier.barrier_id}`,
          { method: "DELETE", headers: controlHeaders },
        )
      ).status,
    ).toBe(204);
    expect(
      (
        await fetch(
          `${registry.testControlURL}/test-control/v1/barriers/${barrier.barrier_id}`,
          { headers: controlHeaders },
        )
      ).status,
    ).toBe(404);

    const rows = await database.query(
      `SELECT team_name, default_image, ingested_at, archived_at FROM teams WHERE team_id=$1`,
      [created.team_id],
    );
    expect(rows.rows).toHaveLength(1);
    expect(rows.rows[0]).toMatchObject({
      team_name: "Renamed Team",
      default_image: digestB,
    });
    expect(rows.rows[0].archived_at).not.toBeNull();
    const audit = await database.query(
      `SELECT audit.action, details.old_team_name, details.new_team_name, details.old_default_image, details.new_default_image FROM audit_entries audit JOIN team_audit_details details USING (audit_id) WHERE audit.team_id=$1 ORDER BY audit.occurred_at, audit.audit_id`,
      [created.team_id],
    );
    expect(audit.rows.map((row) => row.action)).toEqual([
      "team.create",
      "team.update",
      "team.archive",
    ]);
    expect(audit.rows[1]).toMatchObject({
      old_team_name: "Original Team",
      new_team_name: "Renamed Team",
      old_default_image: digestA,
      new_default_image: digestB,
    });
  } finally {
    await database.end();
    await registry.close();
    await postgres.close();
    await keycloak.close();
  }
});

test("v0007.31 v0007.35 v0007.36 v0007.39 v0007.42 reach every production transaction barrier", async () => {
  const keycloak = await startKeycloakContainer();
  const postgres = await startPostgresContainer();
  const registry = await startStateRegistryContainer(
    postgres.connectionString,
    {
      issuer: keycloak.issuer,
      audience: "flowai-api-gateway-admin",
      jwksURL: keycloak.discovery.jwks_uri,
    },
  );
  try {
    const adminToken = await issueAdminToken(keycloak.discovery.token_endpoint);
    const adminHeaders = {
      authorization: `Bearer ${adminToken}`,
      "content-type": "application/json",
    };
    const controlHeaders = {
      authorization: `Bearer ${registry.testControlToken}`,
      "content-type": "application/json",
    };
    const imageA = `registry.example/barrier/agent@sha256:${"c".repeat(64)}`;
    const imageB = `registry.example/barrier/agent@sha256:${"d".repeat(64)}`;

    const createBarrier = await armBarrier(
      registry.testControlURL,
      controlHeaders,
      "team_name_write_after_constraint_before_commit",
    );
    const createPromise = fetch(`${registry.baseURL}/admin/teams`, {
      method: "POST",
      headers: adminHeaders,
      body: JSON.stringify({
        team_name: "Barrier Team",
        default_image: imageA,
      }),
    });
    await reachAndRelease(
      registry.testControlURL,
      controlHeaders,
      createBarrier,
    );
    const createdResponse = await createPromise;
    expect(createdResponse.status).toBe(201);
    const team = (await createdResponse.json()) as { team_id: string };

    const imageBarrier = await armBarrier(
      registry.testControlURL,
      controlHeaders,
      "team_default_image_update_after_lock",
    );
    const imagePromise = fetch(
      `${registry.baseURL}/admin/teams/${team.team_id}`,
      {
        method: "PATCH",
        headers: adminHeaders,
        body: JSON.stringify({ default_image: imageB }),
      },
    );
    await reachAndRelease(
      registry.testControlURL,
      controlHeaders,
      imageBarrier,
    );
    expect((await imagePromise).status).toBe(200);

    const sourceResponse = await fetch(
      `${registry.baseURL}/admin/source-systems`,
      {
        method: "POST",
        headers: adminHeaders,
        body: JSON.stringify({
          team_id: team.team_id,
          listener_identity: "listener-barrier",
        }),
      },
    );
    expect(sourceResponse.status).toBe(201);
    const source = (await sourceResponse.json()) as {
      source_system_id: string;
    };
    const typeResponse = await fetch(`${registry.baseURL}/admin/task-types`, {
      method: "POST",
      headers: adminHeaders,
      body: JSON.stringify({
        team_id: team.team_id,
        execution_tag: "barrier-tag",
      }),
    });
    expect(typeResponse.status).toBe(201);
    const taskType = (await typeResponse.json()) as { task_type_id: string };
    const listenerHeaders = {
      "content-type": "application/json",
      "x-flowai-team-id": team.team_id,
      "x-flowai-source-system-id": source.source_system_id,
      "x-flowai-listener-identity": "listener-barrier",
    };
    const taskInput = {
      team_id: team.team_id,
      source_system_id: source.source_system_id,
      source_id: "barrier-task",
      task_type_id: taskType.task_type_id,
      payload: { barrier: true },
    };
    const ingestBarrier = await armBarrier(
      registry.testControlURL,
      controlHeaders,
      "task_ingest_after_team_lock",
    );
    const ingestPromise = fetch(`${registry.baseURL}/v1/tasks`, {
      method: "POST",
      headers: listenerHeaders,
      body: JSON.stringify(taskInput),
    });
    await reachAndRelease(
      registry.testControlURL,
      controlHeaders,
      ingestBarrier,
    );
    const ingestedResponse = await ingestPromise;
    expect(ingestedResponse.status).toBe(201);
    const task = (await ingestedResponse.json()) as { task_id: string };

    const executorResponse = await fetch(`${registry.baseURL}/v1/executors`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "x-flowai-role": "team-executor",
        "x-flowai-team-id": team.team_id,
        "x-flowai-request-id": randomUUID(),
      },
      body: JSON.stringify({
        scope: "team",
        team_id: team.team_id,
        executor_type: "executor_k8s_openhands",
        authorized_tag: "barrier-tag",
        max_capacity: 1,
        running_count: 0,
        runtime_metadata: { test: "v0007.42" },
      }),
    });
    expect(executorResponse.status).toBe(201);
    const executor = (await executorResponse.json()) as { executor_id: string };
    const claimBarrier = await armBarrier(
      registry.testControlURL,
      controlHeaders,
      "task_claim_after_team_lock",
    );
    const claimPromise = fetch(
      `${registry.baseURL}/v1/executors/${executor.executor_id}/claim`,
      {
        method: "POST",
        headers: {
          "content-type": "application/json",
          "x-flowai-role": "team-executor",
          "x-flowai-team-id": team.team_id,
          "x-flowai-executor-id": executor.executor_id,
          "x-flowai-request-id": randomUUID(),
        },
        body: JSON.stringify({
          task_id: task.task_id,
          command_id: `cmd-${randomUUID()}`,
        }),
      },
    );
    await reachAndRelease(
      registry.testControlURL,
      controlHeaders,
      claimBarrier,
    );
    const claimResponse = await claimPromise;
    expect(claimResponse.status).toBe(200);
    expect(await claimResponse.json()).toMatchObject({
      task: {
        task_id: task.task_id,
        resolved_image: {
          repository: "registry.example/barrier/agent",
          digest: `sha256:${"d".repeat(64)}`,
        },
      },
      image_source: "team_default",
    });
  } finally {
    await registry.close();
    await postgres.close();
    await keycloak.close();
  }
});

test("v0007.34 invalid provider-neutral administrator trust blocks production startup", async () => {
  const gatewayMissing = await runGatewayExpectStartupFailure({
    API_GATEWAY_ADMIN_ISSUER: "https://issuer.invalid",
  });
  expect(gatewayMissing).toContain(
    "admin issuer, audience, JWKS URL, role pointer, and State Registry URL must be configured together",
  );
  const gatewaySymmetric = await runGatewayExpectStartupFailure({
    API_GATEWAY_ADMIN_ISSUER: "https://issuer.invalid",
    API_GATEWAY_ADMIN_AUDIENCE: "admin",
    API_GATEWAY_ADMIN_JWKS_URL: "https://issuer.invalid/jwks",
    API_GATEWAY_ADMIN_ROLE_CLAIM_POINTER: "/roles",
    API_GATEWAY_STATE_REGISTRY_URL: "http://registry.invalid",
    API_GATEWAY_ADMIN_ALGORITHMS: "HS256",
  });
  expect(gatewaySymmetric).toContain(
    "API_GATEWAY_ADMIN_ALGORITHMS must contain only",
  );
  const registryMissing = await runStateRegistryExpectStartupFailure({
    STATE_REGISTRY_ADMIN_ISSUER: "https://issuer.invalid",
  });
  expect(registryMissing).toContain(
    "admin trust configuration must be complete",
  );
  const registrySymmetric = await runStateRegistryExpectStartupFailure({
    STATE_REGISTRY_ADMIN_ISSUER: "https://issuer.invalid",
    STATE_REGISTRY_ADMIN_AUDIENCE: "admin",
    STATE_REGISTRY_ADMIN_JWKS_URL: "https://issuer.invalid/jwks",
    STATE_REGISTRY_ADMIN_ROLE_CLAIM_POINTER: "/roles",
    STATE_REGISTRY_ADMIN_ALGORITHMS: "HS256",
  });
  expect(registrySymmetric).toContain(
    "STATE_REGISTRY_ADMIN_ALGORITHMS must contain only",
  );
  for (const output of [
    gatewayMissing,
    gatewaySymmetric,
    registryMissing,
    registrySymmetric,
  ]) {
    expect(output).not.toContain("token=");
    expect(output).not.toContain("password=");
  }
});

test("v0007.31 v0007.36 v0007.37 v0007.39 serialize both transaction commit orders", async () => {
  const keycloak = await startKeycloakContainer();
  const postgres = await startPostgresContainer();
  const registry = await startStateRegistryContainer(
    postgres.connectionString,
    {
      issuer: keycloak.issuer,
      audience: "flowai-api-gateway-admin",
      jwksURL: keycloak.discovery.jwks_uri,
    },
  );
  const database = new pg.Pool({ connectionString: postgres.connectionString });
  try {
    const adminHeaders = {
      authorization: `Bearer ${await issueAdminToken(keycloak.discovery.token_endpoint)}`,
      "content-type": "application/json",
    };
    const controlHeaders = {
      authorization: `Bearer ${registry.testControlToken}`,
      "content-type": "application/json",
    };
    const ingestFirst = await taskFixture(
      registry.baseURL,
      adminHeaders,
      "Ingest First",
      "1",
    );
    const ingestBarrier = await armBarrier(
      registry.testControlURL,
      controlHeaders,
      "task_ingest_after_team_lock",
    );
    const ingestPromise = ingest(registry.baseURL, ingestFirst, "ingest-first");
    await waitReached(registry.testControlURL, controlHeaders, ingestBarrier);
    const archiveAfterIngest = fetch(
      `${registry.baseURL}/admin/teams/${ingestFirst.teamID}/archive`,
      { method: "POST", headers: adminHeaders },
    );
    await releaseBarrier(
      registry.testControlURL,
      controlHeaders,
      ingestBarrier,
    );
    expect((await ingestPromise).status).toBe(201);
    expect((await archiveAfterIngest).status).toBe(201);
    const archiveFirst = await taskFixture(
      registry.baseURL,
      adminHeaders,
      "Archive First",
      "2",
    );
    expect(
      (await ingest(registry.baseURL, archiveFirst, "existing-before-archive"))
        .status,
    ).toBe(201);
    const archiveBarrier = await armBarrier(
      registry.testControlURL,
      controlHeaders,
      "team_archive_after_lock",
    );
    const archivePromise = fetch(
      `${registry.baseURL}/admin/teams/${archiveFirst.teamID}/archive`,
      { method: "POST", headers: adminHeaders },
    );
    await waitReached(registry.testControlURL, controlHeaders, archiveBarrier);
    const ingestAfterArchive = ingest(
      registry.baseURL,
      archiveFirst,
      "existing-before-archive",
    );
    await releaseBarrier(
      registry.testControlURL,
      controlHeaders,
      archiveBarrier,
    );
    expect((await archivePromise).status).toBe(201);
    expect((await ingestAfterArchive).status).toBe(409);
    expect(
      (
        await database.query(
          "SELECT source_id,count(*)::int AS count FROM tasks WHERE team_id=ANY($1::text[]) GROUP BY source_id ORDER BY source_id",
          [[ingestFirst.teamID, archiveFirst.teamID]],
        )
      ).rows,
    ).toEqual([
      { source_id: "existing-before-archive", count: 1 },
      { source_id: "ingest-first", count: 1 },
    ]);
    for (const teamID of [ingestFirst.teamID, archiveFirst.teamID]) {
      const canonical = await fetch(
        `${registry.baseURL}/internal/v1/teams/${teamID}`,
      );
      expect(canonical.status).toBe(200);
      expect(await canonical.json()).toMatchObject({
        team_id: teamID,
        archived_at: expect.any(String),
      });
    }

    const updateFirst = await claimFixture(
      registry.baseURL,
      adminHeaders,
      "Update First",
      "3",
    );
    const updateBarrier = await armBarrier(
      registry.testControlURL,
      controlHeaders,
      "team_default_image_update_after_lock",
    );
    const newImage = `registry.example/race/agent@sha256:${"f".repeat(64)}`;
    const updatePromise = fetch(
      `${registry.baseURL}/admin/teams/${updateFirst.teamID}`,
      {
        method: "PATCH",
        headers: adminHeaders,
        body: JSON.stringify({ default_image: newImage }),
      },
    );
    await waitReached(registry.testControlURL, controlHeaders, updateBarrier);
    const claimAfterUpdate = claim(registry.baseURL, updateFirst);
    await releaseBarrier(
      registry.testControlURL,
      controlHeaders,
      updateBarrier,
    );
    expect((await updatePromise).status).toBe(200);
    const updatedClaim = await claimAfterUpdate;
    expect(updatedClaim.status).toBe(200);
    expect(await updatedClaim.json()).toMatchObject({
      task: {
        resolved_image: { digest: `sha256:${"f".repeat(64)}` },
        image_source: "team_default",
      },
    });
    await assertPersistedClaim(database, updateFirst, "f", false);
    const claimFirst = await claimFixture(
      registry.baseURL,
      adminHeaders,
      "Claim First",
      "4",
    );
    const claimBarrier = await armBarrier(
      registry.testControlURL,
      controlHeaders,
      "task_claim_after_team_lock",
    );
    const claimPromise = claim(registry.baseURL, claimFirst);
    await waitReached(registry.testControlURL, controlHeaders, claimBarrier);
    const updateAfterClaim = fetch(
      `${registry.baseURL}/admin/teams/${claimFirst.teamID}`,
      {
        method: "PATCH",
        headers: adminHeaders,
        body: JSON.stringify({ default_image: newImage }),
      },
    );
    await releaseBarrier(registry.testControlURL, controlHeaders, claimBarrier);
    const oldClaim = await claimPromise;
    expect(oldClaim.status).toBe(200);
    expect(await oldClaim.json()).toMatchObject({
      task: {
        resolved_image: { digest: `sha256:${"4".repeat(64)}` },
        image_source: "team_default",
      },
    });
    expect((await updateAfterClaim).status).toBe(200);
    await assertPersistedClaim(database, claimFirst, "4", false);

    const claimBeforeArchive = await claimFixture(
      registry.baseURL,
      adminHeaders,
      "Claim Before Archive",
      "5",
    );
    const claimArchiveBarrier = await armBarrier(
      registry.testControlURL,
      controlHeaders,
      "task_claim_after_team_lock",
    );
    const beforeClaim = claim(registry.baseURL, claimBeforeArchive);
    await waitReached(
      registry.testControlURL,
      controlHeaders,
      claimArchiveBarrier,
    );
    const afterArchive = fetch(
      `${registry.baseURL}/admin/teams/${claimBeforeArchive.teamID}/archive`,
      { method: "POST", headers: adminHeaders },
    );
    await releaseBarrier(
      registry.testControlURL,
      controlHeaders,
      claimArchiveBarrier,
    );
    expect((await beforeClaim).status).toBe(200);
    expect((await afterArchive).status).toBe(201);
    const archiveBeforeClaim = await claimFixture(
      registry.baseURL,
      adminHeaders,
      "Archive Before Claim",
      "6",
    );
    const archiveClaimBarrier = await armBarrier(
      registry.testControlURL,
      controlHeaders,
      "team_archive_after_lock",
    );
    const beforeArchive = fetch(
      `${registry.baseURL}/admin/teams/${archiveBeforeClaim.teamID}/archive`,
      { method: "POST", headers: adminHeaders },
    );
    await waitReached(
      registry.testControlURL,
      controlHeaders,
      archiveClaimBarrier,
    );
    const afterClaim = claim(registry.baseURL, archiveBeforeClaim);
    await releaseBarrier(
      registry.testControlURL,
      controlHeaders,
      archiveClaimBarrier,
    );
    expect((await beforeArchive).status).toBe(201);
    expect((await afterClaim).status).toBe(200);
  } finally {
    await database.end();
    await registry.close();
    await postgres.close();
    await keycloak.close();
  }
});

test("v0007.35 serializes create and update writers for every same-name race", async () => {
  const keycloak = await startKeycloakContainer();
  const postgres = await startPostgresContainer();
  const registry = await startStateRegistryContainer(
    postgres.connectionString,
    {
      issuer: keycloak.issuer,
      audience: "flowai-api-gateway-admin",
      jwksURL: keycloak.discovery.jwks_uri,
    },
  );
  const database = new pg.Pool({ connectionString: postgres.connectionString });
  try {
    const headers = {
      authorization: `Bearer ${await issueAdminToken(keycloak.discovery.token_endpoint)}`,
      "content-type": "application/json",
    };
    const control = {
      authorization: `Bearer ${registry.testControlToken}`,
      "content-type": "application/json",
    };
    const createWins = await armBarrier(
      registry.testControlURL,
      control,
      "team_name_write_after_constraint_before_commit",
    );
    const firstCreate = createTeam(
      registry.baseURL,
      headers,
      "Race Create Winner",
      "a",
    );
    await waitReached(registry.testControlURL, control, createWins);
    const losingCreate = createTeam(
      registry.baseURL,
      headers,
      "Race Create Winner",
      "b",
    );
    await releaseBarrier(registry.testControlURL, control, createWins);
    expect((await firstCreate).status).toBe(201);
    expect((await losingCreate).status).toBe(409);

    const updateSeed = await createTeamJSON(
      registry.baseURL,
      headers,
      "Race Update Seed",
      "c",
    );
    const updateWins = await armBarrier(
      registry.testControlURL,
      control,
      "team_name_write_after_constraint_before_commit",
    );
    const firstUpdate = patchTeam(
      registry.baseURL,
      headers,
      updateSeed.team_id,
      "Race Update Winner",
    );
    await waitReached(registry.testControlURL, control, updateWins);
    const losingAfterUpdate = createTeam(
      registry.baseURL,
      headers,
      "Race Update Winner",
      "d",
    );
    await releaseBarrier(registry.testControlURL, control, updateWins);
    expect((await firstUpdate).status).toBe(200);
    expect((await losingAfterUpdate).status).toBe(409);

    const createAgainstUpdateSeed = await createTeamJSON(
      registry.baseURL,
      headers,
      "Race Create Against Update Seed",
      "e",
    );
    const createAgainstUpdate = await armBarrier(
      registry.testControlURL,
      control,
      "team_name_write_after_constraint_before_commit",
    );
    const firstCreateAgainstUpdate = createTeam(
      registry.baseURL,
      headers,
      "Race Create Against Update",
      "f",
    );
    await waitReached(registry.testControlURL, control, createAgainstUpdate);
    const losingUpdate = patchTeam(
      registry.baseURL,
      headers,
      createAgainstUpdateSeed.team_id,
      "Race Create Against Update",
    );
    await releaseBarrier(registry.testControlURL, control, createAgainstUpdate);
    expect((await firstCreateAgainstUpdate).status).toBe(201);
    expect((await losingUpdate).status).toBe(409);

    const updateA = await createTeamJSON(
      registry.baseURL,
      headers,
      "Race Update A",
      "1",
    );
    const updateB = await createTeamJSON(
      registry.baseURL,
      headers,
      "Race Update B",
      "2",
    );
    const updateAgainstUpdate = await armBarrier(
      registry.testControlURL,
      control,
      "team_name_write_after_constraint_before_commit",
    );
    const winningUpdate = patchTeam(
      registry.baseURL,
      headers,
      updateA.team_id,
      "Race Shared Update",
    );
    await waitReached(registry.testControlURL, control, updateAgainstUpdate);
    const losingSharedUpdate = patchTeam(
      registry.baseURL,
      headers,
      updateB.team_id,
      "Race Shared Update",
    );
    await releaseBarrier(registry.testControlURL, control, updateAgainstUpdate);
    expect((await winningUpdate).status).toBe(200);
    expect((await losingSharedUpdate).status).toBe(409);

    const rows = await database.query(
      `SELECT team_name,count(*)::int AS count FROM teams WHERE team_name LIKE 'Race %Winner' OR team_name IN ('Race Create Against Update','Race Shared Update') GROUP BY team_name ORDER BY team_name`,
    );
    expect(rows.rows).toEqual([
      { team_name: "Race Create Against Update", count: 1 },
      { team_name: "Race Create Winner", count: 1 },
      { team_name: "Race Shared Update", count: 1 },
      { team_name: "Race Update Winner", count: 1 },
    ]);
  } finally {
    await database.end();
    await registry.close();
    await postgres.close();
    await keycloak.close();
  }
});

test("v0007.36 v0007.37 v0007.39 cover archived and system-scope reverse commit orders", async () => {
  const keycloak = await startKeycloakContainer();
  const postgres = await startPostgresContainer();
  const registry = await startStateRegistryContainer(
    postgres.connectionString,
    {
      issuer: keycloak.issuer,
      audience: "flowai-api-gateway-admin",
      jwksURL: keycloak.discovery.jwks_uri,
    },
  );
  const database = new pg.Pool({ connectionString: postgres.connectionString });
  try {
    const headers = {
      authorization: `Bearer ${await issueAdminToken(keycloak.discovery.token_endpoint)}`,
      "content-type": "application/json",
    };
    const control = {
      authorization: `Bearer ${registry.testControlToken}`,
      "content-type": "application/json",
    };
    const changed = `registry.example/race/archived@sha256:${"e".repeat(64)}`;
    for (const scope of ["team", "system"] as const) {
      const updateFirst = await claimFixture(
        registry.baseURL,
        headers,
        `Archived Update First ${scope}`,
        scope === "team" ? "7" : "8",
        scope,
      );
      expect(
        (
          await fetch(
            `${registry.baseURL}/admin/teams/${updateFirst.teamID}/archive`,
            { method: "POST", headers },
          )
        ).status,
      ).toBe(201);
      const updateBarrier = await armBarrier(
        registry.testControlURL,
        control,
        "team_default_image_update_after_lock",
      );
      const update = fetch(
        `${registry.baseURL}/admin/teams/${updateFirst.teamID}`,
        {
          method: "PATCH",
          headers,
          body: JSON.stringify({ default_image: changed }),
        },
      );
      await waitReached(registry.testControlURL, control, updateBarrier);
      const afterUpdate = claim(registry.baseURL, updateFirst, scope);
      await releaseBarrier(registry.testControlURL, control, updateBarrier);
      expect((await update).status).toBe(200);
      expect(await (await afterUpdate).json()).toMatchObject({
        task: {
          resolved_image: { digest: `sha256:${"e".repeat(64)}` },
          image_source: "team_default",
        },
      });
      await assertPersistedClaim(database, updateFirst, "e", true);

      const claimFirst = await claimFixture(
        registry.baseURL,
        headers,
        `Archived Claim First ${scope}`,
        scope === "team" ? "9" : "a",
        scope,
      );
      expect(
        (
          await fetch(
            `${registry.baseURL}/admin/teams/${claimFirst.teamID}/archive`,
            { method: "POST", headers },
          )
        ).status,
      ).toBe(201);
      const claimBarrier = await armBarrier(
        registry.testControlURL,
        control,
        "task_claim_after_team_lock",
      );
      const beforeUpdate = claim(registry.baseURL, claimFirst, scope);
      await waitReached(registry.testControlURL, control, claimBarrier);
      const afterClaim = fetch(
        `${registry.baseURL}/admin/teams/${claimFirst.teamID}`,
        {
          method: "PATCH",
          headers,
          body: JSON.stringify({ default_image: changed }),
        },
      );
      await releaseBarrier(registry.testControlURL, control, claimBarrier);
      expect(await (await beforeUpdate).json()).toMatchObject({
        task: {
          resolved_image: {
            digest: `sha256:${(scope === "team" ? "9" : "a").repeat(64)}`,
          },
          image_source: "team_default",
        },
      });
      expect((await afterClaim).status).toBe(200);
      await assertPersistedClaim(
        database,
        claimFirst,
        scope === "team" ? "9" : "a",
        true,
      );
    }

    const retryFirst = await taskFixture(
      registry.baseURL,
      headers,
      "Retry First",
      "b",
    );
    expect(
      (await ingest(registry.baseURL, retryFirst, "dedupe-existing")).status,
    ).toBe(201);
    const retryBarrier = await armBarrier(
      registry.testControlURL,
      control,
      "task_ingest_after_team_lock",
    );
    const retry = ingest(registry.baseURL, retryFirst, "dedupe-existing");
    await waitReached(registry.testControlURL, control, retryBarrier);
    const archiveAfterRetry = fetch(
      `${registry.baseURL}/admin/teams/${retryFirst.teamID}/archive`,
      { method: "POST", headers },
    );
    await releaseBarrier(registry.testControlURL, control, retryBarrier);
    expect((await retry).status).toBe(200);
    expect((await archiveAfterRetry).status).toBe(201);

    const freshArchiveFirst = await taskFixture(
      registry.baseURL,
      headers,
      "Fresh Archive First",
      "f",
    );
    const freshArchiveBarrier = await armBarrier(
      registry.testControlURL,
      control,
      "team_archive_after_lock",
    );
    const firstFreshArchive = fetch(
      `${registry.baseURL}/admin/teams/${freshArchiveFirst.teamID}/archive`,
      { method: "POST", headers },
    );
    await waitReached(registry.testControlURL, control, freshArchiveBarrier);
    const freshAfterArchive = ingest(
      registry.baseURL,
      freshArchiveFirst,
      "new-after-archive",
    );
    await releaseBarrier(registry.testControlURL, control, freshArchiveBarrier);
    expect((await firstFreshArchive).status).toBe(201);
    expect((await freshAfterArchive).status).toBe(409);

    const retryArchive = await taskFixture(
      registry.baseURL,
      headers,
      "Concurrent Archive Retry",
      "5",
    );
    const retryArchiveBarrier = await armBarrier(
      registry.testControlURL,
      control,
      "team_archive_after_lock",
    );
    const archiveOne = fetch(
      `${registry.baseURL}/admin/teams/${retryArchive.teamID}/archive`,
      { method: "POST", headers },
    );
    await waitReached(registry.testControlURL, control, retryArchiveBarrier);
    const archiveTwo = fetch(
      `${registry.baseURL}/admin/teams/${retryArchive.teamID}/archive`,
      { method: "POST", headers },
    );
    const rejectedNew = ingest(registry.baseURL, retryArchive, "rejected-new");
    await releaseBarrier(registry.testControlURL, control, retryArchiveBarrier);
    const archiveResponses = await Promise.all([archiveOne, archiveTwo]);
    expect(archiveResponses.map((response) => response.status).sort()).toEqual([
      200, 201,
    ]);
    const archiveBodies = await Promise.all(
      archiveResponses.map(
        (response) => response.json() as Promise<{ archived_at: string }>,
      ),
    );
    expect(archiveBodies[0].archived_at).toBe(archiveBodies[1].archived_at);
    const canonicalRetry = (await (
      await fetch(
        `${registry.baseURL}/internal/v1/teams/${retryArchive.teamID}`,
      )
    ).json()) as { archived_at: string };
    expect(canonicalRetry.archived_at).toBe(archiveBodies[0].archived_at);
    expect((await rejectedNew).status).toBe(409);
    expect(
      (await ingest(registry.baseURL, retryArchive, "rejected-new")).status,
    ).toBe(409);
    expect(
      (
        await database.query(
          "SELECT count(*)::int AS count FROM tasks WHERE team_id=$1",
          [retryArchive.teamID],
        )
      ).rows,
    ).toEqual([{ count: 0 }]);

    const activeSystemUpdateFirst = await claimFixture(
      registry.baseURL,
      headers,
      "Active System Update First",
      "0",
      "system",
    );
    const activeSystemUpdateBarrier = await armBarrier(
      registry.testControlURL,
      control,
      "team_default_image_update_after_lock",
    );
    const activeSystemUpdate = fetch(
      `${registry.baseURL}/admin/teams/${activeSystemUpdateFirst.teamID}`,
      {
        method: "PATCH",
        headers,
        body: JSON.stringify({ default_image: changed }),
      },
    );
    await waitReached(
      registry.testControlURL,
      control,
      activeSystemUpdateBarrier,
    );
    const activeSystemClaimAfterUpdate = claim(
      registry.baseURL,
      activeSystemUpdateFirst,
      "system",
    );
    await releaseBarrier(
      registry.testControlURL,
      control,
      activeSystemUpdateBarrier,
    );
    expect((await activeSystemUpdate).status).toBe(200);
    expect(await (await activeSystemClaimAfterUpdate).json()).toMatchObject({
      task: {
        resolved_image: { digest: `sha256:${"e".repeat(64)}` },
        image_source: "team_default",
      },
    });
    await assertPersistedClaim(database, activeSystemUpdateFirst, "e", false);
    const activeSystemClaimFirst = await claimFixture(
      registry.baseURL,
      headers,
      "Active System Claim First",
      "6",
      "system",
    );
    const activeSystemClaimBarrier = await armBarrier(
      registry.testControlURL,
      control,
      "task_claim_after_team_lock",
    );
    const activeSystemClaim = claim(
      registry.baseURL,
      activeSystemClaimFirst,
      "system",
    );
    await waitReached(
      registry.testControlURL,
      control,
      activeSystemClaimBarrier,
    );
    const activeSystemUpdateAfterClaim = fetch(
      `${registry.baseURL}/admin/teams/${activeSystemClaimFirst.teamID}`,
      {
        method: "PATCH",
        headers,
        body: JSON.stringify({ default_image: changed }),
      },
    );
    await releaseBarrier(
      registry.testControlURL,
      control,
      activeSystemClaimBarrier,
    );
    expect(await (await activeSystemClaim).json()).toMatchObject({
      task: {
        resolved_image: { digest: `sha256:${"6".repeat(64)}` },
        image_source: "team_default",
      },
    });
    expect((await activeSystemUpdateAfterClaim).status).toBe(200);
    await assertPersistedClaim(database, activeSystemClaimFirst, "6", false);

    for (const archiveFirst of [false, true]) {
      const fixture = await claimFixture(
        registry.baseURL,
        headers,
        `System Archive Claim ${archiveFirst}`,
        archiveFirst ? "c" : "d",
        "system",
      );
      const barrier = await armBarrier(
        registry.testControlURL,
        control,
        archiveFirst ? "team_archive_after_lock" : "task_claim_after_team_lock",
      );
      const first = archiveFirst
        ? fetch(`${registry.baseURL}/admin/teams/${fixture.teamID}/archive`, {
            method: "POST",
            headers,
          })
        : claim(registry.baseURL, fixture, "system");
      await waitReached(registry.testControlURL, control, barrier);
      const second = archiveFirst
        ? claim(registry.baseURL, fixture, "system")
        : fetch(`${registry.baseURL}/admin/teams/${fixture.teamID}/archive`, {
            method: "POST",
            headers,
          });
      await releaseBarrier(registry.testControlURL, control, barrier);
      expect((await first).status).toBe(archiveFirst ? 201 : 200);
      expect((await second).status).toBe(archiveFirst ? 200 : 201);
    }
  } finally {
    await database.end();
    await registry.close();
    await postgres.close();
    await keycloak.close();
  }
});

async function armBarrier(
  baseURL: string,
  headers: Record<string, string>,
  name: string,
): Promise<string> {
  return (
    await new TestControlClient(
      baseURL,
      headers.authorization.replace(/^Bearer /, ""),
    ).createBarrier(name)
  ).barrier_id;
}

async function reachAndRelease(
  baseURL: string,
  headers: Record<string, string>,
  barrierID: string,
): Promise<void> {
  await waitReached(baseURL, headers, barrierID);
  await releaseBarrier(baseURL, headers, barrierID);
  await new TestControlClient(
    baseURL,
    headers.authorization.replace(/^Bearer /, ""),
  ).deleteBarrier(barrierID);
}

async function waitReached(
  baseURL: string,
  headers: Record<string, string>,
  id: string,
): Promise<void> {
  await new TestControlClient(
    baseURL,
    headers.authorization.replace(/^Bearer /, ""),
  ).waitForBarrier(id);
}
async function releaseBarrier(
  baseURL: string,
  headers: Record<string, string>,
  id: string,
): Promise<void> {
  await new TestControlClient(
    baseURL,
    headers.authorization.replace(/^Bearer /, ""),
  ).releaseBarrier(id);
}
type TaskFixture = {
  teamID: string;
  sourceID: string;
  taskTypeID: string;
  listenerHeaders: Record<string, string>;
};
type ClaimFixture = TaskFixture & { taskID: string; executorID: string };
async function taskFixture(
  baseURL: string,
  headers: Record<string, string>,
  name: string,
  hex: string,
): Promise<TaskFixture> {
  const image = `registry.example/race/agent@sha256:${hex.repeat(64)}`;
  const teamResponse = await fetch(`${baseURL}/admin/teams`, {
    method: "POST",
    headers,
    body: JSON.stringify({ team_name: name, default_image: image }),
  });
  expect(teamResponse.status).toBe(201);
  const teamID = ((await teamResponse.json()) as { team_id: string }).team_id;
  const sourceResponse = await fetch(`${baseURL}/admin/source-systems`, {
    method: "POST",
    headers,
    body: JSON.stringify({
      team_id: teamID,
      listener_identity: `listener-${hex}`,
    }),
  });
  expect(sourceResponse.status).toBe(201);
  const sourceID = (
    (await sourceResponse.json()) as { source_system_id: string }
  ).source_system_id;
  const typeResponse = await fetch(`${baseURL}/admin/task-types`, {
    method: "POST",
    headers,
    body: JSON.stringify({ team_id: teamID, execution_tag: `race-${hex}` }),
  });
  expect(typeResponse.status).toBe(201);
  const taskTypeID = ((await typeResponse.json()) as { task_type_id: string })
    .task_type_id;
  return {
    teamID,
    sourceID,
    taskTypeID,
    listenerHeaders: {
      "content-type": "application/json",
      "x-flowai-team-id": teamID,
      "x-flowai-source-system-id": sourceID,
      "x-flowai-listener-identity": `listener-${hex}`,
    },
  };
}
async function ingest(
  baseURL: string,
  fixture: TaskFixture,
  sourceID: string,
): Promise<Response> {
  return fetch(`${baseURL}/v1/tasks`, {
    method: "POST",
    headers: fixture.listenerHeaders,
    body: JSON.stringify({
      team_id: fixture.teamID,
      source_system_id: fixture.sourceID,
      source_id: sourceID,
      task_type_id: fixture.taskTypeID,
      payload: { sourceID },
    }),
  });
}
async function claimFixture(
  baseURL: string,
  headers: Record<string, string>,
  name: string,
  hex: string,
  scope: "team" | "system" = "team",
): Promise<ClaimFixture> {
  const fixture = await taskFixture(baseURL, headers, name, hex);
  const taskResponse = await ingest(baseURL, fixture, `task-${hex}`);
  expect(taskResponse.status).toBe(201);
  const taskID = ((await taskResponse.json()) as { task_id: string }).task_id;
  const executorResponse = await fetch(`${baseURL}/v1/executors`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-flowai-role": scope === "team" ? "team-executor" : "system-executor",
      ...(scope === "team" ? { "x-flowai-team-id": fixture.teamID } : {}),
      "x-flowai-request-id": randomUUID(),
    },
    body: JSON.stringify({
      scope,
      team_id: scope === "team" ? fixture.teamID : null,
      executor_type: "executor_k8s_openhands",
      authorized_tag: `race-${hex}`,
      max_capacity: 1,
      running_count: 0,
      runtime_metadata: { race: true },
    }),
  });
  expect(executorResponse.status).toBe(201);
  return {
    ...fixture,
    taskID,
    executorID: ((await executorResponse.json()) as { executor_id: string })
      .executor_id,
  };
}
async function claim(
  baseURL: string,
  fixture: ClaimFixture,
  scope: "team" | "system" = "team",
): Promise<Response> {
  return fetch(`${baseURL}/v1/executors/${fixture.executorID}/claim`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-flowai-role": scope === "team" ? "team-executor" : "system-executor",
      ...(scope === "team" ? { "x-flowai-team-id": fixture.teamID } : {}),
      "x-flowai-executor-id": fixture.executorID,
      "x-flowai-request-id": randomUUID(),
    },
    body: JSON.stringify({
      task_id: fixture.taskID,
      command_id: `cmd-${randomUUID()}`,
    }),
  });
}
async function assertPersistedClaim(
  database: pg.Pool,
  fixture: ClaimFixture,
  hex: string,
  archived: boolean,
): Promise<void> {
  const result = await database.query(
    `SELECT tasks.executor_id,tasks.resolved_image->>'digest' AS digest,tasks.image_source,teams.archived_at FROM tasks JOIN teams USING(team_id) WHERE tasks.task_id=$1`,
    [fixture.taskID],
  );
  expect(result.rows).toEqual([
    {
      executor_id: fixture.executorID,
      digest: `sha256:${hex.repeat(64)}`,
      image_source: "team_default",
      archived_at: archived ? expect.any(Date) : null,
    },
  ]);
}
async function createTeam(
  baseURL: string,
  headers: Record<string, string>,
  name: string,
  hex: string,
): Promise<Response> {
  return fetch(`${baseURL}/admin/teams`, {
    method: "POST",
    headers,
    body: JSON.stringify({
      team_name: name,
      default_image: `registry.example/race/writer@sha256:${hex.repeat(64)}`,
    }),
  });
}
async function createTeamJSON(
  baseURL: string,
  headers: Record<string, string>,
  name: string,
  hex: string,
): Promise<{ team_id: string }> {
  const response = await createTeam(baseURL, headers, name, hex);
  expect(response.status).toBe(201);
  return response.json() as Promise<{ team_id: string }>;
}
async function patchTeam(
  baseURL: string,
  headers: Record<string, string>,
  teamID: string,
  name: string,
): Promise<Response> {
  return fetch(`${baseURL}/admin/teams/${teamID}`, {
    method: "PATCH",
    headers,
    body: JSON.stringify({ team_name: name }),
  });
}

async function issueAdminToken(tokenEndpoint: string): Promise<string> {
  const response = await fetch(tokenEndpoint, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "password",
      client_id: "flowai-admin-e2e",
      username: "operator-admin",
      password: "flowai-e2e-password",
    }),
  });
  if (!response.ok) throw new Error(`admin token status ${response.status}`);
  return ((await response.json()) as { access_token: string }).access_token;
}
