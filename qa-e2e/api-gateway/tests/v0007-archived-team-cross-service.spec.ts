import { generateKeyPairSync, randomUUID } from "node:crypto";
import { expect, test } from "@playwright/test";
import pg from "pg";
import WebSocket from "ws";
import { startGatewayContainer } from "../fixtures/gateway_container.js";
import { startKeycloakContainer } from "../fixtures/keycloak_container.js";
import { startPostgresContainer } from "../fixtures/postgres_container.js";
import { startProductionIngress } from "../fixtures/production_ingress.js";
import { startStateRegistryContainer } from "../fixtures/state_registry_container.js";

test("v0007.29 v0007.32 v0007.40 v0007.44 preserve archived reads and reject all ingestion", async ({
  page,
}) => {
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
  const ingress = await startProductionIngress();
  const keys = generateKeyPairSync("rsa", { modulusLength: 2048 });
  const privateKey = Buffer.from(
    keys.privateKey.export({ format: "pem", type: "pkcs8" }),
  ).toString("base64");
  let gateway: Awaited<ReturnType<typeof startGatewayContainer>> | undefined;
  const database = new pg.Pool({ connectionString: postgres.connectionString });
  try {
    gateway = await startGatewayContainer(
      {
        API_GATEWAY_OIDC_ISSUER: keycloak.issuer,
        API_GATEWAY_OIDC_CLIENT_ID: "flowai-api-gateway-e2e",
        API_GATEWAY_OIDC_REDIRECT_URI: `${ingress.baseURL}/auth/v1/callback`,
        API_GATEWAY_OIDC_TEAM_CLAIM_ADAPTER: "string_array",
        API_GATEWAY_OIDC_TEAM_CLAIM_POINTER: "/teams",
        API_GATEWAY_OIDC_TEAM_CLAIM_SOURCE: "id_token",
        API_GATEWAY_OIDC_ENDPOINT_TIMEOUT: "5s",
        API_GATEWAY_POSTGRES_URL: postgres.connectionString,
        API_GATEWAY_SESSION_KEY_HEX: "6c".repeat(32),
        API_GATEWAY_WEB_UI_URL: `${ingress.baseURL}/`,
        API_GATEWAY_STATE_REGISTRY_URL: registry.baseURL,
        API_GATEWAY_ADMIN_ISSUER: keycloak.issuer,
        API_GATEWAY_ADMIN_AUDIENCE: "flowai-api-gateway-admin",
        API_GATEWAY_ADMIN_JWKS_URL: keycloak.discovery.jwks_uri,
        API_GATEWAY_ADMIN_ROLE_CLAIM_POINTER: "/realm_access/roles",
        API_GATEWAY_ADMIN_JWKS_TIMEOUT: "5s",
        API_GATEWAY_ADMIN_TOKEN_MAX_AGE: "5m",
        API_GATEWAY_WORKING_TOKEN_PRIVATE_KEY_BASE64: privateKey,
        API_GATEWAY_WORKING_TOKEN_KEY_ID: "cross-service",
        API_GATEWAY_WORKING_TOKEN_ISSUER: "https://gateway.flowai.invalid",
        API_GATEWAY_WORKING_TOKEN_AUDIENCE: "flowai-state-registry",
        API_GATEWAY_WORKING_TOKEN_TTL: "2m",
      },
      { hostNetwork: true },
    );
    ingress.setGatewayOrigin(gateway.baseUrl);
    await keycloak.allowRedirectURI(`${ingress.baseURL}/auth/v1/callback`);
    const adminToken = await adminJWT(keycloak.discovery.token_endpoint);
    const adminHeaders = {
      authorization: `Bearer ${adminToken}`,
      "content-type": "application/json",
    };
    const image = `registry.example/agent@sha256:${"e".repeat(64)}`;
    const teamResponse = await fetch(`${registry.baseURL}/admin/teams`, {
      method: "POST",
      headers: adminHeaders,
      body: JSON.stringify({
        team_name: "Cross Service Team",
        default_image: image,
      }),
    });
    expect(teamResponse.status).toBe(201);
    const team = (await teamResponse.json()) as { team_id: string };
    const sourceResponse = await fetch(
      `${registry.baseURL}/admin/source-systems`,
      {
        method: "POST",
        headers: adminHeaders,
        body: JSON.stringify({
          team_id: team.team_id,
          listener_identity: "listener-cross",
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
        execution_tag: "openhands",
      }),
    });
    expect(typeResponse.status).toBe(201);
    const taskType = (await typeResponse.json()) as { task_type_id: string };
    const listenerHeaders = {
      "content-type": "application/json",
      "x-flowai-team-id": team.team_id,
      "x-flowai-source-system-id": source.source_system_id,
      "x-flowai-listener-identity": "listener-cross",
    };
    const taskBody = {
      team_id: team.team_id,
      source_system_id: source.source_system_id,
      source_id: "existing",
      task_type_id: taskType.task_type_id,
      payload: { marker: "unchanged" },
    };
    const createTask = async (sourceID: string) => {
      const response = await fetch(`${registry.baseURL}/v1/tasks`, {
        method: "POST",
        headers: listenerHeaders,
        body: JSON.stringify({ ...taskBody, source_id: sourceID }),
      });
      expect(response.status).toBe(201);
      return response.json() as Promise<{ task_id: string }>;
    };
    await createTask("existing-running-finish");
    await createTask("existing-running-fail");
    await createTask("existing-finished");
    await createTask("existing-failed");
    await createTask("existing-pending");
    const taskOrder = (
      await database.query(
        "SELECT task_id FROM tasks WHERE team_id=$1 ORDER BY ingested_at,task_id",
        [team.team_id],
      )
    ).rows as Array<{ task_id: string }>;
    const [
      runningFinishTask,
      runningFailTask,
      finishedTask,
      failedTask,
      pendingTask,
    ] = taskOrder;
    const executorHeaders = {
      "content-type": "application/json",
      "x-flowai-role": "team-executor",
      "x-flowai-team-id": team.team_id,
      "x-flowai-request-id": randomUUID(),
    };
    const executorResponse = await fetch(`${registry.baseURL}/v1/executors`, {
      method: "POST",
      headers: executorHeaders,
      body: JSON.stringify({
        scope: "team",
        team_id: team.team_id,
        executor_type: "executor_k8s_openhands",
        authorized_tag: "openhands",
        max_capacity: 4,
        running_count: 0,
        runtime_metadata: { test: "v0007.32" },
      }),
    });
    expect(executorResponse.status).toBe(201);
    const executorID = (
      (await executorResponse.json()) as { executor_id: string }
    ).executor_id;
    const assignedHeaders = {
      ...executorHeaders,
      "x-flowai-executor-id": executorID,
    };
    const claimTask = async (taskID: string) =>
      fetch(`${registry.baseURL}/v1/executors/${executorID}/claim`, {
        method: "POST",
        headers: assignedHeaders,
        body: JSON.stringify({
          task_id: taskID,
          command_id: `cmd-${randomUUID()}`,
        }),
      });
    const append = async (
      taskID: string,
      eventType: "running" | "finished" | "failed",
      offset: number,
    ) =>
      fetch(`${registry.baseURL}/v1/tasks/${taskID}/events`, {
        method: "POST",
        headers: assignedHeaders,
        body: JSON.stringify({
          event_id: randomUUID(),
          team_id: team.team_id,
          task_id: taskID,
          executor_id: executorID,
          event_type: eventType,
          occurred_at: new Date(Date.now() + offset).toISOString(),
          payload: { test: "v0007.32" },
        }),
      });
    expect((await claimTask(runningFinishTask.task_id)).status).toBe(200);
    expect(
      (await append(runningFinishTask.task_id, "running", 100)).status,
    ).toBe(202);
    expect((await claimTask(runningFailTask.task_id)).status).toBe(200);
    expect((await append(runningFailTask.task_id, "running", 200)).status).toBe(
      202,
    );
    expect((await claimTask(finishedTask.task_id)).status).toBe(200);
    expect((await append(finishedTask.task_id, "running", 300)).status).toBe(
      202,
    );
    expect((await append(finishedTask.task_id, "finished", 400)).status).toBe(
      202,
    );
    expect((await claimTask(failedTask.task_id)).status).toBe(200);
    expect((await append(failedTask.task_id, "running", 500)).status).toBe(202);
    expect((await append(failedTask.task_id, "failed", 600)).status).toBe(202);

    const mapping = await fetch(
      `${gateway.baseUrl}/admin/v1/oidc-team-mappings`,
      {
        method: "POST",
        headers: adminHeaders,
        body: JSON.stringify({
          issuer: keycloak.issuer,
          oidc_team_id: "oidc-team-alpha",
          team_id: team.team_id,
        }),
      },
    );
    expect(mapping.status).toBe(201);
    await page.goto(`${ingress.baseURL}/auth/v1/login`);
    await page.locator("#username").fill("operator-admin");
    await page.locator("#password").fill("flowai-e2e-password");
    const callback = page.waitForResponse((response) =>
      response.url().startsWith(`${ingress.baseURL}/auth/v1/callback?`),
    );
    await page.locator("#kc-login").click();
    const callbackResponse = await callback;
    await page.waitForURL(`${ingress.baseURL}/`);
    const setCookie =
      (await callbackResponse.headersArray()).find(
        (header) => header.name.toLowerCase() === "set-cookie",
      )?.value ?? "";
    const session = /flowai_session=([^;]+)/.exec(setCookie)?.[1];
    if (!session) throw new Error("missing session");
    const cookie = { cookie: `flowai_session=${session}` };
    expect(
      (
        (await (
          await fetch(`${gateway.baseUrl}/auth/v1/teams`, { headers: cookie })
        ).json()) as { teams: unknown[] }
      ).teams,
    ).toEqual([
      expect.objectContaining({
        team_id: team.team_id,
        team_name: "Cross Service Team",
        archived_at: null,
      }),
    ]);
    await expect(
      page.getByRole("combobox", { name: "Team", exact: true }),
    ).toHaveValue(team.team_id);
    await expect(page.getByTestId("selected-team-id")).toHaveText(team.team_id);

    expect(
      (
        await fetch(`${registry.baseURL}/admin/teams/${team.team_id}/archive`, {
          method: "POST",
          headers: adminHeaders,
        })
      ).status,
    ).toBe(201);
    const archivedTeams = (
      (await (
        await fetch(`${gateway.baseUrl}/auth/v1/teams`, { headers: cookie })
      ).json()) as {
        teams: Array<{ team_id: string; archived_at: string | null }>;
      }
    ).teams;
    expect(archivedTeams).toHaveLength(1);
    expect(archivedTeams[0].archived_at).not.toBeNull();
    await page.reload();
    await expect(
      page
        .getByRole("combobox", { name: "Team", exact: true })
        .locator("option"),
    ).toHaveText(["Cross Service Team (archived)"]);
    const tokenResponse = await fetch(`${gateway.baseUrl}/auth/v1/token`, {
      method: "POST",
      headers: { ...cookie, "content-type": "application/json" },
      body: JSON.stringify({ team_id: team.team_id }),
    });
    expect(tokenResponse.status).toBe(200);
    const token = ((await tokenResponse.json()) as { access_token: string })
      .access_token;
    const operatorID = (
      JSON.parse(
        Buffer.from(token.split(".")[1], "base64url").toString("utf8"),
      ) as { operator_id: string }
    ).operator_id;
    expect(
      (await append(runningFinishTask.task_id, "finished", 700)).status,
    ).toBe(202);
    expect((await append(runningFailTask.task_id, "failed", 800)).status).toBe(
      202,
    );
    const pendingClaim = await claimTask(pendingTask.task_id);
    expect(pendingClaim.status, await pendingClaim.clone().text()).toBe(200);
    const controlRequestID = randomUUID();
    const control = await fetch(
      `${gateway.baseUrl}/ui/v1/teams/${team.team_id}/tasks/${pendingTask.task_id}/controls/cancel`,
      {
        method: "POST",
        headers: {
          authorization: `Bearer ${token}`,
          "content-type": "application/json",
          "x-flowai-operator-id": "forged",
        },
        body: JSON.stringify({ request_id: controlRequestID }),
      },
    );
    expect(control.status).toBe(202);
    expect(
      (
        await fetch(
          `${registry.baseURL}/v1/tasks/${pendingTask.task_id}/controls`,
          { headers: assignedHeaders },
        )
      ).status,
    ).toBe(200);
    const audit = await database.query(
      `SELECT actor_id,request_id FROM audit_entries WHERE action='task.control.cancel' AND team_id=$1`,
      [team.team_id],
    );
    expect(audit.rows).toEqual([
      { actor_id: operatorID, request_id: controlRequestID },
    ]);
    expect((await append(pendingTask.task_id, "running", 1_000)).status).toBe(
      202,
    );
    expect((await append(pendingTask.task_id, "finished", 2_000)).status).toBe(
      202,
    );
    for (const [taskID, state] of [
      [runningFinishTask.task_id, "finished"],
      [runningFailTask.task_id, "failed"],
      [finishedTask.task_id, "finished"],
      [failedTask.task_id, "failed"],
      [pendingTask.task_id, "finished"],
    ]) {
      const detail = await fetch(
        `${gateway.baseUrl}/ui/v1/teams/${team.team_id}/tasks/${taskID}`,
        { headers: { authorization: `Bearer ${token}` } },
      );
      expect(detail.status).toBe(200);
      expect(await detail.json()).toMatchObject({
        task_id: taskID,
        current_state: state,
      });
    }
    const failedHistory = await fetch(
      `${gateway.baseUrl}/ui/v1/teams/${team.team_id}/tasks/${runningFailTask.task_id}/events`,
      { headers: { authorization: `Bearer ${token}` } },
    );
    expect(failedHistory.status).toBe(200);
    expect(JSON.stringify(await failedHistory.json())).toContain("failed");
    const dashboard = await fetch(
      `${gateway.baseUrl}/ui/v1/teams/${team.team_id}/dashboard?period=week`,
      { headers: { authorization: `Bearer ${token}` } },
    );
    expect(dashboard.status).toBe(200);
    const protocol = `flowai.bearer.${Buffer.from(token).toString("base64url")}`;
    const socket = new WebSocket(
      `${gateway.baseUrl.replace(/^http/, "ws")}/ui/v1/teams/${team.team_id}/stream?after=2026-01-01T00:00:00Z`,
      [protocol],
    );
    const replay = new Promise<string[]>((resolve, reject) => {
      const frames: string[] = [];
      const timer = setTimeout(
        () =>
          reject(
            new Error(
              `production Registry terminal replay timeout: ${frames.join("\n")}`,
            ),
          ),
        5_000,
      );
      socket.on("message", (data) => {
        frames.push(data.toString());
        const all = frames.join("\n");
        if (
          all.includes(runningFinishTask.task_id) &&
          all.includes(runningFailTask.task_id) &&
          all.includes("finished") &&
          all.includes("failed")
        ) {
          clearTimeout(timer);
          resolve(frames);
        }
      });
      socket.once("error", reject);
    });
    await new Promise<void>((resolve, reject) => {
      socket.once("open", resolve);
      socket.once("error", reject);
    });
    const replayFrames = await replay;
    expect(replayFrames.join("\n")).toContain(team.team_id);
    socket.close();

    const retry = await fetch(`${registry.baseURL}/v1/tasks`, {
      method: "POST",
      headers: listenerHeaders,
      body: JSON.stringify({
        ...taskBody,
        source_id: "existing-running-finish",
      }),
    });
    expect(retry.status).toBe(409);
    expect(((await retry.json()) as { code: string }).code).toBe(
      "team_archived",
    );
    const fresh = await fetch(`${registry.baseURL}/v1/tasks`, {
      method: "POST",
      headers: listenerHeaders,
      body: JSON.stringify({ ...taskBody, source_id: "new" }),
    });
    expect(fresh.status).toBe(409);
    const rows = await database.query(
      `SELECT source_id, payload FROM tasks WHERE team_id=$1 ORDER BY source_id`,
      [team.team_id],
    );
    expect(rows.rows).toEqual(
      [
        "existing-failed",
        "existing-finished",
        "existing-pending",
        "existing-running-fail",
        "existing-running-finish",
      ].map((source_id) => ({ source_id, payload: { marker: "unchanged" } })),
    );
  } finally {
    await database.end();
    await gateway?.close();
    await ingress.close();
    await registry.close();
    await postgres.close();
    await keycloak.close();
  }
});

async function adminJWT(endpoint: string): Promise<string> {
  const response = await fetch(endpoint, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "password",
      client_id: "flowai-admin-e2e",
      username: "operator-admin",
      password: "flowai-e2e-password",
    }),
  });
  if (!response.ok) throw new Error(`admin token ${response.status}`);
  return ((await response.json()) as { access_token: string }).access_token;
}
