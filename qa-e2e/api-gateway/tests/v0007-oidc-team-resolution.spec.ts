import { expect, test } from "@playwright/test";
import { generateKeyPairSync } from "node:crypto";
import {
  startGatewayContainer,
  type GatewayContainer,
} from "../fixtures/gateway_container.js";
import {
  ProviderNeutralOIDCFixture,
  ReadOnlyDatabaseAssertions,
} from "../fixtures/harness.js";
import { startPostgresContainer } from "../fixtures/postgres_container.js";

let gateway: GatewayContainer;

test.beforeAll(async () => {
  gateway = await startGatewayContainer();
});
test.afterAll(async () => {
  await gateway?.close();
});

async function request(
  path: string,
  init?: RequestInit,
): Promise<{ response: Response; body: Record<string, unknown> }> {
  const response = await fetch(`${gateway.baseUrl}${path}`, init);
  const body = (await response.json()) as Record<string, unknown>;
  return { response, body };
}

function expectError(body: Record<string, unknown>, code: string): void {
  expect(body.code).toBe(code);
  expect(body.message).toEqual(expect.any(String));
  expect(body.request_id).toEqual(expect.any(String));
}

test("v0007.16 normalizes only provider-authenticated OIDC teams", async () => {
  const { response, body } = await request("/auth/v1/teams", {
    headers: {
      cookie: "flowai_session=forged",
      "x-flowai-team-id": "team-beta",
    },
  });
  expect(response.status).toBe(401);
  expectError(body, "invalid_session");
});

test("v0007.17 resolves mappings without trusting unknown team selection", async () => {
  const { response, body } = await request("/auth/v1/token", {
    method: "POST",
    headers: {
      "content-type": "application/json",
      cookie: "flowai_session=forged",
    },
    body: JSON.stringify({ team_id: "unknown" }),
  });
  expect(response.status).toBe(401);
  expectError(body, "invalid_session");
});

test("v0007.18 mapping registration requires provider-neutral admin authentication", async () => {
  const { response, body } = await request("/admin/v1/oidc-team-mappings", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      issuer: "https://issuer.invalid",
      oidc_team_id: "alpha",
      team_id: "team-alpha",
    }),
  });
  expect(response.status).toBe(401);
  expectError(body, "invalid_admin_token");
});

test("v0007.19 rejects unresolved team selection", async () => {
  const { response, body } = await request("/auth/v1/token", {
    method: "POST",
    headers: {
      "content-type": "application/json",
      cookie: "flowai_session=unknown",
    },
    body: JSON.stringify({ team_id: "team-beta" }),
  });
  expect(response.status).toBe(401);
  expectError(body, "invalid_session");
});

test("v0007.20 rejects identity-provider fields at the canonical-team boundary", async () => {
  const { response, body } = await request("/admin/v1/oidc-team-mappings", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      issuer: "https://issuer.invalid",
      oidc_team_id: "alpha",
      team_id: "team-alpha",
      operator_id: "forged",
    }),
  });
  expect(response.status).toBe(401);
  expectError(body, "invalid_admin_token");
});

test("v0007.21 production Gateway exposes an isolated persistence-backed API", async () => {
  expect(gateway.imageID).toMatch(/^(sha256:)?[a-f0-9]{64}$/);
  const health = await fetch(`${gateway.baseUrl}/healthz`);
  expect(health.status).toBe(200);
  const { response, body } = await request("/auth/v1/teams");
  expect(response.status).toBe(401);
  expectError(body, "invalid_session");
});

test("v0007.22 filters unknown well-formed OIDC memberships", async () => {
  const { response, body } = await request("/auth/v1/token", {
    method: "POST",
    headers: {
      "content-type": "application/json",
      cookie: "flowai_session=unknown",
    },
    body: JSON.stringify({ team_id: "oidc-team-unknown" }),
  });
  expect(response.status).toBe(401);
  expectError(body, "invalid_session");
});

test("v0007.23 fails closed when an OIDC endpoint is unavailable", async () => {
  const { response, body } = await request(
    "/auth/v1/callback?code=fixture-code&state=unknown",
  );
  expect(response.status).toBe(401);
  expectError(body, "invalid_oidc_identity");
});

test("v0007.25 rejects invalid administrator tokens before mapping access", async () => {
  const { response, body } = await request("/admin/v1/oidc-team-mappings", {
    method: "POST",
    headers: {
      authorization: "Bearer invalid",
      "content-type": "application/json",
    },
    body: JSON.stringify({
      issuer: "https://issuer.invalid",
      oidc_team_id: "alpha",
      team_id: "team-alpha",
    }),
  });
  expect(response.status).toBe(401);
  expectError(body, "invalid_admin_token");
});

test("v0007.27 rejects callback XOR violations with the stable envelope", async () => {
  const { response, body } = await request(
    "/auth/v1/callback?code=fixture-code&error=access_denied&state=fixture-state&error_description=secret",
  );
  expect(response.status).toBe(400);
  expectError(body, "invalid_request");
  expect(JSON.stringify(body)).not.toContain("secret");
});

test("v0007.16 object_array UserInfo adapter normalizes IDs and explicit scopes end to end", async ({
  page,
}) => {
  const oidc = new ProviderNeutralOIDCFixture({
    audience: "flowai-api-gateway-e2e",
    teams: [
      { id: "oidc-team-alpha", name: "Alpha", alias: "forged" },
      { id: "oidc-team-alpha", name: "Duplicate" },
      { id: "oidc-team-beta", name: "Beta" },
    ],
  });
  await oidc.start();
  const postgres = await startPostgresContainer();
  const keys = generateKeyPairSync("rsa", { modulusLength: 2048 });
  const objectGateway = await startGatewayContainer(
    {
      API_GATEWAY_OIDC_ISSUER: oidc.issuer,
      API_GATEWAY_OIDC_CLIENT_ID: "flowai-api-gateway-e2e",
      API_GATEWAY_OIDC_REDIRECT_URI: "${GATEWAY_BASE_URL}/auth/v1/callback",
      API_GATEWAY_OIDC_TEAM_CLAIM_SOURCE: "userinfo",
      API_GATEWAY_OIDC_TEAM_CLAIM_ADAPTER: "object_array",
      API_GATEWAY_OIDC_TEAM_CLAIM_POINTER: "/teams",
      API_GATEWAY_OIDC_TEAM_CLAIM_ID_FIELD: "id",
      API_GATEWAY_OIDC_TEAM_CLAIM_NAME_FIELD: "name",
      API_GATEWAY_OIDC_ADDITIONAL_SCOPES:
        " membership.read,profile,membership.read ",
      API_GATEWAY_OIDC_ENDPOINT_TIMEOUT: "5s",
      API_GATEWAY_POSTGRES_URL: postgres.connectionString,
      API_GATEWAY_SESSION_KEY_HEX: "3a".repeat(32),
      API_GATEWAY_WEB_UI_URL: "${GATEWAY_BASE_URL}/after-login",
      API_GATEWAY_WORKING_TOKEN_PRIVATE_KEY_BASE64: Buffer.from(
        keys.privateKey.export({ format: "pem", type: "pkcs8" }),
      ).toString("base64"),
      API_GATEWAY_WORKING_TOKEN_KEY_ID: "object-array",
      API_GATEWAY_WORKING_TOKEN_ISSUER: "https://gateway.flowai.invalid",
      API_GATEWAY_WORKING_TOKEN_AUDIENCE: "flowai-state-registry",
      API_GATEWAY_WORKING_TOKEN_TTL: "5m",
    },
    { hostNetwork: true },
  );
  try {
    await page.goto(`${objectGateway.baseUrl}/auth/v1/login`);
    await page.waitForURL(`${objectGateway.baseUrl}/after-login`);
    const authorize = oidc.requests.find((value) =>
      value.url.startsWith("/authorize?"),
    );
    expect(authorize).toBeTruthy();
    expect(
      new URL(authorize!.url, oidc.issuer).searchParams
        .get("scope")
        ?.split(" "),
    ).toEqual(["openid", "membership.read", "profile"]);
    const database = new ReadOnlyDatabaseAssertions(postgres.connectionString);
    try {
      expect(
        await database.exactRows(
          `SELECT oidc_team_id FROM api_gateway.membership_observations ORDER BY oidc_team_id`,
        ),
      ).toEqual([
        { oidc_team_id: "oidc-team-alpha" },
        { oidc_team_id: "oidc-team-beta" },
      ]);
    } finally {
      await database.close();
    }
  } finally {
    await objectGateway.close();
    await postgres.close();
    await oidc.stop();
  }
});
