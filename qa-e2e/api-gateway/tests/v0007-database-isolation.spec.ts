import { generateKeyPairSync } from "node:crypto";
import { expect, test } from "@playwright/test";
import pg from "pg";
import { startGatewayContainer } from "../fixtures/gateway_container.js";
import { startKeycloakContainer } from "../fixtures/keycloak_container.js";
import { startPostgresContainer } from "../fixtures/postgres_container.js";
import { startStateRegistryContainer } from "../fixtures/state_registry_container.js";

test("v0007.21 Stage 2 isolates Gateway and State Registry runtime roles across restart", async ({
  page,
}) => {
  const keycloak = await startKeycloakContainer();
  const postgres = await startPostgresContainer();
  const bootstrapRegistry = await startStateRegistryContainer(
    postgres.connectionString,
    adminTrust(keycloak),
  );
  const bootstrapGateway = await startGatewayContainer(
    gatewayEnvironment(
      keycloak,
      postgres.connectionString,
      postgres.connectionString,
      bootstrapRegistry.baseURL,
    ),
    { hostNetwork: true },
  );
  await bootstrapGateway.close();
  await bootstrapRegistry.close();
  const admin = new pg.Pool({ connectionString: postgres.connectionString });
  const passwords = {
    gatewayRuntime: "gateway-runtime-v0007",
    gatewayMigrator: "gateway-migrator-v0007",
    registryRuntime: "registry-runtime-v0007",
    registryMigrator: "registry-migrator-v0007",
  };
  try {
    await admin.query(
      `CREATE ROLE api_gateway_runtime LOGIN PASSWORD '${passwords.gatewayRuntime}'`,
    );
    await admin.query(
      `CREATE ROLE api_gateway_migrator LOGIN PASSWORD '${passwords.gatewayMigrator}'`,
    );
    await admin.query(
      `CREATE ROLE state_registry_runtime LOGIN PASSWORD '${passwords.registryRuntime}'`,
    );
    await admin.query(
      `CREATE ROLE state_registry_migrator LOGIN PASSWORD '${passwords.registryMigrator}'`,
    );
    await admin.query(
      `GRANT CONNECT ON DATABASE postgres TO api_gateway_runtime,api_gateway_migrator,state_registry_runtime,state_registry_migrator`,
    );
    await admin.query(
      `GRANT CREATE ON DATABASE postgres TO api_gateway_migrator`,
    );
    await admin.query(
      `GRANT USAGE ON SCHEMA api_gateway TO api_gateway_runtime,api_gateway_migrator`,
    );
    await admin.query(
      `GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA api_gateway TO api_gateway_runtime`,
    );
    await admin.query(
      `GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA api_gateway TO api_gateway_migrator`,
    );
    await admin.query(
      `GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA api_gateway TO api_gateway_runtime,api_gateway_migrator`,
    );
    await admin.query(
      `GRANT USAGE ON SCHEMA public TO state_registry_runtime,state_registry_migrator`,
    );
    await admin.query(
      `GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO state_registry_runtime`,
    );
    await admin.query(
      `GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO state_registry_migrator`,
    );
    await admin.query(
      `GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO state_registry_runtime,state_registry_migrator`,
    );
  } finally {
    await admin.end();
  }

  const gatewayRuntimeURL = roleURL(
    postgres.connectionString,
    "api_gateway_runtime",
    passwords.gatewayRuntime,
  );
  const gatewayMigratorURL = roleURL(
    postgres.connectionString,
    "api_gateway_migrator",
    passwords.gatewayMigrator,
  );
  const registryRuntimeURL = roleURL(
    postgres.connectionString,
    "state_registry_runtime",
    passwords.registryRuntime,
  );
  const registryMigratorURL = roleURL(
    postgres.connectionString,
    "state_registry_migrator",
    passwords.registryMigrator,
  );
  let registry = await startStateRegistryContainer(
    postgres.connectionString,
    adminTrust(keycloak),
    { runtime: registryRuntimeURL, migration: registryMigratorURL },
  );
  let gateway = await startGatewayContainer(
    gatewayEnvironment(
      keycloak,
      gatewayRuntimeURL,
      gatewayMigratorURL,
      registry.baseURL,
    ),
    { hostNetwork: true },
  );
  try {
    expect((await fetch(`${gateway.baseUrl}/healthz`)).status).toBe(200);
    expect((await fetch(`${registry.baseURL}/v1/livez`)).status).toBe(200);
    await keycloak.allowRedirectURI(`${gateway.baseUrl}/auth/v1/callback`);
    const adminToken = await issueAdminToken(keycloak.discovery.token_endpoint);
    const adminHeaders = {
      authorization: `Bearer ${adminToken}`,
      "content-type": "application/json",
    };
    const teamResponse = await fetch(`${registry.baseURL}/admin/teams`, {
      method: "POST",
      headers: adminHeaders,
      body: JSON.stringify({
        team_name: "Isolation Team",
        default_image: `registry.example/isolation@sha256:${"a".repeat(64)}`,
      }),
    });
    expect(teamResponse.status).toBe(201);
    const team = (await teamResponse.json()) as {
      team_id: string;
      team_name: string;
    };
    const mappingResponse = await fetch(
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
    expect(mappingResponse.status).toBe(201);
    await page.goto(`${gateway.baseUrl}/auth/v1/login`);
    await page.locator("#username").fill("operator-admin");
    await page.locator("#password").fill("flowai-e2e-password");
    const callback = page.waitForResponse((response) =>
      response.url().startsWith(`${gateway.baseUrl}/auth/v1/callback?`),
    );
    await page.locator("#kc-login").click();
    const callbackResponse = await callback;
    const cookieHeader =
      (await callbackResponse.headersArray()).find(
        (header) => header.name.toLowerCase() === "set-cookie",
      )?.value ?? "";
    const session = /flowai_session=([^;]+)/.exec(cookieHeader)?.[1];
    expect(session).toBeTruthy();
    const sessionHeaders = { cookie: `flowai_session=${session}` };
    expect(
      await (
        await fetch(`${gateway.baseUrl}/auth/v1/teams`, {
          headers: sessionHeaders,
        })
      ).json(),
    ).toEqual({
      teams: [
        expect.objectContaining({
          team_id: team.team_id,
          team_name: "Isolation Team",
        }),
      ],
    });
    const gatewayDB = new pg.Pool({ connectionString: gatewayRuntimeURL });
    const registryDB = new pg.Pool({ connectionString: registryRuntimeURL });
    try {
      expect(
        (
          await gatewayDB.query(
            `SELECT count(*)::int AS count FROM api_gateway.oidc_sessions`,
          )
        ).rows,
      ).toEqual([{ count: 1 }]);
      expect(
        (await registryDB.query(`SELECT count(*)::int AS count FROM teams`))
          .rows,
      ).toEqual([{ count: 1 }]);
      for (const probe of [
        () => gatewayDB.query(`SELECT * FROM teams LIMIT 1`),
        () =>
          gatewayDB.query(
            `INSERT INTO teams(team_id,team_name,default_image) VALUES('forbidden','forbidden','x@y')`,
          ),
        () =>
          gatewayDB.query(`UPDATE teams SET team_name='forbidden' WHERE false`),
        () =>
          gatewayDB.query(
            `CREATE TABLE api_gateway.runtime_migration_forbidden(id int)`,
          ),
        () =>
          registryDB.query(`SELECT * FROM api_gateway.oidc_sessions LIMIT 1`),
        () =>
          registryDB.query(
            `INSERT INTO api_gateway.auth_audit(audit_id,action,outcome,request_id) VALUES('forbidden','x','x','x')`,
          ),
        () =>
          registryDB.query(
            `UPDATE api_gateway.oidc_team_mappings SET team_id=team_id WHERE false`,
          ),
        () =>
          registryDB.query(
            `CREATE TABLE api_gateway.registry_migration_forbidden(id int)`,
          ),
      ])
        await expectPermissionDenied(probe);
    } finally {
      await gatewayDB.end();
      await registryDB.end();
    }
    await gateway.close();
    await registry.close();
    registry = await startStateRegistryContainer(
      postgres.connectionString,
      adminTrust(keycloak),
      { runtime: registryRuntimeURL, migration: registryMigratorURL },
    );
    gateway = await startGatewayContainer(
      gatewayEnvironment(
        keycloak,
        gatewayRuntimeURL,
        gatewayMigratorURL,
        registry.baseURL,
      ),
      { hostNetwork: true },
    );
    expect((await fetch(`${gateway.baseUrl}/healthz`)).status).toBe(200);
    expect((await fetch(`${registry.baseURL}/v1/livez`)).status).toBe(200);
    const persistedTeam = await fetch(
      `${registry.baseURL}/internal/v1/teams/${team.team_id}`,
    );
    expect(persistedTeam.status).toBe(200);
    expect(await persistedTeam.json()).toMatchObject({
      team_id: team.team_id,
      team_name: "Isolation Team",
    });
    const persistedTeams = await fetch(`${gateway.baseUrl}/auth/v1/teams`, {
      headers: sessionHeaders,
    });
    expect(persistedTeams.status).toBe(200);
    expect(await persistedTeams.json()).toEqual({
      teams: [
        expect.objectContaining({
          team_id: team.team_id,
          team_name: "Isolation Team",
        }),
      ],
    });
  } finally {
    await gateway.close();
    await registry.close();
    await postgres.close();
    await keycloak.close();
  }
});

function adminTrust(
  keycloak: Awaited<ReturnType<typeof startKeycloakContainer>>,
) {
  return {
    issuer: keycloak.issuer,
    audience: "flowai-api-gateway-admin",
    jwksURL: keycloak.discovery.jwks_uri,
  };
}
function roleURL(base: string, user: string, password: string): string {
  const url = new URL(base);
  url.username = user;
  url.password = password;
  return url.toString();
}
function gatewayEnvironment(
  keycloak: Awaited<ReturnType<typeof startKeycloakContainer>>,
  runtimeURL: string,
  migrationURL: string,
  registryURL: string,
): Record<string, string> {
  const keys = generateKeyPairSync("rsa", { modulusLength: 2048 });
  return {
    API_GATEWAY_OIDC_ISSUER: keycloak.issuer,
    API_GATEWAY_OIDC_CLIENT_ID: "flowai-api-gateway-e2e",
    API_GATEWAY_OIDC_REDIRECT_URI: "${GATEWAY_BASE_URL}/auth/v1/callback",
    API_GATEWAY_OIDC_TEAM_CLAIM_ADAPTER: "string_array",
    API_GATEWAY_OIDC_TEAM_CLAIM_POINTER: "/teams",
    API_GATEWAY_OIDC_TEAM_CLAIM_SOURCE: "id_token",
    API_GATEWAY_OIDC_ENDPOINT_TIMEOUT: "5s",
    API_GATEWAY_POSTGRES_URL: runtimeURL,
    API_GATEWAY_MIGRATION_POSTGRES_URL: migrationURL,
    API_GATEWAY_SESSION_KEY_HEX: "4d".repeat(32),
    API_GATEWAY_WEB_UI_URL: "${GATEWAY_BASE_URL}/",
    API_GATEWAY_STATE_REGISTRY_URL: registryURL,
    API_GATEWAY_ADMIN_ISSUER: keycloak.issuer,
    API_GATEWAY_ADMIN_AUDIENCE: "flowai-api-gateway-admin",
    API_GATEWAY_ADMIN_JWKS_URL: keycloak.discovery.jwks_uri,
    API_GATEWAY_ADMIN_ROLE_CLAIM_POINTER: "/realm_access/roles",
    API_GATEWAY_ADMIN_JWKS_TIMEOUT: "5s",
    API_GATEWAY_ADMIN_TOKEN_MAX_AGE: "5m",
    API_GATEWAY_WORKING_TOKEN_PRIVATE_KEY_BASE64: Buffer.from(
      keys.privateKey.export({ format: "pem", type: "pkcs8" }),
    ).toString("base64"),
    API_GATEWAY_WORKING_TOKEN_KEY_ID: "isolation",
    API_GATEWAY_WORKING_TOKEN_ISSUER: "https://gateway.flowai.invalid",
    API_GATEWAY_WORKING_TOKEN_AUDIENCE: "flowai-state-registry",
    API_GATEWAY_WORKING_TOKEN_TTL: "5m",
  };
}
async function expectPermissionDenied(
  probe: () => Promise<unknown>,
): Promise<void> {
  try {
    await probe();
    throw new Error("cross-schema probe unexpectedly succeeded");
  } catch (error) {
    expect((error as { code?: string }).code).toBe("42501");
  }
}
async function issueAdminToken(endpoint: string): Promise<string> {
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
  expect(response.status).toBe(200);
  return ((await response.json()) as { access_token: string }).access_token;
}
